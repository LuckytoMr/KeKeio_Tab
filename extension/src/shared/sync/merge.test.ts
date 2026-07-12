import { describe, expect, it } from "vitest";
import { createDefaultProfile } from "../profile/defaults";
import { toSharedProfileV2 } from "../profile/sharedProfile";
import { mergeSharedProfiles, resolveMergeConflicts } from "./merge";

function baseProfile() {
  return toSharedProfileV2(createDefaultProfile());
}

describe("three-way SharedProfileV2 merge", () => {
  it("automatically combines non-overlapping entity and scalar changes", () => {
    const base = baseProfile();
    const local = structuredClone(base);
    const remote = structuredClone(base);
    local.groups[0].title = "本机分组";
    local.groups[0].updatedAt = "2026-07-12T01:00:00.000Z";
    remote.theme.showBrand = true;
    remote.updatedAt = "2026-07-12T02:00:00.000Z";

    const result = mergeSharedProfiles(base, local, remote);

    expect(result.conflicts).toEqual([]);
    expect(result.merged.groups[0].title).toBe("本机分组");
    expect(result.merged.theme.showBrand).toBe(true);
  });

  it("reports deterministic JSON Pointer conflicts for concurrent edits to the same entity", () => {
    const base = baseProfile();
    const local = structuredClone(base);
    const remote = structuredClone(base);
    local.shortcuts[0].title = "本机标题";
    local.shortcuts[0].updatedAt = "2026-07-12T01:00:00.000Z";
    remote.shortcuts[0].title = "云端标题";
    remote.shortcuts[0].updatedAt = "2026-07-12T02:00:00.000Z";

    const result = mergeSharedProfiles(base, local, remote);

    expect(result.conflicts).toMatchObject([
      { path: `/shortcuts/${base.shortcuts[0].id}`, kind: "entity-edit-edit", canKeepBoth: true }
    ]);
  });

  it("treats delete versus modification as an explicit conflict", () => {
    const base = baseProfile();
    const local = structuredClone(base);
    const remote = structuredClone(base);
    local.shortcuts[0].deletedAt = "2026-07-12T01:00:00.000Z";
    local.shortcuts[0].updatedAt = local.shortcuts[0].deletedAt;
    remote.shortcuts[0].title = "云端修改";
    remote.shortcuts[0].updatedAt = "2026-07-12T02:00:00.000Z";

    const result = mergeSharedProfiles(base, local, remote);

    expect(result.conflicts[0]).toMatchObject({
      path: `/shortcuts/${base.shortcuts[0].id}`,
      kind: "delete-modify",
      canKeepBoth: false
    });
  });

  it("rejects parent deletion merged with a child created or moved into that group", () => {
    const base = baseProfile();
    const local = structuredClone(base);
    const remote = structuredClone(base);
    local.groups[0].deletedAt = "2026-07-12T01:00:00.000Z";
    local.groups[0].updatedAt = local.groups[0].deletedAt;
    remote.shortcuts.push({
      ...remote.shortcuts[0],
      id: "shortcut:remote-new",
      title: "Remote New",
      createdAt: "2026-07-12T02:00:00.000Z",
      updatedAt: "2026-07-12T02:00:00.000Z"
    });

    const result = mergeSharedProfiles(base, local, remote);

    expect(result.conflicts.some((conflict) => conflict.kind === "parent-delete-child-change")).toBe(true);
    expect(result.valid).toBe(false);
  });

  it("does not use updatedAt as last-writer-wins authority for a scalar conflict", () => {
    const base = baseProfile();
    const local = structuredClone(base);
    const remote = structuredClone(base);
    local.theme.columns = 4;
    local.updatedAt = "2026-07-12T01:00:00.000Z";
    remote.theme.columns = 7;
    remote.updatedAt = "2026-07-12T03:00:00.000Z";

    const result = mergeSharedProfiles(base, local, remote);

    expect(result.conflicts).toMatchObject([{ path: "/theme/columns", kind: "scalar-edit-edit", canKeepBoth: false }]);
  });

  it("applies a per-path decision while retaining non-conflicting automatic changes", () => {
    const base = baseProfile();
    const local = structuredClone(base);
    const remote = structuredClone(base);
    local.theme.columns = 4;
    local.theme.showBrand = true;
    remote.theme.columns = 7;

    const resolved = resolveMergeConflicts(base, local, remote, { "/theme/columns": "remote" });

    expect(resolved.theme).toMatchObject({ columns: 7, showBrand: true });
  });

  it("keeps both clonable group versions with new IDs and repaired child references", () => {
    const base = baseProfile();
    const local = structuredClone(base);
    const remote = structuredClone(base);
    const groupId = base.groups[0].id;
    local.groups[0].title = "本机分组";
    local.groups[0].updatedAt = "2026-07-12T01:00:00.000Z";
    remote.groups[0].title = "云端分组";
    remote.groups[0].updatedAt = "2026-07-12T02:00:00.000Z";

    const resolved = resolveMergeConflicts(
      base,
      local,
      remote,
      { [`/groups/${groupId}`]: "both" },
      (kind, originalId) => `${kind === "groups" ? "group" : "shortcut"}:${originalId}:remote-copy`
    );
    const clonedGroup = resolved.groups.find((group) => group.title === "云端分组");
    const clonedChildren = resolved.shortcuts.filter((shortcut) => shortcut.groupId === clonedGroup?.id);

    expect(resolved.groups.find((group) => group.id === groupId)?.title).toBe("本机分组");
    expect(clonedGroup?.id).not.toBe(groupId);
    expect(clonedChildren).toHaveLength(remote.shortcuts.filter((shortcut) => shortcut.groupId === groupId && !shortcut.deletedAt).length);
    expect(clonedChildren.every((shortcut) => shortcut.id.includes("remote-copy"))).toBe(true);
  });

  it("requires an explicit decision for every conflict and disallows keep-both for scalars", () => {
    const base = baseProfile();
    const local = structuredClone(base);
    const remote = structuredClone(base);
    local.theme.columns = 4;
    remote.theme.columns = 7;

    expect(() => resolveMergeConflicts(base, local, remote, {})).toThrow("MERGE_DECISION_REQUIRED");
    expect(() => resolveMergeConflicts(base, local, remote, { "/theme/columns": "both" })).toThrow("MERGE_KEEP_BOTH_NOT_ALLOWED");
  });

  it("merges non-overlapping wallpaper selectedIds additions as set items", () => {
    const base = baseProfile();
    const local = structuredClone(base);
    const remote = structuredClone(base);
    local.wallpaper.selectedIds = [...base.wallpaper.selectedIds, "wallpaper:local-added"];
    remote.wallpaper.selectedIds = [...base.wallpaper.selectedIds, "wallpaper:remote-added"];

    const result = mergeSharedProfiles(base, local, remote);

    expect(result.conflicts.some((conflict) => conflict.path === "/wallpaper/selectedIds")).toBe(false);
    expect(result.merged.wallpaper.selectedIds).toEqual([
      ...base.wallpaper.selectedIds,
      "wallpaper:local-added",
      "wallpaper:remote-added"
    ]);
  });
});
