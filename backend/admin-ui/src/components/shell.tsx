import type { ComponentChildren } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { Activity, Boxes, Gauge, Menu, Settings, ShieldCheck, X } from "lucide-preact";
import type { AdminRoute } from "../lib/router";

export interface AdminUser {
  id: string;
  email: string;
  displayName?: string;
}

const domains = [
  { label: "概览", href: "/admin/overview", icon: Gauge, pages: ["overview"] },
  { label: "用户与同步", href: "/admin/users", icon: Activity, pages: ["users", "user-detail", "sync-attempts", "sync-conflicts"] },
  { label: "内容", href: "/admin/content/official", icon: Boxes, pages: ["content-list", "content-detail"] },
  { label: "发布与审计", href: "/admin/releases", icon: ShieldCheck, pages: ["releases", "admin-audit", "access-audit"] },
  { label: "安全与维护", href: "/admin/security", icon: Settings, pages: ["security", "maintenance", "backups", "system"] }
];

export function AdminShell({
  route,
  user,
  children,
  onNavigate,
  onLogout
}: {
  route: AdminRoute;
  user: AdminUser;
  children: ComponentChildren;
  onNavigate: (href: string) => void;
  onLogout: () => void;
}) {
  const [navOpen, setNavOpen] = useState(false);
  const [drawerMode, setDrawerMode] = useState(() => typeof window.matchMedia === "function" && window.matchMedia("(max-width: 1199px)").matches);
  const navToggleRef = useRef<HTMLButtonElement>(null);
  const sidebarRef = useRef<HTMLElement>(null);

  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const query = window.matchMedia("(max-width: 1199px)");
    const update = () => {
      setDrawerMode(query.matches);
      if (!query.matches) setNavOpen(false);
    };
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);

  useEffect(() => {
    if (!navOpen) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : navToggleRef.current;
    const sidebar = sidebarRef.current;
    const focusable = () => Array.from(sidebar?.querySelectorAll<HTMLElement>("a[href], button:not(:disabled)") ?? []);
    focusable()[0]?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setNavOpen(false);
        return;
      }
      if (event.key !== "Tab") return;
      const items = focusable();
      const first = items.at(0);
      const last = items.at(-1);
      if (!first || !last) return;
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      previous?.focus();
    };
  }, [navOpen]);

  const handleLink = (event: MouseEvent, href: string) => {
    if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    setNavOpen(false);
    onNavigate(href);
  };

  return (
    <div class="admin-shell" data-nav-open={navOpen ? "true" : "false"}>
      <a class="skip-link" href="#main-content">跳到主要内容</a>
      <header class="mobile-header" inert={drawerMode && navOpen} aria-hidden={drawerMode && navOpen ? "true" : undefined}>
        <a class="brand-compact" href="/admin/overview" onClick={(event) => handleLink(event, "/admin/overview")}>
          <span class="brand-mark" aria-hidden="true">k</span>
          <strong>kekeio</strong>
        </a>
        <button
          ref={navToggleRef}
          class="icon-button nav-toggle"
          type="button"
          aria-label={navOpen ? "关闭导航" : "打开导航"}
          aria-expanded={navOpen}
          aria-controls="admin-sidebar"
          onClick={() => setNavOpen((value) => !value)}
        >
          {navOpen ? <X aria-hidden="true" /> : <Menu aria-hidden="true" />}
        </button>
      </header>

      {navOpen ? <button class="nav-scrim" type="button" tabIndex={-1} aria-hidden="true" aria-label="关闭导航" onClick={() => setNavOpen(false)} /> : null}
      <aside ref={sidebarRef} id="admin-sidebar" class="admin-sidebar" aria-label="后台导航抽屉" inert={drawerMode && !navOpen} aria-hidden={drawerMode && !navOpen ? "true" : undefined}>
        <div class="brand-block">
          <span class="brand-mark" aria-hidden="true">k</span>
          <div>
            <strong>kekeio</strong>
            <span>运维工作台</span>
          </div>
          <button class="icon-button sidebar-close" type="button" aria-label="关闭导航" onClick={() => setNavOpen(false)}><X aria-hidden="true" /></button>
        </div>
        <nav class="primary-nav" aria-label="后台主导航">
          {domains.map(({ label, href, icon: Icon, pages }) => {
            const active = pages.includes(route.page);
            return (
              <a key={href} href={href} aria-current={active ? "page" : undefined} onClick={(event) => handleLink(event, href)}>
                <Icon size={18} aria-hidden="true" />
                <span>{label}</span>
              </a>
            );
          })}
        </nav>
        <div class="sidebar-session">
          <span>当前管理员</span>
          <strong>{user.displayName || user.email}</strong>
          <small>{user.email}</small>
          <button class="button secondary full-width" type="button" onClick={onLogout}>退出登录</button>
        </div>
      </aside>

      <main id="main-content" class="admin-main" tabIndex={-1} inert={drawerMode && navOpen} aria-hidden={drawerMode && navOpen ? "true" : undefined}>
        {children}
      </main>
    </div>
  );
}
