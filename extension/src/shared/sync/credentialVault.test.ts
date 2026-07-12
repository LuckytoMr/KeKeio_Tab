import { describe, expect, it } from "vitest";
import { canWriteSyncSession, CredentialVault, isSameWritableSyncSession, type KeyValueStorage } from "./credentialVault";

class MemoryStorage implements KeyValueStorage {
  values: Record<string, unknown> = {};
  async get(key: string) { return { [key]: this.values[key] }; }
  async set(values: Record<string, unknown>) { Object.assign(this.values, values); }
  async remove(key: string) { delete this.values[key]; }
}

describe("CredentialVault", () => {
  it("allows sync writes only after a full session completes first connection", () => {
    expect(canWriteSyncSession({ readOnly: false, firstConnectionPending: false })).toBe(true);
    expect(canWriteSyncSession({ readOnly: false, firstConnectionPending: true })).toBe(false);
    expect(canWriteSyncSession({ readOnly: true, firstConnectionPending: false })).toBe(false);
    expect(canWriteSyncSession(undefined)).toBe(false);
  });

  it("accepts delayed UI data only for the same writable session generation", () => {
    const expected = {
      accountScope: "account:one",
      sessionGeneration: "session:one",
      readOnly: false,
      firstConnectionPending: false
    };
    expect(isSameWritableSyncSession(expected, { ...expected })).toBe(true);
    expect(isSameWritableSyncSession(expected, { ...expected, sessionGeneration: "session:two" })).toBe(false);
    expect(isSameWritableSyncSession(expected, { ...expected, accountScope: "account:two" })).toBe(false);
    expect(isSameWritableSyncSession(expected, { ...expected, firstConnectionPending: true })).toBe(false);
    expect(isSameWritableSyncSession(expected, { ...expected, readOnly: true })).toBe(false);
    expect(isSameWritableSyncSession(expected, undefined)).toBe(false);
  });

  it("exposes only public account metadata outside the worker runtime", async () => {
    const storage = new MemoryStorage();
    const vault = new CredentialVault(storage);
    await vault.save({
      version: 2,
      scope: "full",
      sessionGeneration: "session:one",
      firstConnectionPending: false,
      accountScope: "account:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-secret",
      accessExpiresAt: Date.now() + 60_000,
      refreshToken: "refresh-secret",
      refreshExpiresAt: Date.now() + 86_400_000,
      updatedAt: Date.now()
    });

    const publicSession = await vault.getPublicSession();

    expect(publicSession).toMatchObject({ accountScope: "account:one", email: "one@example.test", readOnly: false });
    expect(publicSession).toMatchObject({ firstConnectionPending: false });
    expect(JSON.stringify(publicSession)).not.toContain("access-secret");
    expect(JSON.stringify(publicSession)).not.toContain("refresh-secret");
    const privateCredentials = await vault.loadPrivate();
    expect(privateCredentials?.scope).toBe("full");
    if (privateCredentials?.scope !== "full") throw new Error("expected full credentials");
    expect(privateCredentials.refreshToken).toBe("refresh-secret");
  });

  it("persists an access-only migration_read session without inventing refresh credentials", async () => {
    const storage = new MemoryStorage();
    const vault = new CredentialVault(storage);
    await vault.save({
      version: 2,
      scope: "migration_read",
      sessionGeneration: "session:legacy",
      firstConnectionPending: true,
      accountScope: "account:legacy",
      baseUrl: "https://sync.example.test",
      userId: "user:legacy",
      email: "legacy@example.test",
      accessToken: "access-legacy",
      accessExpiresAt: Date.now() + 60_000,
      updatedAt: Date.now()
    });

    expect(await vault.loadPrivate()).toMatchObject({
      scope: "migration_read",
      accessToken: "access-legacy"
    });
    const publicSession = await vault.getPublicSession();
    expect(publicSession).toMatchObject({
      scope: "migration_read",
      email: "legacy@example.test",
      readOnly: true,
      firstConnectionPending: true
    });
    expect(JSON.stringify(publicSession)).not.toContain("access-legacy");
    expect(publicSession).not.toHaveProperty("refreshExpiresAt");
  });

  it("invalidates the current generation before logout and rejects a delayed credential replacement", async () => {
    const storage = new MemoryStorage();
    const vault = new CredentialVault(storage);
    const current = {
      version: 2 as const,
      scope: "full" as const,
      sessionGeneration: "session:one",
      firstConnectionPending: false,
      accountScope: "account:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-one",
      accessExpiresAt: Date.now() + 60_000,
      refreshToken: "refresh-one",
      refreshExpiresAt: Date.now() + 86_400_000,
      updatedAt: Date.now()
    };
    await vault.save(current);

    const loggedOut = await vault.takeAndClear();
    const replaced = await vault.replaceIfCurrent(
      { sessionGeneration: current.sessionGeneration, refreshToken: current.refreshToken },
      { ...current, accessToken: "access-late", refreshToken: "refresh-late" }
    );

    expect(loggedOut).toMatchObject({ sessionGeneration: "session:one", refreshToken: "refresh-one" });
    expect(replaced).toBe(false);
    expect(await vault.loadPrivate()).toBeUndefined();
  });

  it("clears only the matching session generation", async () => {
    const storage = new MemoryStorage();
    const vault = new CredentialVault(storage);
    const current = {
      version: 2 as const,
      scope: "migration_read" as const,
      sessionGeneration: "session:new",
      firstConnectionPending: true,
      accountScope: "account:new",
      baseUrl: "https://sync.example.test",
      userId: "user:new",
      email: "new@example.test",
      accessToken: "access-new",
      accessExpiresAt: Date.now() + 60_000,
      updatedAt: Date.now()
    };
    await vault.save(current);

    expect(await vault.clearIfCurrent("session:old")).toBe(false);
    expect((await vault.loadPrivate())?.sessionGeneration).toBe("session:new");
    expect(await vault.clearIfCurrent("session:new")).toBe(true);
    expect(await vault.loadPrivate()).toBeUndefined();
  });

  it("clears first-connection pending only for the matching session generation", async () => {
    const storage = new MemoryStorage();
    const vault = new CredentialVault(storage);
    await vault.save({
      version: 2,
      scope: "migration_read",
      sessionGeneration: "session:legacy",
      firstConnectionPending: true,
      accountScope: "account:legacy",
      baseUrl: "https://sync.example.test",
      userId: "user:legacy",
      email: "legacy@example.test",
      accessToken: "access-legacy",
      accessExpiresAt: Date.now() + 60_000,
      updatedAt: Date.now()
    });

    expect(await vault.completeFirstConnection("session:stale")).toBe(false);
    expect((await vault.loadPrivate())?.firstConnectionPending).toBe(true);
    expect(await vault.completeFirstConnection("session:legacy")).toBe(true);
    expect((await vault.loadPrivate())?.firstConnectionPending).toBe(false);
  });

  it("does not let a delayed token replacement roll first-connection completion back", async () => {
    const storage = new MemoryStorage();
    const vault = new CredentialVault(storage);
    const stale = {
      version: 2 as const,
      scope: "full" as const,
      sessionGeneration: "session:one",
      firstConnectionPending: true,
      accountScope: "account:one",
      baseUrl: "https://sync.example.test",
      userId: "user:one",
      email: "one@example.test",
      accessToken: "access-one",
      accessExpiresAt: Date.now() + 60_000,
      refreshToken: "refresh-one",
      refreshExpiresAt: Date.now() + 86_400_000,
      updatedAt: Date.now()
    };
    await vault.save(stale);
    await vault.completeFirstConnection(stale.sessionGeneration);

    expect(await vault.replaceIfCurrent(
      { sessionGeneration: stale.sessionGeneration, refreshToken: stale.refreshToken },
      { ...stale, accessToken: "access-next", refreshToken: "refresh-next" }
    )).toBe(true);
    expect(await vault.loadPrivate()).toMatchObject({
      accessToken: "access-next",
      refreshToken: "refresh-next",
      firstConnectionPending: false
    });
  });
});
