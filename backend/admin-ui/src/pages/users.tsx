import { useEffect, useState } from "preact/hooks";
import { ArrowLeft, Ban, History, KeyRound, MonitorSmartphone, Search, ShieldOff, UserRound } from "lucide-preact";
import type { ApiClient } from "../lib/api";
import type { AdminRoute } from "../lib/router";
import { buildListHref, getReturnHref, readListState } from "../lib/router";
import { ConfirmDialog } from "../components/common";
import { EmptyState, ExpandButton, InlineError, PageHeading, RouteSkeleton, StatusBadge, useRouteData } from "../components/page";

interface UserListItem { id: string; email: string; displayName?: string; status: string; verificationStatus?: string; lastActivityAt?: string; deviceCount?: number; browserCount?: number; browserFamilies?: string[]; createdAt?: string }
interface UserListPayload { items: UserListItem[]; nextCursor?: string }
interface BrowserSession { id: string; deviceId?: string; deviceName?: string; browserFamily?: string; browserVersion?: string; createdAt?: string; lastUsedAt?: string }

export function UsersPage({ client, route, onNavigate }: { client: ApiClient; route: AdminRoute; onNavigate: (href: string) => void }) {
  const listState = readListState(route.search);
  const [filters, setFilters] = useState(listState);
  const [expanded, setExpanded] = useState<string | null>(null);
  const query = route.search.toString();
  const resource = useRouteData(
    async (signal) => {
      const path = `/api/admin/v1/users${query ? `?${query}` : ""}`;
      const value = await client.getWithLegacy<UserListPayload | UserListItem[]>(path, "/api/admin/users", signal);
      return Array.isArray(value) ? { items: value } : value;
    },
    [client, query]
  );
  const currentHref = `${route.pathname}${query ? `?${query}` : ""}`;
  useEffect(() => setFilters(listState), [query]);

  const applyFilters = (event: Event) => {
    event.preventDefault();
    onNavigate(buildListHref("/admin/users", { query: filters.query, status: filters.status, sort: filters.sort || "-lastActivityAt" }));
  };

  return <>
    <PageHeading eyebrow="用户与同步" title="用户" description="搜索账号、查看验证状态和活跃浏览器。完整配置默认保持隐藏。" />
    <nav class="subnav" aria-label="用户与同步二级导航"><a href="/admin/users" aria-current="page">用户</a><a href="/admin/sync/attempts" onClick={(e) => { e.preventDefault(); onNavigate("/admin/sync/attempts"); }}>同步尝试</a><a href="/admin/sync/conflicts" onClick={(e) => { e.preventDefault(); onNavigate("/admin/sync/conflicts"); }}>冲突</a></nav>
    <form class="filter-bar" onSubmit={applyFilters}>
      <div class="search-field"><Search size={17} aria-hidden="true" /><label class="sr-only" htmlFor="user-search">搜索用户</label><input id="user-search" name="q" type="search" value={filters.query} onInput={(e) => setFilters((value) => ({ ...value, query: e.currentTarget.value }))} placeholder="邮箱或显示名" /></div>
      <label htmlFor="user-status">账号状态</label><select id="user-status" name="status" value={filters.status} onChange={(e) => setFilters((value) => ({ ...value, status: e.currentTarget.value }))}><option value="">全部状态</option><option value="active">活跃</option><option value="pending_verification">待验证</option><option value="legacy_unverified">旧账号待验证</option><option value="suspended">暂停</option><option value="banned">封禁</option></select>
      <label htmlFor="user-sort">排序</label><select id="user-sort" name="sort" value={filters.sort || "-lastActivityAt"} onChange={(e) => setFilters((value) => ({ ...value, sort: e.currentTarget.value }))}><option value="-lastActivityAt">最近活动</option><option value="email">邮箱 A–Z</option><option value="-createdAt">最新注册</option></select>
      <button class="button secondary" type="submit">应用筛选</button>
    </form>
    {resource.loading && !resource.data ? <RouteSkeleton label="正在加载用户" /> : null}
    {resource.error ? <InlineError error={resource.error} onRetry={resource.retry} /> : null}
    {resource.data ? resource.data.items.length ? <div class="data-surface"><table class="responsive-table"><thead><tr><th scope="col">用户</th><th scope="col">状态</th><th scope="col" class="optional-column">最后活动</th><th scope="col" class="optional-column">活跃浏览器</th><th scope="col"><span class="sr-only">操作</span></th></tr></thead><tbody>{resource.data.items.map((item) => {
      const detailId = `user-extra-${item.id}`;
      const href = `/admin/users/${encodeURIComponent(item.id)}?${new URLSearchParams({ returnTo: currentHref })}`;
      const isExpanded = expanded === item.id;
      const browserCount = item.browserCount;
      const browserCountLabel = browserCount === undefined ? "浏览器数量未知" : `${browserCount} 个浏览器`;
      return <><tr key={item.id}><td><div class="identity-cell"><span class="avatar" aria-hidden="true"><UserRound size={17} /></span><div><a class="primary-link" href={href} aria-label={`查看 ${item.email}`} onClick={(event) => { event.preventDefault(); onNavigate(href); }}>{item.email}</a><small>{item.displayName || item.verificationStatus || "插件用户"}</small></div></div></td><td><StatusBadge value={item.status} /></td><td class="optional-column">{formatDate(item.lastActivityAt)}</td><td class="optional-column"><span class="browser-cell"><strong>{browserCountLabel}</strong><small>{formatBrowserFamilies(item.browserFamilies, browserCount)}</small></span></td><td><ExpandButton expanded={isExpanded} controls={detailId} onClick={() => setExpanded(isExpanded ? null : item.id)} label={`${item.email} 的更多字段`} /></td></tr>{isExpanded ? <tr id={detailId} class="expanded-row"><td colSpan={5}><dl><div><dt>验证状态</dt><dd>{item.verificationStatus || "—"}</dd></div><div><dt>创建时间</dt><dd>{formatDate(item.createdAt)}</dd></div><div><dt>活跃浏览器</dt><dd>{browserCount === undefined ? "未知" : `${browserCount} 个`}</dd></div><div><dt>浏览器类型</dt><dd>{formatBrowserFamilies(item.browserFamilies, browserCount)}</dd></div><div><dt>参与写同步的设备</dt><dd>{item.deviceCount ?? 0} 个</dd></div></dl></td></tr> : null}</>;
    })}</tbody></table><div class="pagination"><span>本页 {resource.data.items.length} 个用户</span>{resource.data.nextCursor ? <button class="button secondary" type="button" onClick={() => onNavigate(buildListHref("/admin/users", { ...listState, cursor: resource.data?.nextCursor }))}>下一页</button> : null}</div></div> : <EmptyState title="还没有插件用户" description="用户完成邮箱验证并首次登录后会显示在这里。" /> : null}
  </>;
}

