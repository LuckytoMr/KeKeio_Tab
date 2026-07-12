import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parseAdminLocation } from "../lib/router";
import { AdminShell } from "./shell";

describe("AdminShell", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("exposes the five canonical domains and an accessible mobile drawer", async () => {
    const listeners = new Set<(event: MediaQueryListEvent) => void>();
    vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({
      matches: true,
      media: "(max-width: 1199px)",
      addEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
      removeEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener)
    }));
    const user = userEvent.setup();
    render(
      <AdminShell
        route={parseAdminLocation("/admin/overview")}
        user={{ id: "admin-1", email: "admin@example.com", displayName: "管理员" }}
        onNavigate={vi.fn()}
        onLogout={vi.fn()}
      >
        <h1>概览</h1>
      </AdminShell>
    );

    expect(screen.getAllByText("KeKeIO Tab")).toHaveLength(2);
    expect(screen.getAllByText("KT")).toHaveLength(2);
    const sidebar = document.querySelector("#admin-sidebar");
    expect(sidebar).toHaveAttribute("inert");
    expect(sidebar).toHaveAttribute("aria-hidden", "true");
    expect(screen.queryByRole("navigation", { name: "后台主导航" })).not.toBeInTheDocument();

    const menu = screen.getByRole("button", { name: "打开导航" });
    expect(menu).toHaveAttribute("aria-expanded", "false");
    await user.click(menu);
    expect(menu).toHaveAttribute("aria-expanded", "true");
    expect(sidebar).not.toHaveAttribute("inert");
    expect(sidebar).not.toHaveAttribute("aria-hidden");
    expect(screen.getByRole("navigation", { name: "后台主导航" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "概览" })).toHaveAttribute("href", "/admin/overview");
    expect(screen.getByRole("link", { name: "用户与同步" })).toHaveAttribute("href", "/admin/users");
    expect(screen.getByRole("link", { name: "内容" })).toHaveAttribute("href", "/admin/content/official");
    expect(screen.getByRole("link", { name: "发布与审计" })).toHaveAttribute("href", "/admin/releases");
    expect(screen.getByRole("link", { name: "安全与维护" })).toHaveAttribute("href", "/admin/security");
    expect(screen.getByRole("main", { hidden: true })).toHaveAttribute("inert");
    expect(document.querySelector(".mobile-header")).toHaveAttribute("inert");
    expect(document.querySelector(".mobile-header")).toHaveAttribute("aria-hidden", "true");
    expect(document.querySelector(".nav-scrim")).toHaveAttribute("aria-hidden", "true");
    expect(document.querySelector(".nav-scrim")).toHaveAttribute("tabindex", "-1");
    expect(screen.getAllByRole("button", { name: "关闭导航" }).some((button) => button === document.activeElement)).toBe(true);
    await user.keyboard("{Escape}");
    expect(menu).toHaveAttribute("aria-expanded", "false");
    expect(menu).toHaveFocus();
    expect(sidebar).toHaveAttribute("inert");
  });
});
