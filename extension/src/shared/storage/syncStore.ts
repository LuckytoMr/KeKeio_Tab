import Dexie, { type Table } from "dexie";
import {
  parseSharedProfileV2,
  sharedProfileToLocalProfile,
  toSharedProfileV2,
  type SharedProfileV2
} from "../profile/sharedProfile";
import type { Profile } from "../profile/types";
import { canonicalJson, sha256Hex } from "../sync/gistBackup";
import { mergeSharedProfiles } from "../sync/merge";

export const quietOutboxDelayMs = 3 * 60 * 1000;
export const maxOutboxWaitMs = 10 * 60 * 1000;
export const maxRetryDelayMs = 30 * 60 * 1000;

export class LocalRevisionConflictError extends Error {
  readonly code = "SYNC_LOCAL_REVISION_CONFLICT";

  constructor(
    readonly expectedRevision: number,
    readonly actualRevision: number
  ) {
    super(`Local profile revision changed from ${expectedRevision} to ${actualRevision}`);
    this.name = "LocalRevisionConflictError";
  }
}

export function computeRetryDelayMs(attempt: number, retryAfterMs?: number, random = Math.random()) {
  const exponentialCap = Math.min(maxRetryDelayMs, 1_000 * 2 ** Math.max(1, attempt));
  const jittered = Math.round(exponentialCap * Math.min(1, Math.max(0, random)));
  return Math.max(jittered, retryAfterMs ?? 0);
}

export type LocalProfileRecord = {
  key: "current";
  revision: number;
  profile: Profile;
  sharedSnapshot: SharedProfileV2;
  sharedHash: string;
};

export type DeviceStateRecord = {
  key: "current";
  deviceId: string;
  localRevision: number;
  hasUserEdits: boolean;
};

export type SyncMetadataRecord = {
  accountScope: string;
  active: 0 | 1;
  sessionGeneration?: string;
  baseUrl: string;
  userId: string;
  profileId: string;
  baseVersion: number;
  baseHash: string;
  baseSnapshot: SharedProfileV2;
  status: "idle" | "dirty" | "syncing" | "conflict" | "error";
  lastSyncedAt?: number;
  lastAttemptAt?: number;
  errorCode?: string;
};

export type OutboxMutation = {
  mutationId: string;
  accountScope: string;
  profileId: string;
  sessionGeneration?: string;
  state: "pending" | "in-flight" | "conflict";
  baseVersion: number | null;
  baseHash?: string;
  schemaVersion: 2;
  profileHash: string;
  canonicalProfileJson: string;
  localRevision: number;
  firstDirtyAt: number;
  dueAt: number;
  maxDueAt: number;
  nextAttemptAt: number;
  attempts: number;
  resolvesConflictId?: string;
  terminalCode?: "SERVER_EMPTY_CONFLICT";
  serverConflictId?: string;
};

export type SyncConflictRecord = {
  conflictId: string;
  accountScope: string;
  profileId: string;
  mutationId: string;
  sessionGeneration?: string;
  base: SharedProfileV2;
  localAtConflict: SharedProfileV2;
  remoteAtConflict: SharedProfileV2;
  remoteVersion: number;
  remoteHash: string;
  serverConflictId?: string;
  createdAt: number;
};

export type ActivateAccountInput = {
  accountScope: string;
  sessionGeneration?: string;
  baseUrl: string;
  userId: string;
  profileId: string;
  baseVersion: number;
  baseHash?: string;
  baseSnapshot: SharedProfileV2;
};

export type AcceptRemoteGuard = {
  sessionGeneration: string;
  expectedLocalRevision: number;
  isSessionCurrent?: () => boolean;
};

export type SyncSessionGuard = {
  sessionGeneration: string;
};

export type SyncAcknowledgement = {
  conflictId?: string;
  version: number;
  profileHash: string;
  profile: SharedProfileV2;
};

class SyncDatabase extends Dexie {
  localProfiles!: Table<LocalProfileRecord, "current">;
  deviceStates!: Table<DeviceStateRecord, "current">;
  syncMetadata!: Table<SyncMetadataRecord, [string, string]>;
  outbox!: Table<OutboxMutation, [string, string, string]>;
  conflicts!: Table<SyncConflictRecord, [string, string, string]>;

