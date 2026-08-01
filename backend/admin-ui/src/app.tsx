import { useEffect, useMemo, useState } from "preact/hooks";
import type { ApiClient } from "./lib/api";
import { ApiError, apiClient } from "./lib/api";
import type { AdminRoute } from "./lib/router";
import { parseAdminLocation } from "./lib/router";
import { AdminShell } from "./components/shell";
import { RouteSkeleton } from "./components/page";
import { InstallWizard, LoginPage, type AdminSession } from "./pages/auth";
import { ContentDetailPage, ContentListPage } from "./pages/content";
import { OverviewPage } from "./pages/overview";
import { AuditPage, BackupsPage, ReleasesPage, SyncPage, SystemAreaPage } from "./pages/operations";
import { UserDetailPage, UsersPage } from "./pages/users";

interface SessionProbe {
  authenticated?: boolean;
  user?: AdminSession["user"];
  csrfToken?: string;
}

interface ToastState { id: number; message: string; tone: "status" | "error" }

function routeHref(route: AdminRoute): string {
  const query = route.search.toString();
  return `${route.pathname}${query ? `?${query}` : ""}`;
}

function loginHref(returnTo: string): string {
  return `/admin/login?${new URLSearchParams({ returnTo }).toString()}`;
}

function loginReturnTarget(search: URLSearchParams): string {
  const candidate = search.get("returnTo") || "";
  if (!candidate.startsWith("/admin/") || candidate.startsWith("//")) return "/admin/overview";
  const parsed = parseAdminLocation(candidate);
  return parsed.page === "login" || parsed.page === "install" || parsed.page === "not-found" ? "/admin/overview" : candidate;
}

