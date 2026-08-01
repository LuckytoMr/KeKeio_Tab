import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "../lib/api";
import { parseAdminLocation } from "../lib/router";
import { UserDetailPage } from "./users";

function clientStub(overrides: Partial<ApiClient>): ApiClient {
  return { get: vi.fn(), post: vi.fn(), ...overrides } as unknown as ApiClient;
}

describe("UserDetailPage version recovery", () => {
  it("shows a non-private structural diff and confirms with the human version number", async () => {
    const client = clientStub({
      get: vi.fn().mockResolvedValue({
        user: { id: "user-1", email: "member@example.test", status: "active", verificationStatus: "verified" },
        sessions: [], attempts: [], profile: { version: 5, schemaVersion: 2, groups: 2, shortcuts: 7, bytes: 2048 },
        versions: [{
          id: "pver_opaque_secret_id", version: 4, createdAt: "2026-07-12T01:00:00Z",
          summary: { groups: 1, shortcuts: 5, wallpaper: "builtin", styleId: "quark-flow" },
          changes: { currentVersion: 5, groupsDelta: -1, shortcutsDelta: -2, wallpaperChanged: false, styleChanged: true }
        }]
      })
    });
    const user = userEvent.setup();
    render(<UserDetailPage client={client} route={parseAdminLocation("/admin/users/user-1")} onNavigate={vi.fn()} notify={vi.fn()} />);

    expect(await screen.findByText("1 个分组 · 5 个快捷方式")).toBeInTheDocument();
    expect(screen.getByText("相对当前版本 5：分组 -1，快捷方式 -2，风格已变化")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "恢复版本 4" }));

    const dialog = screen.getByRole("dialog", { name: "恢复配置版本 4" });
    expect(dialog).toHaveTextContent("1 个分组 · 5 个快捷方式");
    expect(dialog).not.toHaveTextContent("pver_opaque_secret_id");
  });
});

describe("UserDetailPage browser sessions", () => {
  it("shows recognized browsers, versions and a legacy fallback", async () => {
    const client = clientStub({
      get: vi.fn().mockResolvedValue({
        user: {
          id: "user-2", email: "browser@example.test", status: "active", verificationStatus: "verified",
          browserCount: 3, browserFamilies: ["chrome", "edge", "unknown"]
        },
        sessions: [
          { id: "edge-session", deviceId: "device-edge-1234567890", browserFamily: "edge", browserVersion: "127.0.2651.74", lastUsedAt: "2026-08-01T09:00:00Z" },
          { id: "chrome-session", deviceId: "device-chrome", browserFamily: "chrome", browserVersion: "126.0.0.0", lastUsedAt: "2026-08-01T08:00:00Z" },
          { id: "legacy-session", deviceName: "device-legacy", lastUsedAt: "2026-08-01T07:00:00Z" }
        ],
        attempts: [], versions: [], profile: null
      })
    });
    render(<UserDetailPage client={client} route={parseAdminLocation("/admin/users/user-2")} onNavigate={vi.fn()} notify={vi.fn()} />);

    expect(await screen.findByText("Microsoft Edge 127.0.2651.74")).toBeInTheDocument();
    expect(screen.getByText("Google Chrome 126.0.0.0")).toBeInTheDocument();
    expect(screen.getAllByText("未知浏览器").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/最近认证/)).toHaveLength(3);
    expect(screen.getByRole("button", { name: "撤销 Microsoft Edge 127.0.2651.74 的会话" })).toBeInTheDocument();
    expect(screen.getByText("浏览器首次写入云端后会显示诊断记录。")).toBeInTheDocument();
  });
});