  constructor(name: string) {
    super(name);
    this.version(1).stores({
      localProfiles: "&key",
      deviceStates: "&key",
      syncMetadata: "&accountScope,active",
      outbox: "&mutationId,accountScope,[accountScope+state],dueAt,nextAttemptAt",
      conflicts: "&conflictId,accountScope,mutationId"
    });
    this.version(2).stores({
      localProfiles: "&key",
      deviceStates: "&key",
      syncMetadata: "&accountScope,active",
      outbox: "&mutationId,accountScope,[accountScope+state],dueAt,nextAttemptAt",
      conflicts: "&conflictId,accountScope,mutationId",
      syncMetadataV2: "&[accountScope+profileId],accountScope,active,[accountScope+active]",
      outboxV2: "&[accountScope+profileId+mutationId],mutationId,accountScope,profileId,[accountScope+profileId],[accountScope+profileId+state],dueAt,nextAttemptAt",
      conflictsV2: "&[accountScope+profileId+conflictId],conflictId,accountScope,profileId,[accountScope+profileId],mutationId"
    }).upgrade(async (transaction) => {
      const metadata = await transaction.table<SyncMetadataRecord>("syncMetadata").toArray();
      const profileByAccount = new Map(metadata.map((record) => [record.accountScope, record.profileId]));
      await transaction.table<SyncMetadataRecord>("syncMetadataV2").bulkPut(metadata);
      const outbox = await transaction.table<OutboxMutation>("outbox").toArray();
      await transaction.table<OutboxMutation>("outboxV2").bulkPut(outbox.map((record) => {
        const parsed = JSON.parse(record.canonicalProfileJson) as { profileId?: unknown };
        return {
          ...record,
          profileId: record.profileId || (typeof parsed.profileId === "string"
            ? parsed.profileId
            : profileByAccount.get(record.accountScope) ?? "legacy:unknown")
        };
      }));
      const conflicts = await transaction.table<SyncConflictRecord>("conflicts").toArray();
      await transaction.table<SyncConflictRecord>("conflictsV2").bulkPut(conflicts.map((record) => ({
        ...record,
        profileId: record.profileId || record.localAtConflict?.profileId || profileByAccount.get(record.accountScope) || "legacy:unknown"
      })));
    });
    this.version(3).stores({
      localProfiles: "&key",
      deviceStates: "&key",
      syncMetadata: null,
      outbox: null,
      conflicts: null,
      syncMetadataV2: "&[accountScope+profileId],accountScope,active,[accountScope+active]",
      outboxV2: "&[accountScope+profileId+mutationId],mutationId,accountScope,profileId,[accountScope+profileId],[accountScope+profileId+state],dueAt,nextAttemptAt",
      conflictsV2: "&[accountScope+profileId+conflictId],conflictId,accountScope,profileId,[accountScope+profileId],mutationId"
    });
    this.syncMetadata = this.table("syncMetadataV2");
    this.outbox = this.table("outboxV2");
    this.conflicts = this.table("conflictsV2");
  }
}

function metadataKey(accountScope: string, profileId: string): [string, string] {
  return [accountScope, profileId];
}

function mutationKey(accountScope: string, profileId: string, mutationId: string): [string, string, string] {
  return [accountScope, profileId, mutationId];
}

function conflictKey(accountScope: string, profileId: string, conflictId: string): [string, string, string] {
  return [accountScope, profileId, conflictId];
}

function canonicalBaseUrl(rawUrl: string) {
  const url = new URL(rawUrl);
  url.username = "";
  url.password = "";
  url.hash = "";
  url.search = "";
  url.pathname = url.pathname.replace(/\/+$/, "");
  return url.toString().replace(/\/+$/, "");
}

function sessionGenerationMatches(actual: string | undefined, expected: string) {
  return actual === expected || (actual === undefined && expected.startsWith("legacy:"));
}

function assertSessionMetadata(
  metadata: SyncMetadataRecord | undefined,
  accountScope: string,
  profileId: string,
  guard: SyncSessionGuard
) {
  if (!metadata) throw new Error("SYNC_ACCOUNT_NOT_FOUND");
  if (metadata.active !== 1) throw new Error("SYNC_ACCOUNT_NOT_ACTIVE");
  if (metadata.accountScope !== accountScope || metadata.profileId !== profileId) {
    throw new Error("SYNC_ACCOUNT_SCOPE_MISMATCH");
  }
  if (!sessionGenerationMatches(metadata.sessionGeneration, guard.sessionGeneration)) {
    throw new Error("SYNC_SESSION_GENERATION_CHANGED");
  }
  return metadata;
}

