import { render, screen, waitFor } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "../lib/api";
import { ApiError } from "../lib/api";
import { parseAdminLocation } from "../lib/router";
import { ContentDetailPage, ContentListPage } from "./content";
import { OverviewPage } from "./overview";
import { UsersPage } from "./users";

function clientStub(overrides: Partial<ApiClient>): ApiClient {
  return { get: vi.fn(), getWithLegacy: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn(), ...overrides } as unknown as ApiClient;
}

describe("OverviewPage", () => {
  it("loads only overview data and deep-links attention items with filters", async () => {
    const client = clientStub({
      getWithLegacy: vi.fn().mockResolvedValue({
        health: [
          { id: "api", label: "API", status: "healthy", detail: "可用" },
          { id: "smtp", label: "邮件", status: "degraded", detail: "最近一次发送失败" }
        ],
        attention: [{ id: "conflict-1", kind: "conflict", severity: "warning", title: "3 个同步冲突待处理", detail: "最早发生于 18 分钟前", href: "/admin/sync/conflicts?status=open" }],
        sync24h: { successRate: 98.6, p95Ms: 142, unauthorized: 2, conflicts: 3, throttled: 1, serverErrors: 0, idempotentReplays: 7 },
        recent: []
      })
    });
    render(<OverviewPage client={client} onNavigate={vi.fn()} />);

    expect(await screen.findByRole("heading", { name: "概览" })).toBeInTheDocument();
    expect(screen.getByText("最近一次发送失败")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /3 个同步冲突待处理/ })).toHaveAttribute("href", "/admin/sync/conflicts?status=open");
    expect(client.getWithLegacy).toHaveBeenCalledWith("/api/admin/v1/overview", "/api/admin/summary", expect.any(AbortSignal));
    expect(client.get).not.toHaveBeenCalled();
  });
});

describe("UsersPage", () => {
  it("uses URL-backed list state and carries the return URL into details", async () => {
    const getWithLegacy = vi.fn().mockResolvedValue({
      items: [{ id: "u-1", email: "alice@example.com", status: "active", verificationStatus: "verified", lastActivityAt: "2026-07-12T01:00:00Z", deviceCount: 2 }],
      nextCursor: "next-page"
    });
    const client = clientStub({ getWithLegacy });
    const onNavigate = vi.fn();
    const route = parseAdminLocation("/admin/users?q=alice&status=active&sort=-lastActivityAt&cursor=next%3A1");
    const user = userEvent.setup();
    render(<UsersPage client={client} route={route} onNavigate={onNavigate} />);

    expect(await screen.findByRole("heading", { name: "用户" })).toBeInTheDocument();
    expect(screen.getByLabelText("搜索用户")).toHaveValue("alice");
    expect(screen.getByLabelText("账号状态")).toHaveValue("active");
    expect(screen.getByText("alice@example.com")).toBeInTheDocument();
    expect(getWithLegacy.mock.calls[0]?.[0]).toContain("q=alice");
    expect(getWithLegacy.mock.calls[0]?.[0]).toContain("cursor=next%3A1");

    await user.click(screen.getByRole("link", { name: "查看 alice@example.com" }));
    expect(onNavigate).toHaveBeenCalledWith(expect.stringContaining("/admin/users/u-1?returnTo="));
    expect(decodeURIComponent(onNavigate.mock.calls[0]?.[0] as string)).toContain("/admin/users?q=alice&status=active&sort=-lastActivityAt&cursor=next%3A1");
  });
});

describe("Content workbench", () => {
  it("renders a list-first C layout and opens a canonical detail route", async () => {
    const client = clientStub({
      getWithLegacy: vi.fn().mockResolvedValue({
        items: [{ id: "style:quiet", name: "安静流", status: "draft", visibility: "enabled", updatedAt: "2026-07-12T01:00:00Z", revision: 3 }]
      })
    });
    const onNavigate = vi.fn();
    const user = userEvent.setup();
    render(<ContentListPage client={client} route={parseAdminLocation("/admin/content/styles?status=draft")} onNavigate={onNavigate} />);

    expect(await screen.findByRole("heading", { name: "风格" })).toBeInTheDocument();
    expect(screen.getByText("选择资源查看详情")).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "编辑 安静流" }));
    expect(onNavigate).toHaveBeenCalledWith(expect.stringContaining("/admin/content/styles/style%3Aquiet?returnTo="));
  });

  it("keeps a detail failure local and offers retry without losing the route", async () => {
    const get = vi
      .fn()
      .mockRejectedValueOnce(new ApiError({ status: 503, code: "UNAVAILABLE", message: "内容服务暂时不可用" }))
      .mockResolvedValueOnce({ item: { id: "style:quiet", name: "安静流", status: "draft", visibility: "enabled", revision: 3, fields: {} } });
    const client = clientStub({ get });
    const user = userEvent.setup();
    render(<ContentDetailPage client={client} route={parseAdminLocation("/admin/content/styles/style%3Aquiet?returnTo=%2Fadmin%2Fcontent%2Fstyles")} onNavigate={vi.fn()} notify={vi.fn()} />);

    expect(await screen.findByRole("alert")).toHaveTextContent("内容服务暂时不可用");
    await user.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() => expect(screen.getByRole("heading", { name: "安静流" })).toBeInTheDocument());
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("round-trips wallpaper tags and variants without clearing existing URLs", async () => {
    const put = vi.fn().mockResolvedValue({ item: { id: "official:aurora" } });
    const client = clientStub({
      get: vi.fn().mockResolvedValue({
        item: {
          id: "official:aurora",
          name: "极光",
          status: "draft",
          visibility: "enabled",
          fields: {
            category: "nature",
            tags: ["aurora", "night"],
            minExtensionVersion: "0.2.0",
            sortIndex: 12,
            variants: [
              { id: "4k", label: "3840x2160", url: "https://cdn.example.test/aurora-4k.jpg" },
              { id: "hd", label: "1920x1080", url: "https://cdn.example.test/aurora-hd.jpg" }
            ]
          }
        },
        revisions: []
      }),
      put
    });
    const user = userEvent.setup();
    render(<ContentDetailPage client={client} route={parseAdminLocation("/admin/content/official/official%3Aaurora")} onNavigate={vi.fn()} notify={vi.fn()} />);

    await waitFor(() => expect(screen.getByLabelText("标签")).toHaveValue("aurora, night"));
    expect(screen.getByLabelText("4K 图片 URL")).toHaveValue("https://cdn.example.test/aurora-4k.jpg");
    expect(screen.getByLabelText("1080P 图片 URL")).toHaveValue("https://cdn.example.test/aurora-hd.jpg");

    await user.click(screen.getByRole("button", { name: "保存草稿" }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0]?.[1]).toMatchObject({
      tags: ["aurora", "night"],
      minExtensionVersion: "0.2.0",
      sortIndex: 12,
      variants: [
        { id: "4k", label: "3840x2160", url: "https://cdn.example.test/aurora-4k.jpg" },
        { id: "hd", label: "1920x1080", url: "https://cdn.example.test/aurora-hd.jpg" }
      ]
    });
  });
});
