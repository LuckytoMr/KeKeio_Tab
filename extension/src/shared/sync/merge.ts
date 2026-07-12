import { parseSharedProfileV2, type SharedProfileV2 } from "../profile/sharedProfile";
import { canonicalJson } from "./gistBackup";

export type MergeConflictKind =
  | "entity-edit-edit"
  | "delete-modify"
  | "scalar-edit-edit"
  | "order-edit-edit"
  | "parent-delete-child-change";

export type MergeConflict = {
  path: string;
  kind: MergeConflictKind;
  base: unknown;
  local: unknown;
  remote: unknown;
  canKeepBoth: boolean;
};

export type MergeResult = {
  merged: SharedProfileV2;
  conflicts: MergeConflict[];
  valid: boolean;
};

export type MergeConflictChoice = "local" | "remote" | "both";
export type MergeConflictDecisions = Record<string, MergeConflictChoice>;
export type MergeCloneIdFactory = (kind: "groups" | "shortcuts", originalId: string) => string;

function equal(left: unknown, right: unknown) {
  return canonicalJson(left) === canonicalJson(right);
}

function clone<T>(value: T): T {
  return structuredClone(value);
}

function pointerSegment(value: string) {
  return value.replace(/~/g, "~0").replace(/\//g, "~1");
}

function decodePointerSegment(value: string) {
  return value.replace(/~1/g, "/").replace(/~0/g, "~");
}

function pointerParts(path: string) {
  return path.split("/").slice(1).map(decodePointerSegment);
}

function setConflictValue(profile: SharedProfileV2, conflict: MergeConflict, value: unknown) {
  const [root, entityId, property] = pointerParts(conflict.path);
  if ((root === "groups" || root === "shortcuts") && entityId) {
    const collection = profile[root] as Array<Record<string, unknown> & { id: string }>;
    const index = collection.findIndex((entity) => entity.id === entityId);
    if (property) {
      if (index < 0) throw new Error(`MERGE_ENTITY_NOT_FOUND:${conflict.path}`);
      collection[index] = { ...collection[index], [property]: clone(value) };
      return;
    }
    if (value === undefined) {
      if (index >= 0) collection.splice(index, 1);
      return;
    }
    const entity = clone(value) as Record<string, unknown> & { id: string };
    if (index >= 0) collection[index] = entity;
    else collection.push(entity);
    return;
  }

  const parts = pointerParts(conflict.path);
  let target = profile as unknown as Record<string, unknown>;
  for (const segment of parts.slice(0, -1)) {
    const next = target[segment];
    if (!next || typeof next !== "object" || Array.isArray(next)) throw new Error(`MERGE_PATH_INVALID:${conflict.path}`);
    target = next as Record<string, unknown>;
  }
  target[parts.at(-1)!] = clone(value);
}

function defaultCloneId(kind: "groups" | "shortcuts") {
  return `${kind === "groups" ? "group" : "shortcut"}:copy:${crypto.randomUUID()}`;
}

function keepBothEntity(
  merged: SharedProfileV2,
  remote: SharedProfileV2,
  conflict: MergeConflict,
  createId: MergeCloneIdFactory
) {
  const [root, entityId, property] = pointerParts(conflict.path);
  if ((root !== "groups" && root !== "shortcuts") || !entityId || property) {
    throw new Error(`MERGE_KEEP_BOTH_NOT_ALLOWED:${conflict.path}`);
  }
  setConflictValue(merged, conflict, conflict.local);
  const remoteEntity = conflict.remote as Record<string, unknown> & { id?: unknown };
  if (!remoteEntity || typeof remoteEntity.id !== "string") throw new Error(`MERGE_KEEP_BOTH_INVALID:${conflict.path}`);

  if (root === "shortcuts") {
    const cloneId = createId("shortcuts", remoteEntity.id);
    if (merged.shortcuts.some((shortcut) => shortcut.id === cloneId)) throw new Error(`MERGE_CLONE_ID_COLLISION:${cloneId}`);
    merged.shortcuts.push({ ...clone(remoteEntity), id: cloneId } as SharedProfileV2["shortcuts"][number]);
    return;
  }

  const cloneId = createId("groups", remoteEntity.id);
  if (merged.groups.some((group) => group.id === cloneId)) throw new Error(`MERGE_CLONE_ID_COLLISION:${cloneId}`);
  merged.groups.push({ ...clone(remoteEntity), id: cloneId } as SharedProfileV2["groups"][number]);
  for (const child of remote.shortcuts.filter((shortcut) => shortcut.groupId === entityId && !shortcut.deletedAt)) {
    const childCloneId = createId("shortcuts", child.id);
    if (merged.shortcuts.some((shortcut) => shortcut.id === childCloneId)) {
      throw new Error(`MERGE_CLONE_ID_COLLISION:${childCloneId}`);
    }
    merged.shortcuts.push({ ...clone(child), id: childCloneId, groupId: cloneId });
  }
}

function isDeleted(entity: unknown) {
  return !entity || (typeof entity === "object" && entity !== null && typeof (entity as { deletedAt?: unknown }).deletedAt === "string");
}

function mergeScalar<T>(path: string, base: T, local: T, remote: T, conflicts: MergeConflict[]): T {
  if (equal(local, remote)) return clone(local);
  if (equal(local, base)) return clone(remote);
  if (equal(remote, base)) return clone(local);
  conflicts.push({
    path,
    kind: Array.isArray(base) ? "order-edit-edit" : "scalar-edit-edit",
    base: clone(base),
    local: clone(local),
    remote: clone(remote),
    canKeepBoth: false
  });
  return clone(local);
}

function mergeStringSet(
  path: string,
  base: string[],
  local: string[],
  remote: string[],
  conflicts: MergeConflict[]
) {
  if (equal(local, remote)) return clone(local);
  if (equal(local, base)) return clone(remote);
  if (equal(remote, base)) return clone(local);

  const baseSet = new Set(base);
  const localSet = new Set(local);
  const remoteSet = new Set(remote);
  const finalSet = new Set<string>();
  for (const item of new Set([...base, ...local, ...remote])) {
    const inBase = baseSet.has(item);
    const inLocal = localSet.has(item);
    const inRemote = remoteSet.has(item);
    const included = inLocal === inRemote
      ? inLocal
      : inLocal === inBase
        ? inRemote
        : inLocal;
    if (included) finalSet.add(item);
  }

  const baseOrder = base.filter((item) => finalSet.has(item));
  const localOrder = local.filter((item) => baseSet.has(item) && finalSet.has(item));
  const remoteOrder = remote.filter((item) => baseSet.has(item) && finalSet.has(item));
  const localReordered = !equal(localOrder, baseOrder);
  const remoteReordered = !equal(remoteOrder, baseOrder);
  let retainedOrder = baseOrder;
  if (localReordered && remoteReordered && !equal(localOrder, remoteOrder)) {
    conflicts.push({
      path,
      kind: "order-edit-edit",
      base: clone(base),
      local: clone(local),
      remote: clone(remote),
      canKeepBoth: false
    });
    retainedOrder = localOrder;
  } else if (localReordered) {
    retainedOrder = localOrder;
  } else if (remoteReordered) {
    retainedOrder = remoteOrder;
  }

  const additions = Array.from(finalSet).filter((item) => !baseSet.has(item)).sort((left, right) => left.localeCompare(right));
  return [...retainedOrder, ...additions];
}

function mergeEntities<T extends { id: string }>(
  collection: "groups" | "shortcuts",
  base: T[],
  local: T[],
  remote: T[],
  conflicts: MergeConflict[]
) {
  const baseById = new Map(base.map((entity) => [entity.id, entity]));
  const localById = new Map(local.map((entity) => [entity.id, entity]));
  const remoteById = new Map(remote.map((entity) => [entity.id, entity]));
  const ids = Array.from(new Set([...baseById.keys(), ...localById.keys(), ...remoteById.keys()]));
  const merged: T[] = [];

  for (const id of ids) {
    const baseEntity = baseById.get(id);
    const localEntity = localById.get(id);
    const remoteEntity = remoteById.get(id);
    const localChanged = !equal(localEntity, baseEntity);
    const remoteChanged = !equal(remoteEntity, baseEntity);
    let selected: T | undefined;

    if (!localChanged) selected = remoteEntity;
    else if (!remoteChanged || equal(localEntity, remoteEntity)) selected = localEntity;
    else {
      const deleteModify = isDeleted(localEntity) !== isDeleted(remoteEntity);
      conflicts.push({
        path: `/${collection}/${pointerSegment(id)}`,
        kind: deleteModify ? "delete-modify" : "entity-edit-edit",
        base: clone(baseEntity),
        local: clone(localEntity),
        remote: clone(remoteEntity),
        canKeepBoth: !deleteModify
      });
      selected = localEntity;
    }

    if (selected) merged.push(clone(selected));
  }
  return merged;
}

export function mergeSharedProfiles(
  baseInput: SharedProfileV2,
  localInput: SharedProfileV2,
  remoteInput: SharedProfileV2
): MergeResult {
  const base = parseSharedProfileV2(baseInput);
  const local = parseSharedProfileV2(localInput);
  const remote = parseSharedProfileV2(remoteInput);
  const conflicts: MergeConflict[] = [];

  const merged: SharedProfileV2 = {
    schemaVersion: 2,
    profileId: mergeScalar("/profileId", base.profileId, local.profileId, remote.profileId, conflicts),
    updatedAt: [base.updatedAt, local.updatedAt, remote.updatedAt].sort().at(-1)!,
    groups: mergeEntities("groups", base.groups, local.groups, remote.groups, conflicts),
    shortcuts: mergeEntities("shortcuts", base.shortcuts, local.shortcuts, remote.shortcuts, conflicts),
    search: {
      mode: mergeScalar("/search/mode", base.search.mode, local.search.mode, remote.search.mode, conflicts),
      disposition: mergeScalar(
        "/search/disposition",
        base.search.disposition,
        local.search.disposition,
        remote.search.disposition,
        conflicts
      ),
      selectedEngineId: mergeScalar(
        "/search/selectedEngineId",
        base.search.selectedEngineId,
        local.search.selectedEngineId,
        remote.search.selectedEngineId,
        conflicts
      ),
      engines: mergeScalar("/search/engines", base.search.engines, local.search.engines, remote.search.engines, conflicts)
    },
    wallpaper: {
      selected: mergeScalar(
        "/wallpaper/selected",
        base.wallpaper.selected,
        local.wallpaper.selected,
        remote.wallpaper.selected,
        conflicts
      ),
      selectedIds: mergeStringSet(
        "/wallpaper/selectedIds",
        base.wallpaper.selectedIds,
        local.wallpaper.selectedIds,
        remote.wallpaper.selectedIds,
        conflicts
      ),
      rotationMode: mergeScalar(
        "/wallpaper/rotationMode",
        base.wallpaper.rotationMode,
        local.wallpaper.rotationMode,
        remote.wallpaper.rotationMode,
        conflicts
      ),
      rotationSource: mergeScalar(
        "/wallpaper/rotationSource",
        base.wallpaper.rotationSource,
        local.wallpaper.rotationSource,
        remote.wallpaper.rotationSource,
        conflicts
      ),
      rotationIntervalSeconds: mergeScalar(
        "/wallpaper/rotationIntervalSeconds",
        base.wallpaper.rotationIntervalSeconds,
        local.wallpaper.rotationIntervalSeconds,
        remote.wallpaper.rotationIntervalSeconds,
        conflicts
      ),
      overlayOpacity: mergeScalar(
        "/wallpaper/overlayOpacity",
        base.wallpaper.overlayOpacity,
        local.wallpaper.overlayOpacity,
        remote.wallpaper.overlayOpacity,
        conflicts
      ),
      blur: mergeScalar("/wallpaper/blur", base.wallpaper.blur, local.wallpaper.blur, remote.wallpaper.blur, conflicts)
    },
    theme: {
      styleId: mergeScalar("/theme/styleId", base.theme.styleId, local.theme.styleId, remote.theme.styleId, conflicts),
      density: mergeScalar("/theme/density", base.theme.density, local.theme.density, remote.theme.density, conflicts),
      sidebarSide: mergeScalar(
        "/theme/sidebarSide",
        base.theme.sidebarSide,
        local.theme.sidebarSide,
        remote.theme.sidebarSide,
        conflicts
      ),
      showBrand: mergeScalar("/theme/showBrand", base.theme.showBrand, local.theme.showBrand, remote.theme.showBrand, conflicts),
      columns: mergeScalar("/theme/columns", base.theme.columns, local.theme.columns, remote.theme.columns, conflicts),
      rows: mergeScalar("/theme/rows", base.theme.rows, local.theme.rows, remote.theme.rows, conflicts),
      iconSize: mergeScalar("/theme/iconSize", base.theme.iconSize, local.theme.iconSize, remote.theme.iconSize, conflicts),
      iconShape: mergeScalar("/theme/iconShape", base.theme.iconShape, local.theme.iconShape, remote.theme.iconShape, conflicts)
    }
  };

  const activeGroups = new Set(merged.groups.filter((group) => !group.deletedAt).map((group) => group.id));
  for (const shortcut of merged.shortcuts) {
    if (shortcut.deletedAt || activeGroups.has(shortcut.groupId)) continue;
    conflicts.push({
      path: `/shortcuts/${pointerSegment(shortcut.id)}/groupId`,
      kind: "parent-delete-child-change",
      base: base.shortcuts.find((item) => item.id === shortcut.id)?.groupId,
      local: local.shortcuts.find((item) => item.id === shortcut.id)?.groupId,
      remote: remote.shortcuts.find((item) => item.id === shortcut.id)?.groupId,
      canKeepBoth: false
    });
  }

  return { merged, conflicts, valid: conflicts.length === 0 };
}

export function resolveMergeConflicts(
  baseInput: SharedProfileV2,
  localInput: SharedProfileV2,
  remoteInput: SharedProfileV2,
  decisions: MergeConflictDecisions,
  createId: MergeCloneIdFactory = defaultCloneId
) {
  const remote = parseSharedProfileV2(remoteInput);
  const result = mergeSharedProfiles(baseInput, localInput, remote);
  const resolved = clone(result.merged);

  for (const conflict of result.conflicts) {
    const choice = decisions[conflict.path];
    if (!choice) throw new Error(`MERGE_DECISION_REQUIRED:${conflict.path}`);
    if (choice === "both") {
      if (!conflict.canKeepBoth) throw new Error(`MERGE_KEEP_BOTH_NOT_ALLOWED:${conflict.path}`);
      keepBothEntity(resolved, remote, conflict, createId);
    } else {
      setConflictValue(resolved, conflict, choice === "local" ? conflict.local : conflict.remote);
    }
  }

  const parsed = parseSharedProfileV2(resolved);
  const activeGroups = new Set(parsed.groups.filter((group) => !group.deletedAt).map((group) => group.id));
  if (parsed.shortcuts.some((shortcut) => !shortcut.deletedAt && !activeGroups.has(shortcut.groupId))) {
    throw new Error("MERGE_REFERENCE_INTEGRITY_FAILED");
  }
  return parsed;
}
