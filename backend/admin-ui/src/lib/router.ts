export type ContentType = "official" | "web" | "styles";

export type PageId =
  | "install"
  | "login"
  | "overview"
  | "users"
  | "user-detail"
  | "sync-attempts"
  | "sync-conflicts"
  | "content-list"
  | "content-detail"
  | "releases"
  | "admin-audit"
  | "access-audit"
  | "security"
  | "maintenance"
  | "backups"
  | "system"
  | "not-found";

export interface AdminRoute {
  page: PageId;
  pathname: string;
  search: URLSearchParams;
  id?: string;
  contentType?: ContentType;
}

export interface ListState {
  query: string;
  status: string;
  sort: string;
  cursor: string;
}

const contentTypes = new Set<ContentType>(["official", "web", "styles"]);

function asUrl(value: string | URL): URL {
  if (value instanceof URL) return value;
  return new URL(value, "https://fullpro.local");
}

function decodeSegment(value: string | undefined): string | undefined {
  if (!value) return undefined;
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

export function parseAdminLocation(value: string | URL): AdminRoute {
  const url = asUrl(value);
  const pathname = url.pathname.replace(/\/$/, "") || "/";
  const segments = pathname.split("/").filter(Boolean);
  const base = { pathname, search: url.searchParams };

  if (pathname === "/install") return { ...base, page: "install" };
  if (pathname === "/admin" || pathname === "/admin/overview") return { ...base, page: "overview" };
  if (pathname === "/admin/login") return { ...base, page: "login" };
  if (pathname === "/admin/users") return { ...base, page: "users" };
  if (segments[0] === "admin" && segments[1] === "users" && segments[2]) {
    return { ...base, page: "user-detail", id: decodeSegment(segments[2]) };
  }
  if (pathname === "/admin/sync/attempts") return { ...base, page: "sync-attempts" };
  if (pathname === "/admin/sync/conflicts") return { ...base, page: "sync-conflicts" };
  if (segments[0] === "admin" && segments[1] === "content" && contentTypes.has(segments[2] as ContentType)) {
    const contentType = segments[2] as ContentType;
    const id = decodeSegment(segments[3]);
    return id
      ? { ...base, page: "content-detail", contentType, id }
      : { ...base, page: "content-list", contentType };
  }
  if (pathname === "/admin/releases") return { ...base, page: "releases" };
  if (pathname === "/admin/audit/admin") return { ...base, page: "admin-audit" };
  if (pathname === "/admin/audit/access") return { ...base, page: "access-audit" };
  if (pathname === "/admin/security") return { ...base, page: "security" };
  if (pathname === "/admin/maintenance") return { ...base, page: "maintenance" };
  if (pathname === "/admin/backups") return { ...base, page: "backups" };
  if (pathname === "/admin/system") return { ...base, page: "system" };
  return { ...base, page: "not-found" };
}

export function readListState(search: URLSearchParams): ListState {
  return {
    query: search.get("q") ?? "",
    status: search.get("status") ?? "",
    sort: search.get("sort") ?? "",
    cursor: search.get("cursor") ?? ""
  };
}

export function buildListHref(pathname: string, state: Partial<ListState>): string {
  const search = new URLSearchParams();
  if (state.query) search.set("q", state.query);
  if (state.status) search.set("status", state.status);
  if (state.sort) search.set("sort", state.sort);
  if (state.cursor) search.set("cursor", state.cursor);
  const query = search.toString();
  return `${pathname}${query ? `?${query}` : ""}`;
}

export function buildDetailHref(contentType: ContentType, id: string, returnTo: string): string {
  const search = new URLSearchParams({ returnTo });
  return `/admin/content/${contentType}/${encodeURIComponent(id)}?${search.toString()}`;
}

export function getReturnHref(search: URLSearchParams, fallback: string): string {
  const candidate = search.get("returnTo");
  if (!candidate || !candidate.startsWith("/admin/") || candidate.startsWith("//")) return fallback;
  return candidate;
}

function scrollKey(href: string): string {
  return `fullpro:admin:scroll:${href}`;
}

export function saveScrollPosition(href: string, scrollY: number): void {
  window.sessionStorage.setItem(scrollKey(href), String(Math.max(0, Math.round(scrollY))));
}

export function takeScrollPosition(href: string): number | null {
  const key = scrollKey(href);
  const raw = window.sessionStorage.getItem(key);
  window.sessionStorage.removeItem(key);
  if (raw === null) return null;
  const value = Number(raw);
  return Number.isFinite(value) ? value : null;
}