function assertMutationSession(mutation: OutboxMutation, guard: SyncSessionGuard) {
  if (!sessionGenerationMatches(mutation.sessionGeneration, guard.sessionGeneration)) {
    throw new Error("SYNC_MUTATION_SESSION_CHANGED");
  }
}

export async function createAccountScope(baseUrl: string, userId: string) {
  return `account:${await sha256Hex(`${canonicalBaseUrl(baseUrl)}\n${userId.trim()}`)}`;
}

export class SyncStore {
  private readonly db: SyncDatabase;

  constructor(name = "FullProSync") {
    this.db = new SyncDatabase(name);
  }

  async delete() {
    this.db.close();
    await Dexie.delete(this.db.name);
  }

  async initialize(profile: Profile, options: { hasUserEdits?: boolean } = {}) {
    const sharedSnapshot = toSharedProfileV2(profile);
    const sharedHash = await sha256Hex(canonicalJson(sharedSnapshot));
    await this.db.transaction("rw", this.db.localProfiles, this.db.deviceStates, async () => {
      const existing = await this.db.localProfiles.get("current");
      if (!existing) {
        await this.db.localProfiles.put({ key: "current", revision: 0, profile, sharedSnapshot, sharedHash });
      }
      const device = await this.db.deviceStates.get("current");
      if (!device) {
        await this.db.deviceStates.put({
          key: "current",
          deviceId: profile.deviceId,
          localRevision: existing?.revision ?? 0,
          hasUserEdits: options.hasUserEdits ?? false
        });
      }
    });
  }

  getLocalProfile() {
    return this.db.localProfiles.get("current");
  }

  getDeviceState() {
    return this.db.deviceStates.get("current");
  }

  getSyncMetadata(accountScope: string, profileId: string) {
    return this.db.syncMetadata.get(metadataKey(accountScope, profileId));
  }

  async getActiveMetadata() {
    return this.db.syncMetadata.where("active").equals(1).first();
  }

  async activateAccount(input: ActivateAccountInput) {
    const baseSnapshot = parseSharedProfileV2(input.baseSnapshot);
    const baseHash = input.baseHash ?? await sha256Hex(canonicalJson(baseSnapshot));
    if (baseHash.trim() === "") throw new Error("SYNC_BASE_HASH_REQUIRED");
    await this.db.transaction("rw", this.db.syncMetadata, async () => {
      await this.db.syncMetadata.toCollection().modify({ active: 0 });
      await this.db.syncMetadata.put({
        accountScope: input.accountScope,
        active: 1,
        sessionGeneration: input.sessionGeneration ?? `legacy:${input.accountScope}`,
        baseUrl: canonicalBaseUrl(input.baseUrl),
        userId: input.userId,
        profileId: input.profileId,
        baseVersion: input.baseVersion,
        baseHash,
        baseSnapshot,
        status: "idle"
      });
    });
  }

  async deactivateAccount(accountScope: string, profileId: string, sessionGeneration?: string) {
    return this.db.transaction("rw", this.db.syncMetadata, async () => {
      const metadata = await this.db.syncMetadata.get(metadataKey(accountScope, profileId));
      if (!metadata) return false;
      if (sessionGeneration && !sessionGenerationMatches(metadata.sessionGeneration, sessionGeneration)) return false;
      await this.db.syncMetadata.update(metadataKey(accountScope, profileId), {
        active: 0,
        ...(sessionGeneration ? { sessionGeneration } : {})
      });
      return true;
    });
  }