interface ProfileVersionItem { id: string; version: number; createdAt?: string; summary?: { groups?: number; shortcuts?: number; wallpaper?: string; styleId?: string }; changes?: { currentVersion?: number; groupsDelta?: number; shortcutsDelta?: number; wallpaperChanged?: boolean; styleChanged?: boolean } }
interface UserDetailData { user: UserListItem; sessions?: BrowserSession[]; profile?: { version?: number; schemaVersion?: number; bytes?: number; groups?: number; shortcuts?: number }; attempts?: Array<{ id: string; status: string; code?: string; requestId?: string; createdAt?: string }>; versions?: ProfileVersionItem[] }

export function UserDetailPage({ client, route, onNavigate, notify }: { client: ApiClient; route: AdminRoute; onNavigate: (href: string) => void; notify: (message: string, tone?: string) => void }) {
  const id = route.id || "";
  const returnTo = getReturnHref(route.search, "/admin/users");
  const resource = useRouteData((signal) => client.get<UserDetailData>(`/api/admin/v1/users/${encodeURIComponent(id)}`, signal), [client, id]);
  const [confirm, setConfirm] = useState<{ action: "suspend" | "ban" | "revoke" | "restore"; target?: string; version?: ProfileVersionItem } | null>(null);
  const [busy, setBusy] = useState(false);

  const runAction = async () => {
    if (!confirm) return;
    setBusy(true);
    try {
      if (confirm.action === "restore") await client.post(`/api/admin/v1/users/${encodeURIComponent(id)}/versions/${encodeURIComponent(confirm.version?.id || "")}/restore`, {});
      else if (confirm.action === "revoke") await client.post(`/api/admin/v1/users/${encodeURIComponent(id)}/sessions/${encodeURIComponent(confirm.target || "")}/revoke`, {});
      else await client.post(`/api/admin/v1/users/${encodeURIComponent(id)}/status`, { status: confirm.action === "ban" ? "banned" : "suspended" });
      notify("操作已完成"); setConfirm(null); resource.retry();
    } catch (error) { notify(error instanceof Error ? error.message : "操作失败", "error"); }
    finally { setBusy(false); }
  };

  if (resource.loading && !resource.data) return <RouteSkeleton label="正在加载用户详情" />;
  if (resource.error) return <InlineError error={resource.error} onRetry={resource.retry} />;
  if (!resource.data) return null;
  const data = resource.data;
  return <>
    <button class="back-link" type="button" onClick={() => onNavigate(returnTo)}><ArrowLeft size={17} aria-hidden="true" />返回用户列表</button>
    <PageHeading eyebrow="用户详情" title={data.user.email} description="默认只展示诊断摘要，不展开私人网址或完整 profile。" actions={<><button class="button secondary" type="button" onClick={() => setConfirm({ action: "suspend" })}><ShieldOff size={16} aria-hidden="true" />暂停账号</button><button class="button danger" type="button" onClick={() => setConfirm({ action: "ban" })}><Ban size={16} aria-hidden="true" />封禁账号</button></>} />
    <div class="detail-sections"><section><h2>账号与配置</h2><dl class="definition-grid"><div><dt>状态</dt><dd><StatusBadge value={data.user.status} /></dd></div><div><dt>验证</dt><dd>{data.user.verificationStatus || "—"}</dd></div><div><dt>活跃浏览器</dt><dd>{data.user.browserCount ?? countDistinctBrowsers(data.sessions)} 个</dd></div><div><dt>浏览器类型</dt><dd>{formatBrowserFamilies(data.user.browserFamilies, data.user.browserCount ?? countDistinctBrowsers(data.sessions))}</dd></div><div><dt>当前版本</dt><dd>{data.profile?.version ?? "—"}</dd></div><div><dt>Schema</dt><dd>{data.profile?.schemaVersion ?? "—"}</dd></div><div><dt>Profile 大小</dt><dd>{formatBytes(data.profile?.bytes)}</dd></div><div><dt>分组 / 快捷方式</dt><dd>{data.profile?.groups ?? 0} / {data.profile?.shortcuts ?? 0}</dd></div></dl></section><section><h2><MonitorSmartphone size={18} aria-hidden="true" />活跃浏览器会话</h2>{data.sessions?.length ? <ul class="action-list">{data.sessions.map((session) => { const browserTitle = formatBrowserSession(session); const deviceLabel = formatDeviceLabel(session.deviceId || session.deviceName); return <li key={session.id}><div class="browser-session"><strong>{browserTitle}</strong><p title={session.deviceId || session.deviceName}>{deviceLabel} · 最近认证 {formatDate(session.lastUsedAt)}</p></div><button class="button secondary" type="button" aria-label={`撤销 ${browserTitle} 的会话`} onClick={() => setConfirm({ action: "revoke", target: session.id })}>撤销会话</button></li>; })}</ul> : <EmptyState title="没有活跃浏览器会话" description="用户下次登录后会创建新会话。" />}</section><section><h2><KeyRound size={18} aria-hidden="true" />最近同步</h2>{data.attempts?.length ? <ul class="event-list">{data.attempts.map((attempt) => <li key={attempt.id}><StatusBadge value={attempt.status} /><div><strong>{attempt.code || "同步完成"}</strong><p>{formatDate(attempt.createdAt)} · request {attempt.requestId || "—"}</p></div></li>)}</ul> : <EmptyState title="没有同步尝试" description="浏览器首次写入云端后会显示诊断记录。" />}</section><section><h2><History size={18} aria-hidden="true" />配置版本</h2>{data.versions?.length ? <ul class="action-list">{data.versions.map((version) => <li key={version.id}><div><strong>版本 {version.version}</strong><p>{formatVersionStructure(version)}</p><p>{formatVersionChanges(version)}</p></div><button class="button secondary" type="button" aria-label={`恢复版本 ${version.version}`} onClick={() => setConfirm({ action: "restore", version })}>恢复此版本</button></li>)}</ul> : <EmptyState title="没有历史版本" description="服务器确认首次配置后会产生版本。" />}</section></div>
    <ConfirmDialog open={Boolean(confirm)} title={confirm?.action === "restore" ? `恢复配置版本 ${confirm.version?.version ?? ""}` : confirm?.action === "revoke" ? "撤销设备会话" : confirm?.action === "ban" ? "封禁用户" : "暂停用户"} description={confirm?.action === "restore" ? <div><p>{formatVersionStructure(confirm.version)}</p><p>{formatVersionChanges(confirm.version)}</p><p>该历史版本会复制为新的当前版本；现有历史不会被覆盖，并会写入管理员审计。</p></div> : "此操作会写入管理员审计。继续前请确认目标无误。"} confirmLabel="确认操作" busy={busy} onCancel={() => setConfirm(null)} onConfirm={runAction} />
  </>;
}

