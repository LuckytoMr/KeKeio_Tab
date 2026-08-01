import "fake-indexeddb/auto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createDefaultProfile } from "../profile/defaults";
import { toSharedProfileV2 } from "../profile/sharedProfile";
import { SyncStore, createAccountScope, quietOutboxDelayMs } from "../storage/syncStore";
import { CredentialVault, type KeyValueStorage, type WorkerCredentials } from "./credentialVault";
import { ApiError } from "./syncApi";
import { classifyFirstConnection, SyncWorkerRuntime, type WorkerApi } from "./workerRuntime";

class MemoryStorage implements KeyValueStorage {
  values: Record<string, unknown> = {};
  async get(key: string) { return { [key]: this.values[key] }; }
  async set(values: Record<string, unknown>) { Object.assign(this.values, values); }
  async remove(key: string) { delete this.values[key]; }
}

function requireFullCredentials(value: WorkerCredentials | undefined) {
  if (!value || value.scope !== "full") throw new Error("expected full credentials");
  return value;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

async function seedDrainableSession(store: SyncStore, vault: CredentialVault) {
  const initial = createDefaultProfile();
  const accountScope = await createAccountScope("https://sync.example.test", "user:one");
  const now = 1_000 + quietOutboxDelayMs;
  await store.initialize(initial);
  await store.activateAccount({
    accountScope,
    sessionGeneration: "session:a",
    baseUrl: "https://sync.example.test",
    userId: "user:one",
    profileId: initial.profileId,
    baseVersion: 3,
    baseSnapshot: toSharedProfileV2(initial)
  });
  await store.commitProfile({
    ...initial,
    updatedAt: "2026-07-12T01:00:00.000Z",
    theme: { ...initial.theme, showBrand: true }
  }, 1_000);
  await vault.save({
    version: 2,
    scope: "full",
    sessionGeneration: "session:a",
    firstConnectionPending: false,
    accountScope,
    baseUrl: "https://sync.example.test",
    userId: "user:one",
    email: "one@example.test",
    accessToken: "access-a",
    accessExpiresAt: 999_999,
    refreshToken: "refresh-a",
    refreshExpiresAt: 9_999_999,
    updatedAt: 1
  });
  return { initial, now };
}

async function replaceActiveSession(store: SyncStore, vault: CredentialVault, profileId: string) {
  const accountScope = await createAccountScope("https://sync.example.test", "user:two");
  await vault.save({
    version: 2,
    scope: "full",
    sessionGeneration: "session:b",
    firstConnectionPending: false,
    accountScope,
    baseUrl: "https://sync.example.test",
    userId: "user:two",
    email: "two@example.test",
    accessToken: "access-b",
    accessExpiresAt: 999_999,
    refreshToken: "refresh-b",
    refreshExpiresAt: 9_999_999,
    updatedAt: 2
  });
  const local = await store.getLocalProfile();
  if (!local) throw new Error("expected local profile");
  await store.activateAccount({
    accountScope,
    sessionGeneration: "session:b",
    baseUrl: "https://sync.example.test",
    userId: "user:two",
    profileId,
    baseVersion: 9,
    baseSnapshot: toSharedProfileV2(local.profile)
  });
}

describe("SyncWorkerRuntime", () => {
  let store: SyncStore;
  let vault: CredentialVault;

  beforeEach(() => {
    store = new SyncStore(`FullProWorkerTest-${crypto.randomUUID()}`);
    vault = new CredentialVault(new MemoryStorage());
  });

  afterEach(async () => {
    await store.delete();
  });

  it("single-flights rotating refresh tokens for concurrent authenticated work", async () => {
    const credentials: WorkerCredentials = {
      version: 2,
      scope: "full",
      sessionGeneration: "session:one",
      firstConnectionPending: false,
      accountScope: "account:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "expired",
      accessExpiresAt: 1,
      refreshToken: "refresh-one",
      refreshExpiresAt: 999_999,
      updatedAt: 1
    };
    await vault.save(credentials);
    const refresh = vi.fn(async () => ({
      user: { id: "user:one", email: "one@example.test", role: "user" as const },
      scope: "full" as const,
      accessToken: "access-new",
      accessExpiresAt: "1970-01-01T00:10:00.000Z",
      refreshToken: "refresh-new",
      refreshExpiresAt: "1970-01-02T00:00:00.000Z"
    }));
    const api = { refresh } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => 100_000);
    const operation = vi.fn(async (token: string) => token);

    const [first, second] = await Promise.all([
      runtime.authenticated("account:one", "session:one", operation),
      runtime.authenticated("account:one", "session:one", operation)
    ]);

    expect([first, second]).toEqual(["access-new", "access-new"]);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(requireFullCredentials(await vault.loadPrivate()).refreshToken).toBe("refresh-new");
  });

  it("does not let a delayed refresh restore credentials after logout", async () => {
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:one",
      firstConnectionPending: false,
      accountScope: "account:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "expired",
      accessExpiresAt: 1,
      refreshToken: "refresh-one",
      refreshExpiresAt: 999_999,
      updatedAt: 1
    });
    const response = deferred<Awaited<ReturnType<WorkerApi["refresh"]>>>();
    const started = deferred<void>();
    const refresh = vi.fn(async () => {
      started.resolve();
      return response.promise;
    });
    const logout = vi.fn(async () => {
      throw new Error("offline");
    });
    const api = { refresh, logout } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => 100_000);

    const authenticated = runtime.authenticated("account:one", "session:one", async (token) => token);
    await started.promise;
    await runtime.logout();
    response.resolve({
      user: { id: "user:one", email: "one@example.test", role: "user" },
      scope: "full",
      accessToken: "access-late",
      accessExpiresAt: "1970-01-01T00:10:00.000Z",
      refreshToken: "refresh-late",
      refreshExpiresAt: "1970-01-02T00:00:00.000Z"
    });

    await expect(authenticated).rejects.toMatchObject({ code: "AUTH_SESSION_CHANGED" });
    expect(await vault.loadPrivate()).toBeUndefined();
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it("does not let an old drain commit into a same-account replacement login", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:a",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 3,
      baseHash: "server-3",
      baseSnapshot: toSharedProfileV2(initial)
    });
    const now = Date.parse("2026-07-12T02:30:00.000Z");
    await store.commitProfile({
      ...initial,
      updatedAt: "2026-07-12T02:00:00.000Z",
      theme: { ...initial.theme, columns: 7 }
    }, now - quietOutboxDelayMs - 1);
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:a",
      firstConnectionPending: false,
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-a",
      accessExpiresAt: Date.parse("2026-07-12T03:00:00.000Z"),
      refreshToken: "refresh-a",
      refreshExpiresAt: Date.parse("2026-08-12T03:00:00.000Z"),
      updatedAt: now
    });
    const putResponse = deferred<Awaited<ReturnType<WorkerApi["putProfile"]>>>();
    const putStarted = deferred<void>();
    const putTokens: string[] = [];
    const remote = toSharedProfileV2(initial);
    const api = {
      putProfile: vi.fn(async (token: string) => {
        putTokens.push(token);
        putStarted.resolve();
        return putResponse.promise;
      }),
      logout: vi.fn(async () => undefined),
      login: vi.fn(async () => ({
        user: { id: "user:one", email: "one@example.test", role: "user" as const },
        scope: "full" as const,
        accessToken: "access-b",
        accessExpiresAt: "2026-07-12T03:00:00.000Z",
        refreshToken: "refresh-b",
        refreshExpiresAt: "2026-08-12T03:00:00.000Z"
      })),
      getProfile: vi.fn(async () => ({
        profile: remote,
        version: 20,
        profileHash: "server-20",
        schemaVersion: 2 as const
      }))
    } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => now);

    const draining = runtime.drain();
    await putStarted.promise;
    await runtime.logout();
    const replacement = await runtime.login({
      baseUrl: "https://sync.example.test",
      email: "one@example.test",
      password: "secret-password"
    });
    putResponse.resolve({
      profile: toSharedProfileV2({
        ...initial,
        updatedAt: "2026-07-12T02:00:00.000Z",
        theme: { ...initial.theme, columns: 7 }
      }),
      version: 4,
      profileHash: "server-4",
      schemaVersion: 2
    });

    await expect(draining).rejects.toMatchObject({ code: "AUTH_SESSION_CHANGED" });
    expect(putTokens).toEqual(["access-a"]);
    expect(replacement.session?.firstConnectionPending).toBe(true);
    expect(await store.getSyncMetadata(accountScope, initial.profileId)).toMatchObject({
      active: 1,
      sessionGeneration: replacement.session?.sessionGeneration,
      baseVersion: 20,
      baseHash: "server-20"
    });
  });

  it("does not return an old account conflict after the active session is replaced", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:a",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 3,
      baseSnapshot: toSharedProfileV2(initial)
    });
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:a",
      firstConnectionPending: false,
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-a",
      accessExpiresAt: 999_999,
      refreshToken: "refresh-a",
      refreshExpiresAt: 9_999_999,
      updatedAt: 1
    });
    const lookup = deferred<unknown>();
    const started = deferred<void>();
    vi.spyOn(store, "getConflict").mockImplementation(() => {
      started.resolve();
      return lookup.promise as ReturnType<typeof store.getConflict>;
    });
    const runtime = new SyncWorkerRuntime(store, vault, undefined, () => 100_000);

    const request = runtime.getConflict("conflict:a");
    await started.promise;
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:b",
      firstConnectionPending: false,
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-b",
      accessExpiresAt: 999_999,
      refreshToken: "refresh-b",
      refreshExpiresAt: 9_999_999,
      updatedAt: 2
    });
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:b",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 20,
      baseSnapshot: toSharedProfileV2(initial)
    });
    lookup.resolve({
      conflictId: "conflict:a",
      accountScope,
      profileId: initial.profileId,
      sessionGeneration: "session:a"
    });

    await expect(request).rejects.toMatchObject({ code: "AUTH_SESSION_CHANGED" });
  });

  it("returns stale instead of exposing a terminal mutation from a replaced session", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:a",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 3,
      baseSnapshot: toSharedProfileV2(initial)
    });
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:a",
      firstConnectionPending: false,
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-a",
      accessExpiresAt: 999_999,
      refreshToken: "refresh-a",
      refreshExpiresAt: 9_999_999,
      updatedAt: 1
    });
    const lookup = deferred<unknown>();
    const started = deferred<void>();
    vi.spyOn(store, "getTerminalOutboxMutation").mockImplementation(async () => {
      started.resolve();
      return lookup.promise as never;
    });
    const runtime = new SyncWorkerRuntime(store, vault, undefined, () => 100_000);

    const request = runtime.drain();
    await started.promise;
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:b",
      firstConnectionPending: false,
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-b",
      accessExpiresAt: 999_999,
      refreshToken: "refresh-b",
      refreshExpiresAt: 9_999_999,
      updatedAt: 2
    });
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:b",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 20,
      baseSnapshot: toSharedProfileV2(initial)
    });
    lookup.resolve({
      mutationId: "mutation:a",
      accountScope,
      profileId: initial.profileId,
      sessionGeneration: "session:a",
      terminalCode: "SERVER_EMPTY_CONFLICT",
      serverConflictId: "server-conflict:a"
    });

    await expect(request).resolves.toEqual({ status: "stale" });
  });

  it("returns stale when a newly recorded conflict finishes after the active session is replaced", async () => {
    const { initial, now } = await seedDrainableSession(store, vault);
    const remote = toSharedProfileV2({
      ...initial,
      updatedAt: "2026-07-12T02:00:00.000Z",
      theme: { ...initial.theme, columns: 4 }
    });
    const recorded = deferred<void>();
    const releaseRecord = deferred<void>();
    const recordConflict = store.recordConflict.bind(store);
    vi.spyOn(store, "recordConflict").mockImplementation(async (...args) => {
      const conflict = await recordConflict(...args);
      recorded.resolve();
      await releaseRecord.promise;
      return conflict;
    });
    const api = {
      putProfile: vi.fn(async () => {
        throw new ApiError("conflict", 409, "PROFILE_CONFLICT", {
          conflictId: "conflict:server",
          currentVersion: 4,
          currentHash: "server-current-hash",
          currentProfile: remote
        });
      })
    } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => now);

    const request = runtime.drain();
    await recorded.promise;
    await replaceActiveSession(store, vault, initial.profileId);
    releaseRecord.resolve();

    await expect(request).resolves.toEqual({ status: "stale" });
  });

  it("returns stale when a newly frozen terminal conflict finishes after the active session is replaced", async () => {
    const { initial, now } = await seedDrainableSession(store, vault);
    const frozen = deferred<void>();
    const releaseFreeze = deferred<void>();
    const freezeServerEmptyConflict = store.freezeServerEmptyConflict.bind(store);
    vi.spyOn(store, "freezeServerEmptyConflict").mockImplementation(async (...args) => {
      const mutation = await freezeServerEmptyConflict(...args);
      frozen.resolve();
      await releaseFreeze.promise;
      return mutation;
    });
    const api = {
      putProfile: vi.fn(async () => {
        throw new ApiError("conflict", 409, "PROFILE_CONFLICT", {
          conflictId: "conflict:empty",
          currentVersion: 0,
          currentHash: "server-empty",
          currentProfile: null
        });
      })
    } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => now);

    const request = runtime.drain();
    await frozen.promise;
    await replaceActiveSession(store, vault, initial.profileId);
    releaseFreeze.resolve();

    await expect(request).resolves.toEqual({ status: "stale" });
  });

  it("persists a refresh request id before I/O, reuses it after a crash, and clears it after ACK", async () => {
    const credentials: WorkerCredentials = {
      version: 2,
      scope: "full",
      sessionGeneration: "session:one",
      firstConnectionPending: false,
      accountScope: "account:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "expired",
      accessExpiresAt: 1,
      refreshToken: "refresh-one",
      refreshExpiresAt: 999_999,
      updatedAt: 1
    };
    await vault.save(credentials);
    const requestIds: string[] = [];
    let attempts = 0;
    const refresh = vi.fn(async (_token: string, requestId: string) => {
      requestIds.push(requestId);
      attempts += 1;
      if (attempts === 1) throw new Error("worker terminated after request dispatch");
      return {
        user: { id: "user:one", email: "one@example.test", role: "user" as const },
        scope: "full" as const,
        accessToken: "access-new",
        accessExpiresAt: "1970-01-01T00:10:00.000Z",
        refreshToken: "refresh-new",
        refreshExpiresAt: "1970-01-02T00:00:00.000Z"
      };
    });
    const api = { refresh } as unknown as WorkerApi;
    const firstRuntime = new SyncWorkerRuntime(store, vault, () => api, () => 100_000);

    await expect(firstRuntime.authenticated("account:one", "session:one", async () => "never"))
      .rejects.toThrow("worker terminated");
    const pending = requireFullCredentials(await vault.loadPrivate());
    expect(pending.pendingRefresh).toMatchObject({
      requestId: requestIds[0],
      refreshToken: "refresh-one"
    });

    const restartedRuntime = new SyncWorkerRuntime(store, vault, () => api, () => 100_000);
    await expect(restartedRuntime.authenticated("account:one", "session:one", async (token) => token)).resolves.toBe("access-new");
    expect(requestIds).toHaveLength(2);
    expect(requestIds[1]).toBe(requestIds[0]);
    expect(requireFullCredentials(await vault.loadPrivate()).pendingRefresh).toBeUndefined();
  });

  it("drains one immutable mutation through CAS and commits the ACK transaction", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 3,
      baseSnapshot: toSharedProfileV2(initial)
    });
    await store.commitProfile({ ...initial, updatedAt: "2026-07-12T01:00:00.000Z", theme: { ...initial.theme, showBrand: true } }, 1_000);
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:one",
      firstConnectionPending: false,
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access",
      accessExpiresAt: 999_999,
      refreshToken: "refresh",
      refreshExpiresAt: 9_999_999,
      updatedAt: 1
    });
    const putProfile = vi.fn(async (_token: string, request: { profile: ReturnType<typeof toSharedProfileV2>; mutationId: string }) => ({
      profile: request.profile,
      version: 4,
      profileHash: "server-hash-4",
      schemaVersion: 2 as const,
      updatedAt: "2026-07-12T01:00:01.000Z",
      mutationId: request.mutationId,
      idempotentReplay: false
    }));
    const api = { putProfile } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => 1_000 + quietOutboxDelayMs);

    const outcome = await runtime.drain();

    expect(outcome).toMatchObject({ status: "synced", version: 4 });
    expect(putProfile).toHaveBeenCalledWith("access", expect.objectContaining({
      baseVersion: 3,
      schemaVersion: 2,
      deviceId: initial.deviceId
    }));
    const request = putProfile.mock.calls[0][1];
    expect(request.mutationId).toMatch(/^mut_/);
    expect(await store.listOutbox(accountScope, initial.profileId)).toEqual([]);
    expect(await store.getSyncMetadata(accountScope, initial.profileId)).toMatchObject({ baseVersion: 4, baseHash: "server-hash-4" });
  });

  it("records the server-provided currentHash for a PROFILE_CONFLICT", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 3,
      baseSnapshot: toSharedProfileV2(initial)
    });
    await store.commitProfile({
      ...initial,
      updatedAt: "2026-07-12T01:00:00.000Z",
      theme: { ...initial.theme, showBrand: true }
    }, 1_000);
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:one",
      firstConnectionPending: false,
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access",
      accessExpiresAt: 999_999,
      refreshToken: "refresh",
      refreshExpiresAt: 9_999_999,
      updatedAt: 1
    });
    const remote = toSharedProfileV2({
      ...initial,
      updatedAt: "2026-07-12T02:00:00.000Z",
      theme: { ...initial.theme, columns: 4 }
    });
    const api = {
      putProfile: vi.fn(async () => {
        throw new ApiError("conflict", 409, "PROFILE_CONFLICT", {
          conflictId: "conflict:server",
          currentVersion: 4,
          currentHash: "server-current-hash",
          currentProfile: remote
        });
      })
    } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => 1_000 + quietOutboxDelayMs);

    const outcome = await runtime.drain();

    expect(outcome).toMatchObject({ status: "conflict" });
    if (outcome.status !== "conflict") throw new Error("expected conflict outcome");
    expect(await store.getConflict(accountScope, initial.profileId, outcome.conflictId)).toMatchObject({
      serverConflictId: "conflict:server",
      remoteHash: "server-current-hash"
    });
    const restartedRuntime = new SyncWorkerRuntime(store, vault, () => api, () => 1_000 + quietOutboxDelayMs);
    await expect(restartedRuntime.drain()).resolves.toEqual({
      status: "conflict",
      conflictId: outcome.conflictId
    });
  });

  it("freezes a server-empty conflict without retrying or discarding the local mutation", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const accountScope = await createAccountScope("https://sync.example.test", "user:one");
    await store.activateAccount({
      accountScope,
      sessionGeneration: "session:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      profileId: initial.profileId,
      baseVersion: 3,
      baseSnapshot: toSharedProfileV2(initial)
    });
    await store.commitProfile({
      ...initial,
      updatedAt: "2026-07-12T01:00:00.000Z",
      theme: { ...initial.theme, showBrand: true }
    }, 1_000);
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:one",
      firstConnectionPending: false,
      accountScope,
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access",
      accessExpiresAt: 999_999,
      refreshToken: "refresh",
      refreshExpiresAt: 9_999_999,
      updatedAt: 1
    });
    const putProfile = vi.fn(async () => {
      throw new ApiError("conflict", 409, "PROFILE_CONFLICT", {
        conflictId: "conflict:empty",
        currentVersion: 0,
        currentHash: "server-empty",
        currentProfile: null
      });
    });
    const api = { putProfile } as unknown as WorkerApi;
    const now = 1_000 + quietOutboxDelayMs;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => now);

    const outcome = await runtime.drain();

    expect(outcome).toMatchObject({ status: "server-empty-conflict", conflictId: "conflict:empty" });
    const frozen = await store.listOutbox(accountScope, initial.profileId);
    expect(frozen).toEqual([
      expect.objectContaining({
        state: "conflict",
        terminalCode: "SERVER_EMPTY_CONFLICT",
        serverConflictId: "conflict:empty"
      })
    ]);
    expect(frozen[0].canonicalProfileJson).toContain('"showBrand":true');
    expect(await store.getNextWakeAt(accountScope, initial.profileId)).toBeUndefined();
    expect(await store.getSyncMetadata(accountScope, initial.profileId)).toMatchObject({
      status: "conflict",
      errorCode: "SERVER_EMPTY_CONFLICT"
    });

    const restartedRuntime = new SyncWorkerRuntime(store, vault, () => api, () => now + 60_000);
    await expect(restartedRuntime.drain()).resolves.toMatchObject({
      status: "server-empty-conflict",
      conflictId: "conflict:empty"
    });
    expect(putProfile).toHaveBeenCalledTimes(1);
  });

  it("bootstraps anonymously from the explicit canonical backend URL", async () => {
    const bootstrap = vi.fn(async () => ({ latestRelease: { version: "0.2.0" } }));
    const apiFactory = vi.fn(() => ({ bootstrap }) as unknown as WorkerApi);
    const runtime = new SyncWorkerRuntime(store, vault, apiFactory);

    await expect(runtime.getCatalog("bootstrap", "", " HTTPS://Sync.Example.Test/root///?ignored=yes#fragment "))
      .resolves.toEqual({ latestRelease: { version: "0.2.0" } });

    expect(apiFactory).toHaveBeenCalledWith("https://sync.example.test/root");
    expect(bootstrap).toHaveBeenCalledTimes(1);
    expect(await vault.loadPrivate()).toBeUndefined();
  });

  it("routes UHDpaper page and image reads through the signed-in backend client", async () => {
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:uhdpaper",
      firstConnectionPending: false,
      accountScope: "account:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-a",
      accessExpiresAt: 999_999,
      refreshToken: "refresh-a",
      refreshExpiresAt: 9_999_999,
      updatedAt: 1
    });
    const fetchUhdpaperPage = vi.fn(async () => ({ html: "<html></html>" }));
    const fetchUhdpaperImage = vi.fn(async () => ({ mimeType: "image/jpeg", dataUrl: "data:image/jpeg;base64,AA==" }));
    const api = { fetchUhdpaperPage, fetchUhdpaperImage } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => 100);
    const pageUrl = "https://www.uhdpaper.com/?page=2";
    const imageUrl = "https://img.uhdpaper.com/wallpaper/space.jpg";

    await expect(runtime.getCatalog("uhdpaper-page", pageUrl)).resolves.toEqual({ html: "<html></html>" });
    await expect(runtime.getCatalog("uhdpaper-image", imageUrl)).resolves.toEqual({
      mimeType: "image/jpeg",
      dataUrl: "data:image/jpeg;base64,AA=="
    });

    expect(fetchUhdpaperPage).toHaveBeenCalledWith("access-a", pageUrl);
    expect(fetchUhdpaperImage).toHaveBeenCalledWith("access-a", imageUrl);
  });

  it("classifies the four first-connection states without treating starter defaults as user data", () => {
    expect(classifyFirstConnection(false, false)).toBe("both-empty");
    expect(classifyFirstConnection(true, false)).toBe("local-only");
    expect(classifyFirstConnection(false, true)).toBe("remote-only");
    expect(classifyFirstConnection(true, true)).toBe("both-have-data");
  });

  it("normalizes email addresses before dispatching recovery requests", async () => {
    const resendVerification = vi.fn(async () => ({ accepted: true }));
    const forgotPassword = vi.fn(async () => ({ accepted: true }));
    const api = { resendVerification, forgotPassword } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api);

    await runtime.resendVerification({ baseUrl: "https://sync.example.test", email: " pending@example.test " });
    await runtime.forgotPassword({ baseUrl: "https://sync.example.test", email: " one@example.test " });

    expect(resendVerification).toHaveBeenCalledWith("pending@example.test");
    expect(forgotPassword).toHaveBeenCalledWith("one@example.test");
  });

  it("rejects a delayed catalog response after the writable session generation changes", async () => {
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:a",
      firstConnectionPending: false,
      accountScope: "account:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-a",
      accessExpiresAt: Date.parse("2026-07-12T03:00:00.000Z"),
      refreshToken: "refresh-a",
      refreshExpiresAt: Date.parse("2026-08-12T03:00:00.000Z"),
      updatedAt: Date.parse("2026-07-12T02:00:00.000Z")
    });
    const response = deferred<unknown>();
    const started = deferred<void>();
    const api = {
      listStyles: vi.fn(async () => {
        started.resolve();
        return response.promise;
      })
    } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => Date.parse("2026-07-12T02:30:00.000Z"));

    const request = runtime.getCatalog("styles");
    await started.promise;
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:b",
      firstConnectionPending: false,
      accountScope: "account:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-b",
      accessExpiresAt: Date.parse("2026-07-12T03:00:00.000Z"),
      refreshToken: "refresh-b",
      refreshExpiresAt: Date.parse("2026-08-12T03:00:00.000Z"),
      updatedAt: Date.parse("2026-07-12T02:10:00.000Z")
    });
    response.resolve({ items: [{ id: "stale-style" }] });

    await expect(request).rejects.toMatchObject({ code: "AUTH_SESSION_CHANGED" });
  });

  it("requires an explicit first-connection choice before replacing local data", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    await store.commitProfile({
      ...initial,
      updatedAt: "2026-07-12T01:00:00.000Z",
      theme: { ...initial.theme, columns: 7 }
    }, 1_000);
    const remote = toSharedProfileV2({
      ...initial,
      updatedAt: "2026-07-12T02:00:00.000Z",
      theme: { ...initial.theme, columns: 4 }
    });
    const login = vi.fn(async () => ({
        user: { id: "user:one", email: "one@example.test", role: "user" as const },
        scope: "full" as const,
        accessToken: "access",
        accessExpiresAt: "2026-07-12T03:00:00.000Z",
        refreshToken: "refresh",
        refreshExpiresAt: "2026-08-12T03:00:00.000Z"
      }));
    const api = {
      login,
      getProfile: vi.fn(async () => ({
        profile: remote,
        version: 6,
        profileHash: "remote-6",
        schemaVersion: 2 as const,
        updatedAt: "2026-07-12T02:00:00.000Z"
      }))
    } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => Date.parse("2026-07-12T02:30:00.000Z"));

    const connected = await runtime.login({
      baseUrl: "https://sync.example.test",
      email: "one@example.test",
      password: "secret-password"
    });
    expect(connected.session?.firstConnectionPending).toBe(true);
    expect(connected.firstConnection).toBe("both-have-data");
    expect(login).toHaveBeenCalledWith({
      email: "one@example.test",
      password: "secret-password",
      deviceId: initial.deviceId
    });
    expect(await store.getSyncMetadata(connected.session!.accountScope, initial.profileId)).toMatchObject({
      baseHash: "remote-6"
    });
    expect((await store.getLocalProfile())?.profile.theme.columns).toBe(7);

    const completed = await runtime.completeFirstConnection("use-remote");
    expect(completed.session?.firstConnectionPending).toBe(false);
    expect((await runtime.getSession())?.firstConnectionPending).toBe(false);
    expect((await store.getLocalProfile())?.profile.theme.columns).toBe(4);
  });

  it("keeps the outbox paused until the first-connection strategy completes", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const now = Date.parse("2026-07-12T02:30:00.000Z");
    const putProfile = vi.fn(async (_token: string, request: { profile: ReturnType<typeof toSharedProfileV2> }) => ({
      profile: request.profile,
      version: 1,
      profileHash: "server-1",
      schemaVersion: 2 as const
    }));
    const api = {
      login: vi.fn(async () => ({
        user: { id: "user:one", email: "one@example.test", role: "user" as const },
        scope: "full" as const,
        accessToken: "access",
        accessExpiresAt: "2026-07-12T03:00:00.000Z",
        refreshToken: "refresh",
        refreshExpiresAt: "2026-08-12T03:00:00.000Z"
      })),
      getProfile: vi.fn(async () => ({ profile: null, version: 0, profileHash: null, schemaVersion: 2 as const })),
      putProfile
    } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => now);

    await runtime.login({ baseUrl: "https://sync.example.test", email: "one@example.test", password: "secret-password" });
    await store.commitProfile({
      ...initial,
      updatedAt: "2026-07-12T02:20:00.000Z",
      theme: { ...initial.theme, columns: 7 }
    }, now - quietOutboxDelayMs - 1);

    await expect(runtime.drain()).resolves.toEqual({ status: "connection-pending" });
    expect(putProfile).not.toHaveBeenCalled();
    await runtime.completeFirstConnection("use-local");
    expect((await runtime.getSession())?.firstConnectionPending).toBe(false);
  });

  it("rejects a delayed use-remote result after logout instead of applying the old account", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const remote = toSharedProfileV2({
      ...initial,
      updatedAt: "2026-07-12T02:00:00.000Z",
      theme: { ...initial.theme, columns: 8 }
    });
    const delayedRemote = deferred<{
      profile: typeof remote;
      version: number;
      profileHash: string;
      schemaVersion: 2;
    }>();
    const completeStarted = deferred<void>();
    let profileReads = 0;
    const remoteRecord = { profile: remote, version: 4, profileHash: "remote-4", schemaVersion: 2 as const };
    const api = {
      login: vi.fn(async () => ({
        user: { id: "user:one", email: "one@example.test", role: "user" as const },
        scope: "full" as const,
        accessToken: "access",
        accessExpiresAt: "2026-07-12T03:00:00.000Z",
        refreshToken: "refresh",
        refreshExpiresAt: "2026-08-12T03:00:00.000Z"
      })),
      getProfile: vi.fn(async () => {
        profileReads += 1;
        if (profileReads === 1) return remoteRecord;
        completeStarted.resolve();
        return delayedRemote.promise;
      }),
      logout: vi.fn(async () => undefined)
    } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => Date.parse("2026-07-12T02:30:00.000Z"));
    await runtime.login({ baseUrl: "https://sync.example.test", email: "one@example.test", password: "secret-password" });

    const completing = runtime.completeFirstConnection("use-remote");
    await completeStarted.promise;
    await runtime.logout();
    delayedRemote.resolve(remoteRecord);

    await expect(completing).rejects.toMatchObject({ code: "AUTH_SESSION_CHANGED" });
    expect((await store.getLocalProfile())?.profile.theme.columns).toBe(initial.theme.columns);
  });

  it("uses an access-only migration session to read and explicitly adopt the remote profile", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    const remote = toSharedProfileV2({
      ...initial,
      updatedAt: "2026-07-12T02:00:00.000Z",
      theme: { ...initial.theme, columns: 8 }
    });
    const putProfile = vi.fn();
    const api = {
      login: vi.fn(async () => ({
        user: { id: "user:legacy", email: "legacy@example.test", role: "user" as const },
        scope: "migration_read" as const,
        accessToken: "access-legacy",
        accessExpiresAt: "2026-07-12T03:00:00.000Z"
      })),
      getProfile: vi.fn(async () => ({
        profile: remote,
        version: 4,
        profileHash: "remote-4",
        schemaVersion: 2 as const,
        updatedAt: "2026-07-12T02:00:00.000Z"
      })),
      putProfile
    } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => Date.parse("2026-07-12T02:30:00.000Z"));

    const connected = await runtime.login({
      baseUrl: "https://sync.example.test/root/?ignored=1#fragment",
      email: "legacy@example.test",
      password: "secret-password"
    });
    expect(connected.session).toMatchObject({
      scope: "migration_read",
      baseUrl: "https://sync.example.test/root"
    });
    expect((await store.getLocalProfile())?.profile.theme.columns).toBe(initial.theme.columns);

    await runtime.completeFirstConnection("use-remote");

    expect((await store.getLocalProfile())?.profile.theme.columns).toBe(8);
    await expect(runtime.drain()).resolves.toEqual({ status: "read-only" });
    expect(putProfile).not.toHaveBeenCalled();
  });

  it("blocks write selection and private catalog access for migration_read sessions", async () => {
    const initial = createDefaultProfile();
    await store.initialize(initial);
    await vault.save({
      version: 2,
      scope: "migration_read",
      sessionGeneration: "session:legacy",
      firstConnectionPending: false,
      accountScope: "account:legacy",
      baseUrl: "https://sync.example.test",
      userId: "user:legacy",
      email: "legacy@example.test",
      accessToken: "access-legacy",
      accessExpiresAt: Date.parse("2026-07-12T03:00:00.000Z"),
      updatedAt: Date.parse("2026-07-12T02:30:00.000Z")
    });
    await store.activateAccount({
      accountScope: "account:legacy",
      baseUrl: "https://sync.example.test",
      userId: "user:legacy",
      profileId: initial.profileId,
      baseVersion: 0,
      baseSnapshot: toSharedProfileV2(initial)
    });
    const listStyles = vi.fn();
    const api = { listStyles } as unknown as WorkerApi;
    const runtime = new SyncWorkerRuntime(store, vault, () => api, () => Date.parse("2026-07-12T02:30:00.000Z"));

    await expect(runtime.completeFirstConnection("use-local")).rejects.toMatchObject({
      code: "MIGRATION_READ_ONLY"
    });
    await expect(runtime.getCatalog("styles")).rejects.toMatchObject({
      code: "MIGRATION_READ_ONLY"
    });
    expect(listStyles).not.toHaveBeenCalled();
  });
});