  async commitProfile(profile: Profile, now = Date.now(), expectedRevision?: number) {
    const sharedSnapshot = toSharedProfileV2(profile);
    const sharedHash = await sha256Hex(canonicalJson(sharedSnapshot));
    const canonicalProfileJson = canonicalJson(sharedSnapshot);

    return this.db.transaction(
      "rw",
      this.db.localProfiles,
      this.db.deviceStates,
      this.db.syncMetadata,
      this.db.outbox,
      async () => {
        const previous = await this.db.localProfiles.get("current");
        const actualRevision = previous?.revision ?? 0;
        if (expectedRevision !== undefined && actualRevision !== expectedRevision) {
          throw new LocalRevisionConflictError(expectedRevision, actualRevision);
        }
        const revision = (previous?.revision ?? 0) + 1;
        const deviceState = await this.db.deviceStates.get("current");
        const sharedChanged = previous?.sharedHash !== sharedHash;
        await this.db.localProfiles.put({ key: "current", revision, profile, sharedSnapshot, sharedHash });
        await this.db.deviceStates.put({
          key: "current",
          deviceId: profile.deviceId,
          localRevision: revision,
          hasUserEdits: Boolean(deviceState?.hasUserEdits || sharedChanged)
        });

        if (!sharedChanged) return { revision };
        const metadata = await this.db.syncMetadata.where("active").equals(1).first();
        if (!metadata) return { revision };
        if (metadata.profileId !== profile.profileId) throw new Error("SYNC_PROFILE_SCOPE_MISMATCH");

        const inFlight = await this.db.outbox.where("[accountScope+profileId+state]").equals([metadata.accountScope, metadata.profileId, "in-flight"]).first();
        const conflicted = await this.db.outbox.where("[accountScope+profileId+state]").equals([metadata.accountScope, metadata.profileId, "conflict"]).first();
        const blocker = inFlight ?? conflicted;
        const pending = await this.db.outbox.where("[accountScope+profileId+state]").equals([metadata.accountScope, metadata.profileId, "pending"]).first();
        if (!blocker && sharedHash === metadata.baseHash) {
          if (pending) await this.db.outbox.delete(mutationKey(metadata.accountScope, metadata.profileId, pending.mutationId));
          await this.db.syncMetadata.update(metadataKey(metadata.accountScope, metadata.profileId), { status: "idle", errorCode: undefined });
          return { revision };
        }

        const firstDirtyAt = pending?.firstDirtyAt ?? now;
        if (pending) await this.db.outbox.delete(mutationKey(metadata.accountScope, metadata.profileId, pending.mutationId));
        const mutation: OutboxMutation = {
          mutationId: `mut_${crypto.randomUUID()}`,
          accountScope: metadata.accountScope,
          profileId: metadata.profileId,
          sessionGeneration: metadata.sessionGeneration,
          state: "pending",
          baseVersion: blocker ? null : metadata.baseVersion,
          ...(blocker ? {} : { baseHash: metadata.baseHash }),
          schemaVersion: 2,
          profileHash: sharedHash,
          canonicalProfileJson,
          localRevision: revision,
          firstDirtyAt,
          dueAt: now + quietOutboxDelayMs,
          maxDueAt: firstDirtyAt + maxOutboxWaitMs,
          nextAttemptAt: now + quietOutboxDelayMs,
          attempts: 0
        };
        await this.db.outbox.put(mutation);
        await this.db.syncMetadata.update(metadataKey(metadata.accountScope, metadata.profileId), { status: "dirty", errorCode: undefined });
        return { revision, mutation };
      }
    );
  }

  async listOutbox(accountScope: string, profileId: string) {
    const records = await this.db.outbox.where("[accountScope+profileId]").equals([accountScope, profileId]).toArray();
    return records.sort((left, right) => left.firstDirtyAt - right.firstDirtyAt);
  }

  async getNextWakeAt(accountScope: string, profileId: string) {
    const records = await this.listOutbox(accountScope, profileId);
    const wakeTimes = records.flatMap((record) => {
      if (record.state === "conflict" || record.baseVersion === null) return [];
      if (record.state === "in-flight") return [record.nextAttemptAt];
      return [Math.min(record.dueAt, record.maxDueAt)];
    });
    return wakeTimes.length ? Math.min(...wakeTimes) : undefined;
  }

  async getTerminalOutboxMutation(accountScope: string, profileId: string) {
    const mutation = await this.db.outbox
      .where("[accountScope+profileId+state]")
      .equals([accountScope, profileId, "conflict"])
      .first();
    return mutation?.terminalCode ? mutation : undefined;
  }