function formatDate(value?: string) { if (!value) return "—"; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN"); }
function formatBytes(value?: number) { if (value === undefined) return "—"; return value < 1024 ? `${value} B` : `${(value / 1024).toFixed(1)} KiB`; }
function formatVersionStructure(version?: ProfileVersionItem) { if (!version?.summary) return formatDate(version?.createdAt); return `${version.summary.groups ?? 0} 个分组 · ${version.summary.shortcuts ?? 0} 个快捷方式`; }
function formatVersionChanges(version?: ProfileVersionItem) { if (!version?.changes) return "没有可用的变化摘要"; const details = [`分组 ${formatDelta(version.changes.groupsDelta)}`, `快捷方式 ${formatDelta(version.changes.shortcutsDelta)}`]; if (version.changes.wallpaperChanged) details.push("壁纸已变化"); if (version.changes.styleChanged) details.push("风格已变化"); return `相对当前版本 ${version.changes.currentVersion ?? "—"}：${details.join("，")}`; }
function formatDelta(value?: number) { const next = value ?? 0; return next > 0 ? `+${next}` : String(next); }
const browserLabels: Record<string, string> = { chrome: "Google Chrome", edge: "Microsoft Edge", firefox: "Mozilla Firefox", opera: "Opera", safari: "Safari", chromium: "Chromium", vivaldi: "Vivaldi", yandex: "Yandex Browser", "samsung-internet": "Samsung Internet" };
function formatBrowserName(family?: string) { return family ? browserLabels[family] || "未知浏览器" : "未知浏览器"; }
function formatBrowserFamilies(families: string[] | undefined, count?: number) { if (count === 0) return "暂无活跃会话"; const labels = Array.from(new Set((families || []).map(formatBrowserName))); return labels.length ? labels.join("、") : "浏览器信息未知"; }
function formatBrowserSession(session: BrowserSession) { const name = formatBrowserName(session.browserFamily); const version = session.browserVersion?.trim(); return name === "未知浏览器" || !version ? name : `${name} ${version}`; }
function formatDeviceLabel(value?: string) { if (!value) return "设备标识未知"; return value.length <= 28 ? `设备 ${value}` : `设备 ${value.slice(0, 16)}…${value.slice(-6)}`; }
function countDistinctBrowsers(sessions?: BrowserSession[]) { return new Set((sessions || []).map((session) => session.deviceId || session.deviceName || session.id)).size; }