export function App({ client = apiClient }: { client?: ApiClient }) {
  const [route, setRoute] = useState<AdminRoute>(() => parseAdminLocation(window.location.href));
  const [session, setSession] = useState<AdminSession | null>(null);
  const [preAuthCsrf, setPreAuthCsrf] = useState("");
  const [sessionChecked, setSessionChecked] = useState(false);
  const [sessionError, setSessionError] = useState<string | null>(null);
  const [sessionAttempt, setSessionAttempt] = useState(0);
  const [toast, setToast] = useState<ToastState | null>(null);

  const navigate = (href: string, replace = false) => {
    if (replace) window.history.replaceState(null, "", href);
    else if (`${window.location.pathname}${window.location.search}` !== href) window.history.pushState(null, "", href);
    setRoute(parseAdminLocation(window.location.href));
  };

  useEffect(() => {
    const onPopState = () => setRoute(parseAdminLocation(window.location.href));
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  useEffect(() => {
    if (typeof client.onUnauthorized !== "function") return;
    return client.onUnauthorized(() => {
      const current = parseAdminLocation(window.location.href);
      if (current.page === "install") return;
      const destination = current.page === "login" ? "/admin/login" : loginHref(routeHref(current));
      client.setCsrfToken(null);
      setSession(null);
      setPreAuthCsrf("");
      setSessionError(null);
      setSessionChecked(false);
      setSessionAttempt((attempt) => attempt + 1);
      window.history.replaceState(null, "", destination);
      setRoute(parseAdminLocation(window.location.href));
      setToast({ id: Date.now(), message: "管理员会话已失效，请重新登录", tone: "error" });
    });
  }, [client]);

  useEffect(() => {
    if (route.page === "install" || sessionChecked) return;
    let active = true;
    const probeSession = async () => {
      try {
        let probe: SessionProbe;
        try {
          probe = await client.get<SessionProbe>("/api/admin/v1/auth/session");
        } catch (error) {
          if (!(error instanceof ApiError) || error.status !== 401) throw error;
          client.setCsrfToken(null);
          probe = await client.get<SessionProbe>("/api/admin/v1/auth/session");
        }
        if (!active) return;
        const csrfToken = probe.csrfToken?.trim() || "";
        if (!csrfToken) throw new Error("服务器未提供管理员登录所需的 CSRF 令牌");
        setSessionError(null);
        setPreAuthCsrf(csrfToken);
        client.setCsrfToken(csrfToken);
        if (probe.authenticated && probe.user) {
          setSession({ user: probe.user, csrfToken });
          setSessionChecked(true);
          if (route.page === "login") navigate(loginReturnTarget(route.search), true);
          return;
        }
        setSession(null);
        setSessionChecked(true);
        if (route.page !== "login") navigate(loginHref(routeHref(route)), true);
      } catch (error) {
        if (!active) return;
        const message = error instanceof Error ? error.message : "无法确认管理员会话";
        setSessionError(message);
        setSessionChecked(true);
        notify(message, "error");
      }
    };
    void probeSession();
    return () => { active = false; };
  }, [client, route.page, route.pathname, route.search.toString(), sessionAttempt, sessionChecked]);

  useEffect(() => {
    if (!sessionError || route.page === "install") return;
    const timer = window.setTimeout(() => {
      setSessionError(null);
      setSessionChecked(false);
      setSessionAttempt((attempt) => attempt + 1);
    }, 3000);
    return () => window.clearTimeout(timer);
  }, [route.page, sessionError]);

  useEffect(() => {
    if (!session || route.page === "login" || route.page === "install") return;
    const root = document.querySelector<HTMLElement>("#main-content");
    if (!root) return;
    let observer: MutationObserver | null = null;
    let timeout = 0;
    const focusHeading = () => {
      const heading = root.querySelector<HTMLElement>("h1");
      if (!heading) return false;
      heading.focus();
      observer?.disconnect();
      if (timeout) window.clearTimeout(timeout);
      return true;
    };
    const frame = window.requestAnimationFrame(() => {
      if (focusHeading()) return;
      observer = new MutationObserver(() => { focusHeading(); });
      observer.observe(root, { childList: true, subtree: true });
      focusHeading();
      timeout = window.setTimeout(() => observer?.disconnect(), 5000);
    });
    return () => {
      window.cancelAnimationFrame(frame);
      observer?.disconnect();
      if (timeout) window.clearTimeout(timeout);
    };
  }, [route.pathname, route.search.toString(), session]);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast((current) => current?.id === toast.id ? null : current), 4200);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const notify = (message: string, tone = "status") => {
    setToast({ id: Date.now(), message, tone: tone === "error" ? "error" : "status" });
  };

  const authenticated = (next: AdminSession) => {
    setSessionError(null);
    setSession(next);
    client.setCsrfToken(next.csrfToken);
    setSessionChecked(true);
    navigate(loginReturnTarget(route.search), true);
  };

  const logout = async () => {
    try { await client.post("/api/admin/v1/auth/logout", {}); }
    catch { /* Local session is cleared even if the server is already unavailable. */ }
    client.setCsrfToken(null);
    setSession(null);
    setPreAuthCsrf("");
    setSessionError(null);
    setSessionChecked(false);
    setSessionAttempt((attempt) => attempt + 1);
    navigate("/admin/login", true);
    notify("已退出管理员会话");
  };

  const retrySessionProbe = () => {
    setSessionError(null);
    setSessionChecked(false);
    setSessionAttempt((attempt) => attempt + 1);
  };

  const content = useMemo(() => {
    if (route.page === "install") return <InstallWizard client={client} onInstalled={() => navigate("/admin/login", true)} />;
    if (sessionError) return <SessionProbeFailure message={sessionError} onRetry={retrySessionProbe} />;
    if (!sessionChecked) return <main class="route-loading"><RouteSkeleton label="正在确认管理员会话" /></main>;
    if (route.page === "login" || !session) return preAuthCsrf
      ? <LoginPage client={client} preAuthCsrf={preAuthCsrf} onAuthenticated={authenticated} />
      : <SessionProbeFailure message="尚未取得管理员登录所需的 CSRF 令牌" onRetry={retrySessionProbe} />;
    return <AdminShell route={route} user={session.user} onNavigate={navigate} onLogout={logout}>
      <RouteView route={route} client={client} navigate={navigate} notify={notify} />
    </AdminShell>;
  }, [client, preAuthCsrf, route, session, sessionChecked, sessionError]);

  return <>{content}{toast ? <div class={`toast ${toast.tone}`} role={toast.tone === "error" ? "alert" : "status"} aria-live={toast.tone === "error" ? "assertive" : "polite"}>{toast.message}</div> : null}</>;
}

function SessionProbeFailure({ message, onRetry }: { message: string; onRetry: () => void }) {
  return <main class="route-loading"><section class="inline-alert error session-probe-error" role="alert" aria-labelledby="session-probe-error-title">
    <h1 id="session-probe-error-title" tabIndex={-1}>暂时无法确认管理员会话</h1>
    <p>{message}</p>
    <p>当前地址已保留；连接恢复后可重试，页面也会自动再次检查。</p>
    <button class="button primary" type="button" onClick={onRetry}>重新检查管理员会话</button>
  </section></main>;
}

function RouteView({ route, client, navigate, notify }: { route: AdminRoute; client: ApiClient; navigate: (href: string) => void; notify: (message: string, tone?: string) => void }) {
  switch (route.page) {
    case "overview": return <OverviewPage client={client} onNavigate={navigate} />;
    case "users": return <UsersPage client={client} route={route} onNavigate={navigate} />;
    case "user-detail": return <UserDetailPage client={client} route={route} onNavigate={navigate} notify={notify} />;
    case "sync-attempts":
    case "sync-conflicts": return <SyncPage client={client} route={route} onNavigate={navigate} />;
    case "content-list": return <ContentListPage client={client} route={route} onNavigate={navigate} />;
    case "content-detail": return <ContentDetailPage client={client} route={route} onNavigate={navigate} notify={notify} />;
    case "releases": return <ReleasesPage client={client} notify={notify} />;
    case "admin-audit":
    case "access-audit": return <AuditPage client={client} route={route} onNavigate={navigate} />;
    case "backups": return <BackupsPage client={client} notify={notify} />;
    case "security":
    case "maintenance":
    case "system": return <SystemAreaPage client={client} route={route} notify={notify} />;
    default: return <section class="not-found"><p class="section-label">404</p><h1>找不到这个后台页面</h1><p>该地址不属于 kekeio 的 canonical routes。</p><button class="button primary" type="button" onClick={() => navigate("/admin/overview")}>返回概览</button></section>;
  }
}