  async claimDue(accountScope: string, profileId: string, now: number, guard: SyncSessionGuard) {
    return this.db.transaction("rw", this.db.outbox, this.db.syncMetadata, async () => {
      const metadata = assertSessionMetadata(
        await this.db.syncMetadata.get(metadataKey(accountScope, profileId)),
        accountScope,
        profileId,
        guard
      );
      const inFlight = await this.db.outbox.where("[accountScope+profileId+state]").equals([accountScope, profileId, "in-flight"]).first();
      if (inFlight) {
        assertMutationSession(inFlight, guard);
        if (inFlight.nextAttemptAt > now) return undefined;
        const retry = { ...inFlight, attempts: inFlight.attempts + 1, nextAttemptAt: now };
        await this.db.outbox.put(retry);
        return retry;
      }
      const pending = await this.db.outbox.where("[accountScope+profileId+state]").equals([accountScope, profileId, "pending"]).first();
      if (!pending || pending.baseVersion === null || (pending.dueAt > now && pending.maxDueAt > now)) return undefined;
      assertMutationSession(pending, guard);
      const claimed: OutboxMutation = {
        ...pending,
        sessionGeneration: guard.sessionGeneration,
        state: "in-flight",
        attempts: pending.attempts + 1,
        nextAttemptAt: now
      };
      await this.db.outbox.put(claimed);
      await this.db.syncMetadata.put({ ...metadata, status: "syncing", lastAttemptAt: now, errorCode: undefined });
      return claimed;
    });
  }

  async acknowledge(
    accountScope: string,
    profileId: string,
    mutationId: string,
    acknowledgement: SyncAcknowledgement,
    guard: SyncSessionGuard
  ) {
    const serverProfile = parseSharedProfileV2(acknowledgement.profile);
    return this.db.transaction("rw", this.db.outbox, this.db.syncMetadata, async () => {
      const mutation = await this.db.outbox.get(mutationKey(accountScope, profileId, mutationId));
      if (!mutation || mutation.state !== "in-flight") {
        throw new Error("SYNC_ACK_MUTATION_NOT_IN_FLIGHT");
      }
      assertMutationSession(mutation, guard);
      const metadata = assertSessionMetadata(
        await this.db.syncMetadata.get(metadataKey(accountScope, profileId)),
        accountScope,
        profileId,
        guard
      );

      await this.db.outbox.delete(mutationKey(accountScope, profileId, mutationId));
      const successor = await this.db.outbox.where("[accountScope+profileId+state]").equals([accountScope, profileId, "pending"]).first();
      if (successor) {
        assertMutationSession(successor, guard);
        await this.db.outbox.put({ ...successor, baseVersion: acknowledgement.version, baseHash: acknowledgement.profileHash });
      }
      await this.db.syncMetadata.put({
        ...metadata,
        baseVersion: acknowledgement.version,
        baseHash: acknowledgement.profileHash,
        baseSnapshot: serverProfile,
        status: successor ? "dirty" : "idle",
        lastSyncedAt: Date.now(),
        errorCode: undefined
      });
    });
  }

  async acceptRemote(
    accountScope: string,
    profileId: string,
    acknowledgement: SyncAcknowledgement,
    guard: AcceptRemoteGuard
  ) {
    const remote = parseSharedProfileV2(acknowledgement.profile);
    return this.db.transaction(
      "rw",
      this.db.localProfiles,
      this.db.deviceStates,
      this.db.syncMetadata,
      this.db.outbox,
      this.db.conflicts,
      async () => {
        const [metadata, current, deviceState] = await Promise.all([
          this.db.syncMetadata.get(metadataKey(accountScope, profileId)),
          this.db.localProfiles.get("current"),
          this.db.deviceStates.get("current")
        ]);
        if (!metadata) throw new Error("SYNC_ACCOUNT_NOT_FOUND");
        if (metadata.active !== 1) throw new Error("SYNC_ACCOUNT_NOT_ACTIVE");
        if (metadata.accountScope !== accountScope || metadata.profileId !== profileId) {
          throw new Error("SYNC_ACCOUNT_SCOPE_MISMATCH");
        }
        if (!sessionGenerationMatches(metadata.sessionGeneration, guard.sessionGeneration)) {
          throw new Error("SYNC_SESSION_GENERATION_CHANGED");
        }
        if (guard.isSessionCurrent && !guard.isSessionCurrent()) throw new Error("SYNC_SESSION_GENERATION_CHANGED");
        if (!current) throw new Error("SYNC_LOCAL_PROFILE_NOT_FOUND");
        if (current.profile.profileId !== profileId) throw new Error("SYNC_PROFILE_SCOPE_MISMATCH");
        if (current.revision !== guard.expectedLocalRevision) {
          throw new LocalRevisionConflictError(guard.expectedLocalRevision, current.revision);
        }
        const nextProfile = sharedProfileToLocalProfile(remote, current.profile);
        const revision = current.revision + 1;
        await this.db.outbox.where("[accountScope+profileId]").equals([accountScope, profileId]).delete();
        await this.db.conflicts.where("[accountScope+profileId]").equals([accountScope, profileId]).delete();
        if (guard.isSessionCurrent && !guard.isSessionCurrent()) throw new Error("SYNC_SESSION_GENERATION_CHANGED");
        await this.db.localProfiles.put({
          key: "current",
          revision,
          profile: nextProfile,
          sharedSnapshot: remote,
          sharedHash: acknowledgement.profileHash
        });
        await this.db.deviceStates.put({
          key: "current",
          deviceId: nextProfile.deviceId,
          localRevision: revision,
          hasUserEdits: Boolean(deviceState?.hasUserEdits)
        });
        if (remote.profileId !== profileId) await this.db.syncMetadata.delete(metadataKey(accountScope, profileId));
        await this.db.syncMetadata.put({
          ...metadata,
          sessionGeneration: guard.sessionGeneration,
          profileId: remote.profileId,
          baseVersion: acknowledgement.version,
          baseHash: acknowledgement.profileHash,
          baseSnapshot: remote,
          status: "idle",
          lastSyncedAt: Date.now(),
          errorCode: undefined
        });
        return nextProfile;
      }
    );
  }

