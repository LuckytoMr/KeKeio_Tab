import { render, screen, waitFor } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "./lib/api";
import { ApiError } from "./lib/api";
import { App } from "./app";

function clientStub(overrides: Partial<ApiClient>): ApiClient {
  return { get: vi.fn(), getWithLegacy: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn(), setCsrfToken: vi.fn(), ...overrides } as unknown as ApiClient;
}

describe("App route orchestration", () => {
  it("treats /install as a standalone route without probing admin auth", async () => {
    window.history.replaceState(null, "", "/install");
    const get = vi.fn().mockResolvedValue({ state: "uninitialized", mode: "fresh_install", requiresCode: true });
    render(<App client={clientStub({ get })} />);

    expect(await screen.findByRole("heading", { name: "验证安装码" })).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/install/api/v1/status");
    expect(get).not.toHaveBeenCalledWith("/api/admin/v1/auth/session");
  });

  it("obtains a fresh pre-auth CSRF token after an expired session before rendering login", async () => {
    window.history.replaceState(null, "", "/admin/users?q=alice");
    let resolvePreAuth!: (probe: { authenticated: false; user: null; csrfToken: string }) => void;
    const preAuthProbe = new Promise<{ authenticated: false; user: null; csrfToken: string }>((resolve) => { resolvePreAuth = resolve; });
    const get = vi.fn()
      .mockRejectedValueOnce(new ApiError({ status: 401, code: "UNAUTHENTICATED", message: "请登录" }))
      .mockReturnValueOnce(preAuthProbe);
    const setCsrfToken = vi.fn();
    render(<App client={clientStub({ get, setCsrfToken })} />);

    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("heading", { name: "登录运维工作台" })).not.toBeInTheDocument();
    resolvePreAuth({ authenticated: false, user: null, csrfToken: "preauth" });

    expect(await screen.findByRole("heading", { name: "登录运维工作台" })).toBeInTheDocument();
    expect(window.location.pathname).toBe("/admin/login");
    expect(setCsrfToken).toHaveBeenCalledWith("preauth");
  });

  it("keeps the protected deep link on a transient session probe failure and recovers on retry", async () => {
    window.history.replaceState(null, "", "/admin/users/user-1?returnTo=%2Fadmin%2Fusers%3Fq%3Dalice");
    const get = vi.fn()
      .mockRejectedValueOnce(new Error("network unavailable"))
      .mockResolvedValueOnce({ authenticated: false, user: null, csrfToken: "preauth" });
    const user = userEvent.setup();
    render(<App client={clientStub({ get })} />);

    const retry = await screen.findByRole("button", { name: "重新检查管理员会话" });
    expect(screen.queryByRole("heading", { name: "登录运维工作台" })).not.toBeInTheDocument();
    expect(window.location.pathname + window.location.search).toBe("/admin/users/user-1?returnTo=%2Fadmin%2Fusers%3Fq%3Dalice");
    await user.click(retry);

    expect(await screen.findByRole("heading", { name: "登录运维工作台" })).toBeInTheDocument();
    expect(new URLSearchParams(window.location.search).get("returnTo")).toBe("/admin/users/user-1?returnTo=%2Fadmin%2Fusers%3Fq%3Dalice");
  });

  it("loads only the authenticated current route", async () => {
    window.history.replaceState(null, "", "/admin/overview");
    const get = vi.fn().mockResolvedValue({ authenticated: true, user: { id: "admin-1", email: "admin@example.com", displayName: "管理员" }, csrfToken: "csrf" });
    const getWithLegacy = vi.fn().mockResolvedValue({ health: [], attention: [], sync24h: {}, recent: [] });
    render(<App client={clientStub({ get, getWithLegacy })} />);

    expect(await screen.findByRole("heading", { name: "概览" })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "后台主导航" })).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/admin/v1/auth/session");
    await waitFor(() => expect(getWithLegacy).toHaveBeenCalledTimes(1));
  });

  it("returns to the protected deep link after login and focuses its page heading", async () => {
    window.history.replaceState(null, "", "/admin/users?q=alice");
    const session = { user: { id: "admin-1", email: "admin@example.com", displayName: "管理员" }, csrfToken: "csrf" };
    const get = vi.fn().mockResolvedValue({ authenticated: false, user: null, csrfToken: "preauth" });
    const post = vi.fn().mockResolvedValue(session);
    const getWithLegacy = vi.fn().mockResolvedValue({ items: [] });
    const user = userEvent.setup();
    render(<App client={clientStub({ get, post, getWithLegacy })} />);

    await screen.findByRole("heading", { name: "登录运维工作台" });
    expect(window.location.pathname).toBe("/admin/login");
    expect(new URLSearchParams(window.location.search).get("returnTo")).toBe("/admin/users?q=alice");

    await user.type(screen.getByLabelText("管理员邮箱"), "admin@example.com");
    await user.type(screen.getByLabelText("密码"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "登录后台" }));

    const heading = await screen.findByRole("heading", { name: "用户" });
    expect(window.location.pathname + window.location.search).toBe("/admin/users?q=alice");
    await waitFor(() => expect(heading).toHaveFocus());
  });

  it("focuses a detail heading that appears after asynchronous route data resolves", async () => {
    window.history.replaceState(null, "", "/admin/users/user-1");
    let resolveDetail!: (value: unknown) => void;
    const detail = new Promise((resolve) => { resolveDetail = resolve; });
    const get = vi.fn((path: string) => {
      if (path === "/api/admin/v1/auth/session") {
        return Promise.resolve({ authenticated: true, user: { id: "admin-1", email: "admin@example.com", displayName: "管理员" }, csrfToken: "csrf" });
      }
      if (path === "/api/admin/v1/users/user-1") return detail;
      return Promise.reject(new Error(`unexpected GET ${path}`));
    });
    render(<App client={clientStub({ get: get as unknown as ApiClient["get"] })} />);

    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/admin/v1/users/user-1", expect.any(AbortSignal)));
    resolveDetail({
      user: { id: "user-1", email: "member@example.test", status: "active", verificationStatus: "verified" },
      sessions: [], attempts: [], versions: [], profile: { version: 1, groups: 1, shortcuts: 1 }
    });

    const heading = await screen.findByRole("heading", { name: "member@example.test" });
    await waitFor(() => expect(heading).toHaveFocus());
  });
});
