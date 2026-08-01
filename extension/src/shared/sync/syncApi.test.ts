import { afterEach, describe, expect, it, vi } from "vitest";
import { createDefaultProfile } from "../profile/defaults";
import { toSharedProfileV2 } from "../profile/sharedProfile";
import { ApiError, canonicalBackendBaseUrl, SyncApiClient } from "./syncApi";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("SyncApiClient", () => {
  it("canonicalizes a public backend URL", () => {
    expect(canonicalBackendBaseUrl(" HTTPS://Sync.Example.Test:8443/root///?token=secret#fragment "))
      .toBe("https://sync.example.test:8443/root");
    expect(() => canonicalBackendBaseUrl("http://sync.example.test"))
      .toThrow(/HTTPS/);
  });

  it.each([
    "http://10.0.0.8:8787",
    "http://172.16.5.8",
    "http://172.31.255.254",
    "http://192.168.50.2",
    "http://[fd12:3456::8]:8787"
  ])("allows an explicit RFC1918 or ULA backend over HTTP: %s", (url) => {
    expect(canonicalBackendBaseUrl(url)).toBe(url);
  });

  it.each(["http://8.8.8.8", "http://172.32.0.1", "http://203.0.113.8"])("rejects public HTTP backends: %s", (url) => {
    expect(() => canonicalBackendBaseUrl(url)).toThrow(/HTTPS/);
  });

  it("aborts a request after the default timeout and releases its timer", async () => {
    vi.useFakeTimers();
    let requestSignal: AbortSignal | undefined;
    const fetchMock = vi.fn((_url: string, init?: RequestInit) => {
      requestSignal = init?.signal ?? undefined;
      return new Promise<Response>((_resolve, reject) => {
        requestSignal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")), { once: true });
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    const client = new SyncApiClient("https://sync.example.test");

    const outcome = client.bootstrap().catch((error: unknown) => error);

    expect(requestSignal).toBeInstanceOf(AbortSignal);
    expect(requestSignal?.aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(30_000);
    await expect(outcome).resolves.toMatchObject({ status: 0, code: "REQUEST_TIMEOUT" });
    expect(requestSignal?.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("clears the timeout after a complete JSON response", async () => {
    vi.useFakeTimers();
    let requestSignal: AbortSignal | undefined;
    vi.stubGlobal("fetch", vi.fn(async (_url: string, init?: RequestInit) => {
      requestSignal = init?.signal ?? undefined;
      return new Response(JSON.stringify({ data: { ready: true } }), {
        status: 200,
        headers: { "content-type": "application/json" }
      });
    }));
    const client = new SyncApiClient("https://sync.example.test");

    await expect(client.bootstrap()).resolves.toEqual({ ready: true });

    expect(requestSignal).toBeInstanceOf(AbortSignal);
    expect(requestSignal?.aborted).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("uses the v1 CAS contract and the same mutation id as Idempotency-Key", async () => {
    const profile = toSharedProfileV2(createDefaultProfile());
    const fetchMock = vi.fn(async (_url: string, init?: RequestInit) =>
      new Response(JSON.stringify({
        data: {
          profile,
          version: 13,
          profileHash: "a".repeat(64),
          schemaVersion: 2,
          updatedAt: "2026-07-12T00:00:00.000Z",
          idempotentReplay: false
        },
        requestId: "req_1"
      }), { status: 200, headers: { "content-type": "application/json" } })
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new SyncApiClient("https://sync.example.test/");

    const result = await client.putProfile("access", {
      baseVersion: 12,
      mutationId: "mut_test",
      deviceId: "device:test",
      schemaVersion: 2,
      profile
    });

    expect(result.profileHash).toBe("a".repeat(64));

    expect(fetchMock).toHaveBeenCalledWith("https://sync.example.test/api/v1/sync/profile", expect.objectContaining({
      method: "PUT",
      headers: expect.objectContaining({ Authorization: "Bearer access", "Idempotency-Key": "mut_test" })
    }));
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toMatchObject({ baseVersion: 12, mutationId: "mut_test" });
  });

  it("preserves structured conflict details from a non-2xx response", async () => {
    const profile = toSharedProfileV2(createDefaultProfile());
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      error: {
        code: "PROFILE_CONFLICT",
        message: "云端配置已更新",
        details: {
          conflictId: "conflict:server",
          baseVersion: 8,
          currentVersion: 9,
          currentHash: "b".repeat(64),
          currentProfile: profile
        }
      },
      requestId: "req_conflict"
    }), { status: 409, headers: { "content-type": "application/json" } })));
    const client = new SyncApiClient("https://sync.example.test");

    const error = await client.putProfile("access", {
      baseVersion: 8,
      mutationId: "mut_test",
      deviceId: "device:test",
      schemaVersion: 2,
      profile
    }).catch((reason: unknown) => reason);

    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 409, code: "PROFILE_CONFLICT", requestId: "req_conflict" });
    expect((error as ApiError).details).toMatchObject({
      conflictId: "conflict:server",
      baseVersion: 8,
      currentVersion: 9,
      currentHash: "b".repeat(64)
    });
  });

  it.each(["server-empty", null])("accepts a server-empty PROFILE_CONFLICT with currentHash %s", async (currentHash) => {
    const profile = toSharedProfileV2(createDefaultProfile());
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      error: {
        code: "PROFILE_CONFLICT",
        message: "云端配置为空",
        details: {
          conflictId: "conflict:empty",
          baseVersion: 8,
          currentVersion: 0,
          currentHash,
          currentProfile: null
        }
      },
      requestId: "req_empty_conflict"
    }), { status: 409, headers: { "content-type": "application/json" } })));
    const client = new SyncApiClient("https://sync.example.test");

    const error = await client.putProfile("access", {
      baseVersion: 8,
      mutationId: "mut_empty",
      deviceId: "device:test",
      schemaVersion: 2,
      profile
    }).catch((reason: unknown) => reason);

    expect(error).toMatchObject({
      status: 409,
      code: "PROFILE_CONFLICT",
      details: { conflictId: "conflict:empty", baseVersion: 8, currentVersion: 0, currentHash, currentProfile: null }
    });
  });

  it("rejects a non-empty profile response when profileHash is missing", async () => {
    const profile = toSharedProfileV2(createDefaultProfile());
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      data: { profile, version: 1, schemaVersion: 2 }
    }), { status: 200, headers: { "content-type": "application/json" } })));
    const client = new SyncApiClient("https://sync.example.test");

    await expect(client.getProfile("access")).rejects.toBeInstanceOf(Error);
  });

  it("accepts the exact empty GET profile contract with a nullable profile hash", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      data: { profile: null, version: 0, profileHash: null, schemaVersion: 2 }
    }), { status: 200, headers: { "content-type": "application/json" } })));
    const client = new SyncApiClient("https://sync.example.test");

    await expect(client.getProfile("access")).resolves.toEqual({
      profile: null,
      version: 0,
      profileHash: null,
      schemaVersion: 2
    });
  });

  it("rejects PROFILE_CONFLICT details when currentHash is missing", async () => {
    const profile = toSharedProfileV2(createDefaultProfile());
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      error: {
        code: "PROFILE_CONFLICT",
        message: "云端配置已更新",
        details: { conflictId: "conflict:server", currentVersion: 9, currentProfile: profile }
      },
      requestId: "req_conflict"
    }), { status: 409, headers: { "content-type": "application/json" } })));
    const client = new SyncApiClient("https://sync.example.test");

    const error = await client.putProfile("access", {
      baseVersion: 8,
      mutationId: "mut_test",
      deviceId: "device:test",
      schemaVersion: 2,
      profile
    }).catch((reason: unknown) => reason);

    expect(error).toMatchObject({ status: 409, code: "INVALID_RESPONSE" });
  });

  it("rejects non-JSON success responses instead of treating them as valid", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("ok", { status: 200, headers: { "content-type": "text/plain" } })));
    const client = new SyncApiClient("https://sync.example.test");

    await expect(client.getProfile("access")).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  });

  it("sends the stable IndexedDB device id on login and the idempotent request id on refresh", async () => {
    const responseData = {
      user: { id: "user:one", email: "one@example.test", role: "user" },
      scope: "full",
      accessToken: "access",
      accessExpiresAt: "2026-07-12T01:00:00.000Z",
      refreshToken: "refresh-next",
      refreshExpiresAt: "2026-08-12T01:00:00.000Z"
    };
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify({ data: responseData, requestId: "req" }), {
      status: 200,
      headers: { "content-type": "application/json" }
    }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new SyncApiClient("https://sync.example.test");

    await client.login({ email: "one@example.test", password: "secret-password", deviceId: "device:stable" });
    await client.refresh("refresh-current", "refresh_request_1");

    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toEqual({
      email: "one@example.test",
      password: "secret-password",
      deviceId: "device:stable"
    });
    expect(JSON.parse(String(fetchMock.mock.calls[1][1]?.body))).toEqual({
      refreshToken: "refresh-current",
      requestId: "refresh_request_1"
    });
  });

  it("accepts an access-only migration_read login response", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      data: {
        user: { id: "user:legacy", email: "legacy@example.test", role: "user", status: "legacy_unverified" },
        scope: "migration_read",
        accessToken: "access-legacy",
        accessExpiresAt: "2026-07-12T01:15:00.000Z"
      }
    }), { status: 200, headers: { "content-type": "application/json" } })));
    const client = new SyncApiClient("https://sync.example.test");

    const tokens = await client.login({
      email: "legacy@example.test",
      password: "secret-password",
      deviceId: "device:legacy"
    });

    expect(tokens).toMatchObject({ scope: "migration_read", accessToken: "access-legacy" });
    expect("refreshToken" in tokens).toBe(false);
  });

  it("accepts the backend logout 204 without parsing an empty JSON body", async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new SyncApiClient("https://sync.example.test");

    await expect(client.logout("access-current", "refresh-current")).resolves.toBeUndefined();

    expect(fetchMock).toHaveBeenCalledWith("https://sync.example.test/api/v1/auth/logout", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({ Authorization: "Bearer access-current" }),
      body: JSON.stringify({ refreshToken: "refresh-current" }),
      signal: expect.any(AbortSignal)
    }));
  });

  it("logs out an access-only session with Authorization and an empty body object", async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new SyncApiClient("https://sync.example.test");

    await expect(client.logout("access-legacy")).resolves.toBeUndefined();

    expect(fetchMock).toHaveBeenCalledWith("https://sync.example.test/api/v1/auth/logout", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({ Authorization: "Bearer access-legacy" }),
      body: JSON.stringify({})
    }));
  });

  it("uses the v1 account recovery endpoints with generic email-only requests", async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify({
      data: { accepted: true },
      requestId: "req_recovery"
    }), { status: 202, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new SyncApiClient("https://sync.example.test");

    await expect(client.resendVerification("pending@example.test")).resolves.toEqual({ accepted: true });
    await expect(client.forgotPassword("one@example.test")).resolves.toEqual({ accepted: true });

    expect(fetchMock.mock.calls.map(([url, init]) => [url, JSON.parse(String(init?.body))])).toEqual([
      ["https://sync.example.test/api/v1/auth/resend-verification", { email: "pending@example.test" }],
      ["https://sync.example.test/api/v1/auth/forgot-password", { email: "one@example.test" }]
    ]);
  });

  it("routes UHDpaper page and image requests through authenticated backend endpoints", async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify({ data: { ok: true } }), {
      status: 200,
      headers: { "content-type": "application/json" }
    }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new SyncApiClient("https://sync.example.test/root");
    const pageUrl = "https://www.uhdpaper.com/?page=2&search=space";
    const imageUrl = "https://img.uhdpaper.com/wallpaper/space.jpg?dl";

    await client.fetchUhdpaperPage("access-token", pageUrl);
    await client.fetchUhdpaperImage("access-token", imageUrl);

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      `https://sync.example.test/root/api/v1/catalog/uhdpaper/page?url=${encodeURIComponent(pageUrl)}`,
      `https://sync.example.test/root/api/v1/catalog/uhdpaper/image?url=${encodeURIComponent(imageUrl)}`
    ]);
    expect(fetchMock.mock.calls[0][1]).toMatchObject({
      headers: expect.objectContaining({ Authorization: "Bearer access-token" })
    });
    expect(fetchMock.mock.calls[1][1]).toMatchObject({
      headers: expect.objectContaining({ Authorization: "Bearer access-token" })
    });
  });
});