  async forceEnqueueCurrent(accountScope: string, profileId: string, now: number, guard: SyncSessionGuard) {
    return this.db.transaction("rw", this.db.localProfiles, this.db.syncMetadata, this.db.outbox, this.db.conflicts, async () => {
      const local = await this.db.localProfiles.get("current");
      const metadata = assertSessionMetadata(
        await this.db.syncMetadata.get(metadataKey(accountScope, profileId)),
        accountScope,
        profileId,
        guard
      );
      if (!local) throw new Error("SYNC_ACCOUNT_NOT_INITIALIZED");
      if (local.profile.profileId !== profileId) throw new Error("SYNC_PROFILE_SCOPE_MISMATCH");
      let blocker = (await this.db.outbox.where("[accountScope+profileId+state]").equals([accountScope, profileId, "in-flight"]).first()) ??
        (await this.db.outbox.where("[accountScope+profileId+state]").equals([accountScope, profileId, "conflict"]).first());
      if (blocker && !sessionGenerationMatches(blocker.sessionGeneration, guard.sessionGeneration)) {
        await this.db.outbox.delete(mutationKey(accountScope, profileId, blocker.mutationId));
        await this.db.conflicts.where("[accountScope+profileId]").equals([accountScope, profileId]).delete();
        blocker = undefined;
      }
      if (blocker) throw new Error("SYNC_ACCOUNT_HAS_BLOCKING_MUTATION");
      const pending = await this.db.outbox.where("[accountScope+profileId+state]").equals([accountScope, profileId, "pending"]).first();
      if (pending) await this.db.outbox.delete(mutationKey(accountScope, profileId, pending.mutationId));
      const mutation: OutboxMutation = {
        mutationId: `mut_${crypto.randomUUID()}`,
        accountScope,
        profileId,
        sessionGeneration: guard.sessionGeneration,
        state: "pending",
        baseVersion: metadata.baseVersion,
        baseHash: metadata.baseHash,
        schemaVersion: 2,
        profileHash: local.sharedHash,
        canonicalProfileJson: canonicalJson(local.sharedSnapshot),
        localRevision: local.revision,
        firstDirtyAt: now,
        dueAt: now,
        maxDueAt: now + maxOutboxWaitMs,
        nextAttemptAt: now,
        attempts: 0
      };
      await this.db.outbox.put(mutation);
      await this.db.syncMetadata.update(metadataKey(accountScope, profileId), { status: "dirty", errorCode: undefined });
      return mutation;
    });
  }

  getConflict(accountScope: string, profileId: string, conflictId: string) {
    return this.db.conflicts.get(conflictKey(accountScope, profileId, conflictId));
  }

  getActiveConflict(accountScope: string, profileId: string) {
    return this.db.conflicts.where("[accountScope+profileId]").equals([accountScope, profileId]).first();
  }

