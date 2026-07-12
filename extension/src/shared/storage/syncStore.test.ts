import "fake-indexeddb/auto";
import Dexie from "dexie";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createDefaultProfile } from "../profile/defaults";
import { toSharedProfileV2 } from "../profile/sharedProfile";
import {
  computeRetryDelayMs,
  createAccountScope,
  maxOutboxWaitMs,
  quietOutboxDelayMs,
  SyncStore
} from "./syncStore";

describe("SyncStore", () => {
  let store: SyncStore;

  beforeEach(() => {
    store = new SyncStore(`FullProSyncTest-${crypto.randomUUID()}`);
  });

  afterEach(async () => {
    await store.delete();
  });

  it("atomically advances the local revision and creates an account-scoped outbox mutation", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test/", "user:one");
    await store.activateAccount({
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 12,
      baseSnapshot: toSharedProfileV2(initial)
    });

    const changed = { ...initial, updatedAt: "2026-07-12T01:00:00.000Z", theme: { ...initial.theme, density: "compact" as const } };
    const committed = await store.commitProfile(changed, Date.parse("2026-07-12T01:00:00.000Z"));
    const local = await store.getLocalProfile();
    const outbox = await store.listOutbox(accountScope, initial.profileId);

    expect(committed.revision).toBe(1);
    expect(local).toMatchObject({ revision: 1, profile: changed });
    expect(outbox).toHaveLength(1);
    expect(outbox[0]).toMatchObject({
      accountScope,
      state: "pending",
      baseVersion: 12,
      schemaVersion: 2,
      localRevision: 1,
      dueAt: Date.parse("2026-07-12T01:00:00.000Z") + quietOutboxDelayMs,
      maxDueAt: Date.parse("2026-07-12T01:00:00.000Z") + maxOutboxWaitMs
    });
    expect(await store.getNextWakeAt(accountScope, initial.profileId)).toBe(Date.parse("2026-07-12T01:00:00.000Z") + quietOutboxDelayMs);
  });

  it("uses the server-confirmed profile hash as the activated account base hash", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 12,
      baseHash: "server-confirmed-hash",
      baseSnapshot: toSharedProfileV2(initial)
    });

    expect(await store.getSyncMetadata(accountScope, initial.profileId)).toMatchObject({ baseHash: "server-confirmed-hash" });
  });

  it("keeps one immutable in-flight mutation and coalesces later edits into one successor", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 4,
      baseSnapshot: toSharedProfileV2(initial)
    });

    const first = { ...initial, updatedAt: "2026-07-12T01:00:00.000Z", theme: { ...initial.theme, density: "compact" as const } };
    await store.commitProfile(first, 1_000);
    const guard = { sessionGeneration: `legacy:${accountScope}` };
    const claimed = await store.claimDue(accountScope, initial.profileId, 1_000 + quietOutboxDelayMs, guard);
    expect(claimed?.state).toBe("in-flight");
    const immutableBody = claimed?.canonicalProfileJson;

    const second = { ...first, updatedAt: "2026-07-12T01:01:00.000Z", theme: { ...first.theme, showBrand: true } };
    await store.commitProfile(second, 2_000);
    const third = { ...second, updatedAt: "2026-07-12T01:02:00.000Z", theme: { ...second.theme, columns: 7 as const } };
    await store.commitProfile(third, 3_000);

    const queued = await store.listOutbox(accountScope, initial.profileId);
    expect(queued).toHaveLength(2);
    expect(queued.find((item) => item.state === "in-flight")?.canonicalProfileJson).toBe(immutableBody);
    const successor = queued.find((item) => item.state === "pending")!;
    expect(successor.baseVersion).toBeNull();
    expect(successor.firstDirtyAt).toBe(2_000);
    expect(JSON.parse(successor.canonicalProfileJson).theme.columns).toBe(7);

    await store.acknowledge(accountScope, initial.profileId, claimed!.mutationId, {
      version: 5,
      profileHash: claimed!.profileHash,
      profile: JSON.parse(claimed!.canonicalProfileJson)
    }, guard);

    const rebased = await store.listOutbox(accountScope, initial.profileId);
    expect(rebased).toHaveLength(1);
    expect(rebased[0]).toMatchObject({ state: "pending", baseVersion: 5, baseHash: claimed!.profileHash });
  });

  it("does not enqueue device-only changes and never retargets another account's queue", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const firstScope = await createAccountScope("https://one.example.test", "user:one");
    await store.activateAccount({
      accountScope: firstScope,
      baseUrl: "https://one.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 1,
      baseSnapshot: toSharedProfileV2(initial)
    });

    await store.commitProfile({
      ...initial,
      wallpaper: { ...initial.wallpaper, rotationHistory: ["mist"] }
    }, 1_000);
    expect(await store.listOutbox(firstScope, initial.profileId)).toHaveLength(0);
    expect(await store.getDeviceState()).toMatchObject({ hasUserEdits: false });

    const sharedChange = { ...initial, updatedAt: "2026-07-12T02:00:00.000Z", theme: { ...initial.theme, showBrand: true } };
    await store.commitProfile(sharedChange, 2_000);
    expect(await store.listOutbox(firstScope, initial.profileId)).toHaveLength(1);

    const secondScope = await createAccountScope("https://two.example.test", "user:two");
    await store.activateAccount({
      accountScope: secondScope,
      baseUrl: "https://two.example.test",
      userId: "user:two",
      profileId: initial.profileId,
      baseVersion: 20,
      baseSnapshot: toSharedProfileV2(sharedChange)
    });
    const secondChange = { ...sharedChange, updatedAt: "2026-07-12T03:00:00.000Z", theme: { ...sharedChange.theme, density: "compact" as const } };
    await store.commitProfile(secondChange, 3_000);

    expect(await store.listOutbox(firstScope, initial.profileId)).toHaveLength(1);
    expect(await store.listOutbox(secondScope, initial.profileId)).toHaveLength(1);
    expect((await store.listOutbox(secondScope, initial.profileId))[0].baseVersion).toBe(20);
  });

  it("isolates metadata and mutations by accountScope plus profileId", async () => {
    const first = createDefaultProfile();
    await store.initialize(first);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: first.profileId,
      baseVersion: 1,
      baseSnapshot: toSharedProfileV2(first)
    });
    await store.commitProfile({
      ...first,
      updatedAt: "2026-07-12T01:00:00.000Z",
      theme: { ...first.theme, showBrand: true }
    }, 1_000);

    const secondBase = {
      ...first,
      profileId: "profile:two",
      updatedAt: "2026-07-12T01:30:00.000Z"
    };
    const second = {
      ...secondBase,
      updatedAt: "2026-07-12T02:00:00.000Z",
      theme: { ...first.theme, columns: 7 as const }
    };
    await store.activateAccount({
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: second.profileId,
      baseVersion: 20,
      baseSnapshot: toSharedProfileV2(secondBase)
    });
    await store.commitProfile(second, 2_000);

    expect(await store.getSyncMetadata(accountScope, first.profileId)).toMatchObject({
      profileId: first.profileId,
      baseVersion: 1
    });
    expect(await store.getSyncMetadata(accountScope, second.profileId)).toMatchObject({
      profileId: second.profileId,
      baseVersion: 20
    });
    expect(await store.listOutbox(accountScope, first.profileId)).toHaveLength(1);
    expect(await store.listOutbox(accountScope, second.profileId)).toHaveLength(1);
    expect(await store.listOutbox(accountScope, first.profileId)).toEqual([
      expect.objectContaining({ accountScope, profileId: first.profileId })
    ]);
  });

  it("upgrades v1 account-only records into composite account/profile keys", async () => {
    await store.delete();
    const databaseName = `FullProSyncLegacyTest-${crypto.randomUUID()}`;
    const initial = createDefaultProfile();
    const shared = toSharedProfileV2(initial);
    const accountScope = "account:legacy";
    const legacy = new Dexie(databaseName);
    legacy.version(1).stores({
      localProfiles: "&key",
      deviceStates: "&key",
      syncMetadata: "&accountScope,active",
      outbox: "&mutationId,accountScope,[accountScope+state],dueAt,nextAttemptAt",
      conflicts: "&conflictId,accountScope,mutationId"
    });
    await legacy.open();
    await legacy.table("syncMetadata").put({
      accountScope,
      active: 1,
      baseUrl: "https://sync.example.test",
      userId: "user:legacy",
      profileId: initial.profileId,
      baseVersion: 2,
      baseHash: "legacy-base",
      baseSnapshot: shared,
      status: "dirty"
    });
    await legacy.table("outbox").put({
      mutationId: "mut_legacy",
      accountScope,
      state: "pending",
      baseVersion: 2,
      baseHash: "legacy-base",
      schemaVersion: 2,
      profileHash: "legacy-profile",
      canonicalProfileJson: JSON.stringify(shared),
      localRevision: 1,
      firstDirtyAt: 1,
      dueAt: 2,
      maxDueAt: 3,
      nextAttemptAt: 2,
      attempts: 0
    });
    legacy.close();

    const migrated = new SyncStore(databaseName);
    try {
      expect(await migrated.getSyncMetadata(accountScope, initial.profileId)).toMatchObject({ baseVersion: 2 });
      expect(await migrated.listOutbox(accountScope, initial.profileId)).toEqual([
        expect.objectContaining({ mutationId: "mut_legacy", profileId: initial.profileId })
      ]);
      expect(await migrated.deactivateAccount(
        accountScope,
        initial.profileId,
        `legacy:${accountScope}:1`
      )).toBe(true);
      expect(await migrated.getSyncMetadata(accountScope, initial.profileId)).toMatchObject({ active: 0 });
    } finally {
      await migrated.delete();
      store = new SyncStore(`FullProSyncTest-${crypto.randomUUID()}`);
    }
  });

  it("freezes a conflict snapshot, keeps later edits as a branch, and rebases the resolved mutation", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 7,
      baseSnapshot: toSharedProfileV2(initial)
    });
    const local = { ...initial, updatedAt: "2026-07-12T01:00:00.000Z", theme: { ...initial.theme, columns: 7 as const } };
    await store.commitProfile(local, 1_000);
    const guard = { sessionGeneration: `legacy:${accountScope}` };
    const claimed = await store.claimDue(accountScope, initial.profileId, 1_000 + quietOutboxDelayMs, guard);
    const remote = toSharedProfileV2({
      ...initial,
      updatedAt: "2026-07-12T02:00:00.000Z",
      theme: { ...initial.theme, columns: 4 }
    });

    const conflict = await store.recordConflict(accountScope, initial.profileId, claimed!.mutationId, {
      conflictId: "conflict:server",
      version: 8,
      profileHash: "remote-hash",
      profile: remote
    }, 2_000, guard);
    const branch = { ...local, updatedAt: "2026-07-12T03:00:00.000Z", theme: { ...local.theme, showBrand: true } };
    await store.commitProfile(branch, 3_000);

    expect((await store.getConflict(accountScope, initial.profileId, conflict.conflictId))?.localAtConflict.theme.columns).toBe(7);
    expect((await store.listOutbox(accountScope, initial.profileId)).map((item) => item.state).sort()).toEqual(["conflict", "pending"]);

    const resolved = structuredClone(remote);
    resolved.theme.columns = 7;
    const resolution = await store.resolveConflict(accountScope, initial.profileId, conflict.conflictId, resolved, 4_000, guard);
    const queued = await store.listOutbox(accountScope, initial.profileId);

    expect(resolution.profile.theme).toMatchObject({ columns: 7, showBrand: true });
    expect(queued).toHaveLength(1);
    expect(queued[0]).toMatchObject({
      state: "pending",
      baseVersion: 8,
      baseHash: "remote-hash",
      resolvesConflictId: "conflict:server"
    });
    expect(await store.getConflict(accountScope, initial.profileId, conflict.conflictId)).toBeUndefined();
  });

  it("persists exponential retry timing and respects Retry-After", async () => {
    expect(computeRetryDelayMs(1, undefined, 0.5)).toBe(1_000);
    expect(computeRetryDelayMs(4, 90_000, 0)).toBe(90_000);
    expect(computeRetryDelayMs(99, undefined, 1)).toBe(30 * 60 * 1000);

    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 1,
      baseSnapshot: toSharedProfileV2(initial)
    });
    await store.commitProfile({ ...initial, updatedAt: "2026-07-12T01:00:00.000Z", theme: { ...initial.theme, showBrand: true } }, 1_000);
    const guard = { sessionGeneration: `legacy:${accountScope}` };
    const claimed = await store.claimDue(accountScope, initial.profileId, 1_000 + quietOutboxDelayMs, guard);
    await store.scheduleRetry(accountScope, initial.profileId, claimed!.mutationId, { retryAfterMs: 90_000, errorCode: "RATE_LIMITED" }, 2_000, guard);

    expect(await store.getNextWakeAt(accountScope, initial.profileId)).toBe(92_000);
    expect(await store.claimDue(accountScope, initial.profileId, 91_999, guard)).toBeUndefined();
    expect(await store.claimDue(accountScope, initial.profileId, 92_000, guard)).toMatchObject({ mutationId: claimed!.mutationId, attempts: 2 });
    expect((await store.getSyncMetadata(accountScope, initial.profileId))?.errorCode).toBe("RATE_LIMITED");
  });

  it("applies an explicitly chosen remote first-connection profile without enqueuing it back", async () => {
    const initial = createDefaultProfile();
    initial.deviceId = "device:keep";
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 2,
      baseSnapshot: toSharedProfileV2(initial)
    });
    const remote = toSharedProfileV2({
      ...initial,
      updatedAt: "2026-07-12T04:00:00.000Z",
      theme: { ...initial.theme, showBrand: true }
    });

    const applied = await store.acceptRemote(
      accountScope,
      initial.profileId,
      { version: 3, profileHash: "remote-3", profile: remote },
      { sessionGeneration: "session:one", expectedLocalRevision: 0 }
    );

    expect(applied.deviceId).toBe("device:keep");
    expect(applied.theme.showBrand).toBe(true);
    expect(await store.listOutbox(accountScope, initial.profileId)).toEqual([]);
    expect(await store.getSyncMetadata(accountScope, initial.profileId)).toMatchObject({ baseVersion: 3, baseHash: "remote-3", status: "idle" });
    expect(await store.getDeviceState()).toMatchObject({ hasUserEdits: false });
  });

  it("refuses remote adoption for inactive, replaced-session, or stale local state", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    const remote = toSharedProfileV2({
      ...initial,
      updatedAt: "2026-07-12T04:00:00.000Z",
      theme: { ...initial.theme, columns: 8 }
    });
    const acknowledgement = { version: 3, profileHash: "remote-3", profile: remote };
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 2,
      baseSnapshot: toSharedProfileV2(initial)
    });
    await store.deactivateAccount(accountScope, initial.profileId);

    await expect(store.acceptRemote(
      accountScope,
      initial.profileId,
      acknowledgement,
      { sessionGeneration: "session:one", expectedLocalRevision: 0 }
    )).rejects.toThrow("SYNC_ACCOUNT_NOT_ACTIVE");

    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:two",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 2,
      baseSnapshot: toSharedProfileV2(initial)
    });
    await expect(store.acceptRemote(
      accountScope,
      initial.profileId,
      acknowledgement,
      { sessionGeneration: "session:one", expectedLocalRevision: 0 }
    )).rejects.toThrow("SYNC_SESSION_GENERATION_CHANGED");

    await store.commitProfile({
      ...initial,
      updatedAt: "2026-07-12T03:00:00.000Z",
      theme: { ...initial.theme, columns: 7 }
    }, 3_000);
    await expect(store.acceptRemote(
      accountScope,
      initial.profileId,
      acknowledgement,
      { sessionGeneration: "session:two", expectedLocalRevision: 0 }
    )).rejects.toMatchObject({ code: "SYNC_LOCAL_REVISION_CONFLICT" });
    expect((await store.getLocalProfile())?.profile.theme.columns).toBe(7);
  });

  it("does not let an older logout deactivate replacement-session metadata", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:new",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 2,
      baseSnapshot: toSharedProfileV2(initial)
    });

    expect(await store.deactivateAccount(accountScope, initial.profileId, "session:old")).toBe(false);
    expect(await store.getSyncMetadata(accountScope, initial.profileId)).toMatchObject({
      active: 1,
      sessionGeneration: "session:new"
    });
  });

  it("rejects delayed ACK, retry, and conflict commits from a replaced session", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    const guardA = { sessionGeneration: "session:a" };
    await store.activateAccount({
      accountScope,
      sessionGeneration: guardA.sessionGeneration,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 3,
      baseSnapshot: toSharedProfileV2(initial)
    });
    await store.commitProfile({
      ...initial,
      updatedAt: "2026-07-12T01:00:00.000Z",
      theme: { ...initial.theme, columns: 7 }
    }, 1_000);
    const claimed = await store.claimDue(accountScope, initial.profileId, 1_000 + quietOutboxDelayMs, guardA);
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:b",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 20,
      baseHash: "server-20",
      baseSnapshot: toSharedProfileV2(initial)
    });
    const acknowledgement = {
      version: 4,
      profileHash: "server-4",
      profile: JSON.parse(claimed!.canonicalProfileJson)
    };

    await expect(store.acknowledge(
      accountScope,
      initial.profileId,
      claimed!.mutationId,
      acknowledgement,
      guardA
    )).rejects.toThrow("SYNC_SESSION_GENERATION_CHANGED");
    await expect(store.scheduleRetry(
      accountScope,
      initial.profileId,
      claimed!.mutationId,
      { errorCode: "NETWORK_ERROR" },
      2_000,
      guardA
    )).rejects.toThrow("SYNC_SESSION_GENERATION_CHANGED");
    await expect(store.recordConflict(
      accountScope,
      initial.profileId,
      claimed!.mutationId,
      acknowledgement,
      2_000,
      guardA
    )).rejects.toThrow("SYNC_SESSION_GENERATION_CHANGED");
    expect(await store.getSyncMetadata(accountScope, initial.profileId)).toMatchObject({
      active: 1,
      sessionGeneration: "session:b",
      baseVersion: 20,
      baseHash: "server-20"
    });
    expect(await store.listOutbox(accountScope, initial.profileId)).toEqual([
      expect.objectContaining({ mutationId: claimed!.mutationId, state: "in-flight", sessionGeneration: "session:a" })
    ]);
  });

  it("can explicitly enqueue the current profile against the selected remote base", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 9,
      baseSnapshot: toSharedProfileV2(initial)
    });

    const mutation = await store.forceEnqueueCurrent(
      accountScope,
      initial.profileId,
      5_000,
      { sessionGeneration: `legacy:${accountScope}` }
    );

    expect(mutation).toMatchObject({ accountScope, baseVersion: 9, state: "pending", dueAt: 5_000 });
  });
});
