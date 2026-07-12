import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, ApiError } from "./api";

function response(status: number, body: unknown, headers: Record<string, string> = {}) {
  return new Response(body === undefined ? undefined : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers }
  });
}

describe("ApiClient", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("unwraps the v1 envelope and remembers request metadata", async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(200, { data: { items: [1, 2] }, requestId: "req-7" }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient();

    await expect(client.get<{ items: number[] }>("/api/admin/v1/users")).resolves.toEqual({ items: [1, 2] });
    expect(client.lastRequestId).toBe("req-7");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/admin/v1/users",
      expect.objectContaining({ credentials: "same-origin", method: "GET" })
    );
  });

  it("adds CSRF only to state-changing requests", async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(200, { data: { ok: true } }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient();
    client.setCsrfToken("csrf-123");

    await client.post("/api/admin/v1/users/u1/status", { status: "suspended" });
    const init = fetchMock.mock.calls[0]?.[1] as RequestInit;
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("csrf-123");
    expect(new Headers(init.headers).get("Content-Type")).toBe("application/json");
  });

  it("uses a legacy endpoint only for safe reads when v1 is unavailable", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(response(404, { error: { code: "NOT_FOUND", message: "missing" } }))
      .mockResolvedValueOnce(response(200, { users: 3 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient();

    await expect(client.getWithLegacy<{ users: number }>("/api/admin/v1/overview", "/api/admin/summary")).resolves.toEqual({ users: 3 });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("never downgrades a write and exposes stable API errors", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      response(426, { error: { code: "UPGRADE_REQUIRED", message: "请升级后端", details: { minimum: "0.2.0" } }, requestId: "req-up" })
    );
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient();

    const error = await client.post("/api/admin/v1/catalog/styles", { name: "quiet" }).catch((value) => value);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 426, code: "UPGRADE_REQUIRED", requestId: "req-up" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("notifies the app when an authenticated admin request loses its session", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(401, { error: { code: "UNAUTHORIZED", message: "登录已失效" } })));
    const client = new ApiClient();
    const listener = vi.fn();
    const unsubscribe = client.onUnauthorized(listener);

    await expect(client.get("/api/admin/v1/users")).rejects.toMatchObject({ status: 401 });
    expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe();
  });
});