  async freezeServerEmptyConflict(
    accountScope: string,
    profileId: string,
    mutationId: string,
    serverConflictId: string,
    guard: SyncSessionGuard
  ) {
    return this.db.transaction("rw", this.db.outbox, this.db.syncMetadata, async () => {
      const mutation = await this.db.outbox.get(mutationKey(accountScope, profileId, mutationId));
      const metadata = await this.db.syncMetadata.get(metadataKey(accountScope, profileId));
      if (!mutation || mutation.state !== "in-flight") {
        throw new Error("SYNC_CONFLICT_MUTATION_NOT_IN_FLIGHT");
      }
      assertMutationSession(mutation, guard);
      assertSessionMetadata(metadata, accountScope, profileId, guard);
      const frozen: OutboxMutation = {
        ...mutation,
        state: "conflict",
        terminalCode: "SERVER_EMPTY_CONFLICT",
        serverConflictId
      };
      await this.db.outbox.put(frozen);
      await this.db.syncMetadata.update(metadataKey(accountScope, profileId), {
        status: "conflict",
        errorCode: "SERVER_EMPTY_CONFLICT"
      });
      return frozen;
    });
  }

  async recordConflict(
    accountScope: string,
    profileId: string,
    mutationId: string,
    remote: SyncAcknowledgement,
    now: number,
    guard: SyncSessionGuard
  ) {
    const remoteProfile = parseSharedProfileV2(remote.profile);
    return this.db.transaction("rw", this.db.outbox, this.db.syncMetadata, this.db.conflicts, async () => {
      const mutation = await this.db.outbox.get(mutationKey(accountScope, profileId, mutationId));
      const metadata = await this.db.syncMetadata.get(metadataKey(accountScope, profileId));
      if (!mutation || mutation.state !== "in-flight") {
        throw new Error("SYNC_CONFLICT_MUTATION_NOT_IN_FLIGHT");
      }
      assertMutationSession(mutation, guard);
      const guardedMetadata = assertSessionMetadata(metadata, accountScope, profileId, guard);
      const conflict: SyncConflictRecord = {
        conflictId: `conf_${crypto.randomUUID()}`,
        accountScope,
        profileId,
        mutationId,
        sessionGeneration: guard.sessionGeneration,
        base: parseSharedProfileV2(guardedMetadata.baseSnapshot),
        localAtConflict: parseSharedProfileV2(JSON.parse(mutation.canonicalProfileJson) as unknown),
        remoteAtConflict: remoteProfile,
        remoteVersion: remote.version,
        remoteHash: remote.profileHash,
        serverConflictId: remote.conflictId,
        createdAt: now
      };
      await this.db.conflicts.put(conflict);
      await this.db.outbox.put({ ...mutation, state: "conflict" });
      await this.db.syncMetadata.update(metadataKey(accountScope, profileId), { status: "conflict", errorCode: "PROFILE_CONFLICT" });
      return conflict;
    });
  }

  async resolveConflict(
    accountScope: string,
    profileId: string,
    conflictId: string,
    resolvedInput: SharedProfileV2,
    now: number,
    guard: SyncSessionGuard
  ) {
    const conflict = await this.db.conflicts.get(conflictKey(accountScope, profileId, conflictId));
    const localRecord = await this.db.localProfiles.get("current");
    if (!conflict || conflict.accountScope !== accountScope) throw new Error("SYNC_CONFLICT_NOT_FOUND");
    if (!localRecord) throw new Error("SYNC_LOCAL_PROFILE_NOT_FOUND");
    if (localRecord.profile.profileId !== profileId) throw new Error("SYNC_PROFILE_SCOPE_MISMATCH");
    if (!sessionGenerationMatches(conflict.sessionGeneration, guard.sessionGeneration)) {
      throw new Error("SYNC_CONFLICT_SESSION_CHANGED");
    }
    const successor = await this.db.outbox.where("[accountScope+profileId+state]").equals([accountScope, profileId, "pending"]).first();
    const branch = successor
      ? parseSharedProfileV2(JSON.parse(successor.canonicalProfileJson) as unknown)
      : conflict.localAtConflict;
    const resolved = parseSharedProfileV2(resolvedInput);
    const branchMerge = mergeSharedProfiles(conflict.localAtConflict, branch, resolved);
    if (!branchMerge.valid) throw new Error("SYNC_CONFLICT_BRANCH_REBASE_REQUIRED");

    const canonicalProfileJson = canonicalJson(branchMerge.merged);
    const profileHash = await sha256Hex(canonicalProfileJson);
    const nextLocal = sharedProfileToLocalProfile(branchMerge.merged, localRecord.profile);
    const revision = localRecord.revision + 1;

    await this.db.transaction(
      "rw",
      this.db.localProfiles,
      this.db.deviceStates,
      this.db.syncMetadata,
      this.db.outbox,
      this.db.conflicts,
      async () => {
        const currentConflict = await this.db.conflicts.get(conflictKey(accountScope, profileId, conflictId));
        if (!currentConflict) throw new Error("SYNC_CONFLICT_CHANGED");
        if (!sessionGenerationMatches(currentConflict.sessionGeneration, guard.sessionGeneration)) {
          throw new Error("SYNC_CONFLICT_SESSION_CHANGED");
        }
        const metadata = assertSessionMetadata(
          await this.db.syncMetadata.get(metadataKey(accountScope, profileId)),
          accountScope,
          profileId,
          guard
        );
        const currentLocal = await this.db.localProfiles.get("current");
        if (!currentLocal) throw new Error("SYNC_LOCAL_PROFILE_NOT_FOUND");
        if (currentLocal.revision !== localRecord.revision) {
          throw new LocalRevisionConflictError(localRecord.revision, currentLocal.revision);
        }
        if (successor) assertMutationSession(successor, guard);
        await this.db.outbox.delete(mutationKey(accountScope, profileId, currentConflict.mutationId));
        if (successor) await this.db.outbox.delete(mutationKey(accountScope, profileId, successor.mutationId));
        await this.db.conflicts.delete(conflictKey(accountScope, profileId, conflictId));
        await this.db.localProfiles.put({
          key: "current",
          revision,
          profile: nextLocal,
          sharedSnapshot: branchMerge.merged,
          sharedHash: profileHash
        });
        await this.db.deviceStates.put({
          key: "current",
          deviceId: nextLocal.deviceId,
          localRevision: revision,
          hasUserEdits: true
        });
        const firstDirtyAt = Math.min(currentConflict.createdAt, successor?.firstDirtyAt ?? now);
        await this.db.outbox.put({
          mutationId: `mut_${crypto.randomUUID()}`,
          accountScope,
          profileId,
          sessionGeneration: guard.sessionGeneration,
          state: "pending",
          baseVersion: currentConflict.remoteVersion,
          baseHash: currentConflict.remoteHash,
          schemaVersion: 2,
          profileHash,
          canonicalProfileJson,
          localRevision: revision,
          firstDirtyAt,
          dueAt: now,
          maxDueAt: firstDirtyAt + maxOutboxWaitMs,
          nextAttemptAt: now,
          attempts: 0,
          ...(currentConflict.serverConflictId ? { resolvesConflictId: currentConflict.serverConflictId } : {})
        });
        await this.db.syncMetadata.put({
          ...metadata,
          baseVersion: currentConflict.remoteVersion,
          baseHash: currentConflict.remoteHash,
          baseSnapshot: currentConflict.remoteAtConflict,
          status: "dirty",
          errorCode: undefined
        });
      }
    );
    return { profile: branchMerge.merged, revision };
  }

  async scheduleRetry(
    accountScope: string,
    profileId: string,
    mutationId: string,
    options: { retryAfterMs?: number; errorCode: string },
    now: number,
    guard: SyncSessionGuard
  ) {
    return this.db.transaction("rw", this.db.outbox, this.db.syncMetadata, async () => {
      const mutation = await this.db.outbox.get(mutationKey(accountScope, profileId, mutationId));
      if (!mutation || mutation.state !== "in-flight") {
        throw new Error("SYNC_RETRY_MUTATION_NOT_IN_FLIGHT");
      }
      assertMutationSession(mutation, guard);
      const metadata = assertSessionMetadata(
        await this.db.syncMetadata.get(metadataKey(accountScope, profileId)),
        accountScope,
        profileId,
        guard
      );
      const delay = computeRetryDelayMs(mutation.attempts, options.retryAfterMs);
      await this.db.outbox.put({ ...mutation, nextAttemptAt: now + delay });
      await this.db.syncMetadata.put({ ...metadata, status: "error", errorCode: options.errorCode });
      return delay;
    });
  }
}
