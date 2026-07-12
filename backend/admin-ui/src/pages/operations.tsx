import { useEffect, useRef, useState } from "preact/hooks";
import { ArchiveRestore, Download, FileClock, HardDrive, Play, Plus, Search, Send, ShieldBan, ShieldCheck, Wrench } from "lucide-preact";
import type { ApiClient } from "../lib/api";
import type { AdminRoute, PageId } from "../lib/router";
import { buildListHref, readListState } from "../lib/router";
import { detectSMTPProvider, getSMTPPreset, smtpProviderOptions, type SMTPProviderId } from "../lib/smtpPresets";
import { ConfirmDialog, FormErrorSummary } from "../components/common";
import { EmptyState, InlineError, PageHeading, RouteSkeleton, StatusBadge, useRouteData } from "../components/page";

interface OperationConfig { title: string; eyebrow: string; description: string; endpoint: string }

const configs: Partial<Record<PageId, OperationConfig>> = {
  "sync-attempts": { title: "同步尝试", eyebrow: "用户与同步", description: "按请求、账号和结果定位失败；这里不展示完整 profile。", endpoint: "/api/admin/v1/sync/attempts" },
  "sync-conflicts": { title: "同步冲突", eyebrow: "用户与同步", description: "查看冲突元数据、版本和 hash；配置内容默认保持隐藏。", endpoint: "/api/admin/v1/sync/conflicts" },
  "admin-audit": { title: "管理员审计", eyebrow: "发布与审计", description: "不可变的管理员状态变更记录，包含操作原因与 request ID。", endpoint: "/api/admin/v1/audit/admin" },
  "access-audit": { title: "HTTP 访问日志", eyebrow: "发布与审计", description: "按采样和保留策略记录请求，不保存 token、私人 URL 或完整 profile。", endpoint: "/api/admin/v1/audit/access" },
  security: { title: "安全设置", eyebrow: "安全与维护", description: "管理注册、来源、配额和保留；启动级网络配置保持只读。", endpoint: "/api/admin/v1/system/settings" },
  maintenance: { title: "维护任务", eyebrow: "安全与维护", description: "查看清理、checkpoint 和容量维护任务及失败原因。", endpoint: "/api/admin/v1/system/maintenance/jobs" },
  backups: { title: "备份与恢复", eyebrow: "安全与维护", description: "创建带 manifest 与校验和的备份；恢复会进入维护模式并正常退出进程。", endpoint: "/api/admin/v1/system/backups" },
  system: { title: "系统状态", eyebrow: "安全与维护", description: "查看 readiness、存储水位、安装信息和只读启动配置。", endpoint: "/api/admin/v1/system/health" }
};

export function getOperationConfig(page: PageId): OperationConfig {
  return configs[page] || { title: "未知页面", eyebrow: "KeKeIO Tab", description: "这个页面不存在。", endpoint: "" };
}

interface RecordList { items: Array<Record<string, unknown>>; nextCursor?: string }

export function SyncPage({ client, route, onNavigate }: { client: ApiClient; route: AdminRoute; onNavigate: (href: string) => void }) {
  const config = getOperationConfig(route.page);
  const query = route.search.toString();
  const resource = useRouteData<RecordList>((signal) => client.get(`${config.endpoint}${query ? `?${query}` : ""}`, signal), [client, config.endpoint, query]);
  const state = readListState(route.search);
  const apply = (event: Event) => {
    event.preventDefault(); const form = new FormData(event.currentTarget as HTMLFormElement);
    onNavigate(buildListHref(route.pathname, { query: String(form.get("q") || ""), status: String(form.get("status") || ""), sort: String(form.get("sort") || "-createdAt") }));
  };
  const conflicts = route.page === "sync-conflicts";
  return <>
    <PageHeading eyebrow={config.eyebrow} title={config.title} description={config.description} />
    <nav class="subnav" aria-label="用户与同步二级导航"><a href="/admin/users" onClick={(e) => { e.preventDefault(); onNavigate("/admin/users"); }}>用户</a><a href="/admin/sync/attempts" aria-current={!conflicts ? "page" : undefined} onClick={(e) => { e.preventDefault(); onNavigate("/admin/sync/attempts"); }}>同步尝试</a><a href="/admin/sync/conflicts" aria-current={conflicts ? "page" : undefined} onClick={(e) => { e.preventDefault(); onNavigate("/admin/sync/conflicts"); }}>冲突</a></nav>
    <form class="filter-bar" onSubmit={apply}><div class="search-field"><Search size={17} aria-hidden="true" /><label class="sr-only" htmlFor="sync-search">搜索请求、用户或 code</label><input id="sync-search" name="q" type="search" defaultValue={state.query} placeholder="request ID、邮箱或错误 code" /></div><label htmlFor="sync-status">状态</label><select id="sync-status" name="status" defaultValue={state.status}><option value="">全部状态</option><option value="open">待处理</option><option value="success">成功</option><option value="failed">失败</option><option value="resolved">已解决</option></select><input type="hidden" name="sort" value={state.sort || "-createdAt"} /><button class="button secondary" type="submit">应用筛选</button></form>
    {resource.loading && !resource.data ? <RouteSkeleton label={`正在加载${config.title}`} /> : null}
    {resource.error ? <InlineError error={resource.error} onRetry={resource.retry} /> : null}
    {resource.data ? resource.data.items.length ? <div class="data-surface"><table class="responsive-table"><thead><tr>{conflicts ? <><th scope="col">冲突</th><th scope="col">用户</th><th scope="col" class="optional-column">版本</th><th scope="col">状态</th><th scope="col">时间</th></> : <><th scope="col">时间</th><th scope="col">用户</th><th scope="col">结果</th><th scope="col" class="optional-column">耗时</th><th scope="col">Request ID</th></>}</tr></thead><tbody>{resource.data.items.map((item) => conflicts ? <tr key={String(item.id)}><td><strong>{String(item.id || "—")}</strong><code>{String(item.path || item.code || "配置冲突")}</code></td><td>{String(item.userEmail || item.userId || "—")}</td><td class="optional-column">{String(item.baseVersion ?? "—")} → {String(item.currentVersion ?? "—")}</td><td><StatusBadge value={String(item.status || "open")} /></td><td>{formatDate(item.createdAt)}</td></tr> : <tr key={String(item.id)}><td>{formatDate(item.createdAt)}</td><td>{String(item.userEmail || item.userId || "匿名")}</td><td><StatusBadge value={String(item.status || "unknown")} /><code>{String(item.code || "—")}</code></td><td class="optional-column">{item.durationMs === undefined ? "—" : `${String(item.durationMs)} ms`}</td><td><code>{String(item.requestId || "—")}</code></td></tr>)}</tbody></table></div> : <EmptyState title={conflicts ? "没有待处理冲突" : "没有同步尝试"} description={conflicts ? "重叠修改出现时会在这里生成可追踪冲突。" : "设备首次同步后会显示请求结果。"} /> : null}
  </>;
}

interface ReleaseItem { id: string; version: string; channel: string; status: string; notes?: string; createdAt?: string; publishedAt?: string; disabledAt?: string }
type ReleaseAction = { item: ReleaseItem; action: "publish" | "disable" };
interface ReleaseHistoryItem { id: string; action: string; fromStatus: string; toStatus: string; adminEmail?: string; requestId?: string; createdAt?: string }

export function ReleasesPage({ client, notify }: { client: ApiClient; notify: (message: string, tone?: string) => void }) {
  const resource = useRouteData<{ items: ReleaseItem[] }>((signal) => client.get("/api/admin/v1/releases", signal), [client]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [pendingAction, setPendingAction] = useState<ReleaseAction | null>(null);
  const [history, setHistory] = useState<{ release: ReleaseItem; items: ReleaseHistoryItem[]; loading: boolean; error: string } | null>(null);
  const historyRequest = useRef<{ sequence: number; controller: AbortController | null }>({ sequence: 0, controller: null });
  useEffect(() => () => historyRequest.current.controller?.abort(), []);
  const submit = async (event: Event) => {
    event.preventDefault(); const formElement = event.currentTarget as HTMLFormElement; const form = new FormData(formElement); const version = String(form.get("version") || "").trim();
    if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) { setErrors({ releaseVersion: "请输入有效 semver，例如 0.2.0" }); return; }
    setErrors({}); setBusy(true);
    try { await client.post("/api/admin/v1/releases", { version, channel: String(form.get("channel") || "stable"), notes: String(form.get("notes") || ""), downloadUrl: String(form.get("downloadUrl") || ""), minimumVersion: String(form.get("minimumVersion") || ""), status: "draft" }); notify("版本草稿已保存"); formElement.reset(); resource.retry(); }
    catch (error) { setErrors({ form: error instanceof Error ? error.message : "保存失败" }); }
    finally { setBusy(false); }
  };
  const runAction = async () => {
    if (!pendingAction) return;
    const { item, action } = pendingAction;
    setBusy(true);
    try {
      await client.post(`/api/admin/v1/releases/${encodeURIComponent(item.id)}/${action}`, {});
      notify(action === "publish" ? `版本 ${item.version} 已发布` : `版本 ${item.version} 已停用`);
      setPendingAction(null);
      resource.retry();
    } catch (error) {
      notify(error instanceof Error ? error.message : "版本操作失败", "error");
    } finally {
      setBusy(false);
    }
  };
  const loadHistory = async (release: ReleaseItem) => {
    historyRequest.current.controller?.abort();
    const controller = new AbortController();
    const sequence = historyRequest.current.sequence + 1;
    historyRequest.current = { sequence, controller };
    setHistory({ release, items: [], loading: true, error: "" });
    try {
      const result = await client.get<{ items: ReleaseHistoryItem[] }>(`/api/admin/v1/releases/${encodeURIComponent(release.id)}/history`, controller.signal);
      if (controller.signal.aborted || historyRequest.current.sequence !== sequence) return;
      setHistory({ release, items: result.items || [], loading: false, error: "" });
    } catch (error) {
      if (controller.signal.aborted || historyRequest.current.sequence !== sequence) return;
      setHistory({ release, items: [], loading: false, error: error instanceof Error ? error.message : "无法读取版本历史" });
    }
  };
  const closeHistory = () => {
    historyRequest.current.controller?.abort();
    historyRequest.current = { sequence: historyRequest.current.sequence + 1, controller: null };
    setHistory(null);
  };
  const confirmationTitle = pendingAction ? `${pendingAction.action === "publish" ? "发布" : "停用"}版本 ${pendingAction.item.version}` : "版本操作";
  const confirmationDescription = pendingAction?.action === "publish"
    ? `发布后 ${pendingAction.item.channel} 通道客户端将看到此版本公告。此操作会写入管理员审计。`
    : "停用后客户端 bootstrap 将不再返回此版本。此操作会写入管理员审计。";
  return <>
    <PageHeading eyebrow="发布与审计" title="版本发布" description="版本公告先保存为草稿；校验后才能切换 channel 的 active revision。" />
    <ReleaseAuditNav current="releases" />
    <div class="split-workspace"><section><h2>版本记录</h2>{resource.loading && !resource.data ? <RouteSkeleton label="正在加载版本" /> : null}{resource.error ? <InlineError error={resource.error} onRetry={resource.retry} /> : null}{resource.data ? resource.data.items.length ? <ul class="record-list">{resource.data.items.map((item) => <li key={item.id}><div><strong>{item.version}</strong><span>{item.channel}</span><p>{item.notes || "未填写更新说明"}</p></div><div><StatusBadge value={item.status} /><time>{formatDate(item.disabledAt || item.publishedAt || item.createdAt)}</time><button class="button secondary" type="button" aria-label={`查看 ${item.version} 的历史`} onClick={() => void loadHistory(item)}>历史</button>{item.status === "draft" ? <button class="button primary" type="button" aria-label={`发布 ${item.version}`} disabled={busy} onClick={() => setPendingAction({ item, action: "publish" })}><Send size={16} aria-hidden="true" />发布</button> : null}{item.status === "published" ? <button class="button danger" type="button" aria-label={`停用 ${item.version}`} disabled={busy} onClick={() => setPendingAction({ item, action: "disable" })}><ShieldBan size={16} aria-hidden="true" />停用</button> : null}</div></li>)}</ul> : <EmptyState title="还没有版本草稿" description="填写右侧表单创建第一个版本公告。" /> : null}</section><aside class="form-panel"><h2>新建版本草稿</h2><FormErrorSummary errors={errors} focusOnMount /><form class="stack-form" onSubmit={submit} noValidate><label htmlFor="releaseVersion">版本号</label><input id="releaseVersion" name="version" placeholder="0.2.0" /><label htmlFor="releaseChannel">通道</label><select id="releaseChannel" name="channel"><option value="stable">stable</option><option value="beta">beta</option></select><label htmlFor="minimumVersion">最低支持版本</label><input id="minimumVersion" name="minimumVersion" placeholder="0.1.0" /><label htmlFor="downloadUrl">下载 / 商店链接</label><input id="downloadUrl" name="downloadUrl" type="url" /><label htmlFor="releaseNotes">更新说明</label><textarea id="releaseNotes" name="notes" rows={6} /><button class="button primary" type="submit" disabled={busy}><Plus size={16} aria-hidden="true" />保存版本草稿</button></form></aside></div>
    {history ? <section class="release-history" aria-live="polite"><header class="section-heading"><h2>{history.release.version} 版本历史</h2><button class="button secondary" type="button" onClick={closeHistory}>关闭历史</button></header>{history.loading ? <RouteSkeleton label="正在加载版本历史" /> : history.error ? <div class="inline-alert error" role="alert">{history.error}</div> : history.items.length ? <ol class="event-list">{history.items.map((item) => <li key={item.id}><StatusBadge value={item.toStatus} /><div><strong>{item.fromStatus || "创建"} → {item.toStatus}</strong><p>{item.action} · {item.adminEmail || "系统"} · request {item.requestId || "—"} · {formatDate(item.createdAt)}</p></div></li>)}</ol> : <EmptyState title="没有版本历史" description="创建、发布与停用事件会显示在这里。" />}</section> : null}
    <ConfirmDialog open={Boolean(pendingAction)} title={confirmationTitle} description={confirmationDescription} confirmLabel={pendingAction?.action === "publish" ? "确认发布" : "确认停用"} busy={busy} onCancel={() => setPendingAction(null)} onConfirm={runAction} />
  </>;
}

export function AuditPage({ client, route, onNavigate }: { client: ApiClient; route: AdminRoute; onNavigate: (href: string) => void }) {
  const config = getOperationConfig(route.page); const query = route.search.toString();
  const resource = useRouteData<RecordList>((signal) => client.get(`${config.endpoint}${query ? `?${query}` : ""}`, signal), [client, config.endpoint, query]);
  return <>
    <PageHeading eyebrow={config.eyebrow} title={config.title} description={config.description} actions={<a class="button secondary" href={`${config.endpoint}/export${query ? `?${query}` : ""}`}><Download size={16} aria-hidden="true" />导出当前结果</a>} />
    <nav class="subnav" aria-label="发布与审计二级导航"><a href="/admin/releases" onClick={(e) => { e.preventDefault(); onNavigate("/admin/releases"); }}>版本发布</a><a href="/admin/audit/admin" aria-current={route.page === "admin-audit" ? "page" : undefined} onClick={(e) => { e.preventDefault(); onNavigate("/admin/audit/admin"); }}>管理员审计</a><a href="/admin/audit/access" aria-current={route.page === "access-audit" ? "page" : undefined} onClick={(e) => { e.preventDefault(); onNavigate("/admin/audit/access"); }}>HTTP 访问日志</a></nav>
    {resource.loading && !resource.data ? <RouteSkeleton label={`正在加载${config.title}`} /> : null}{resource.error ? <InlineError error={resource.error} onRetry={resource.retry} /> : null}{resource.data ? resource.data.items.length ? <div class="data-surface"><table class="responsive-table"><thead><tr><th scope="col">时间</th><th scope="col">动作 / 路由</th><th scope="col">主体</th><th scope="col" class="optional-column">结果</th><th scope="col">Request ID</th></tr></thead><tbody>{resource.data.items.map((item) => <tr key={String(item.id)}><td>{formatDate(item.createdAt)}</td><td><strong>{String(item.action || `${item.method || ""} ${item.route || item.routeGroup || ""}`)}</strong><code>{String(item.reason || item.path || "—")}</code></td><td>{String(item.adminEmail || item.userEmail || item.ip || "匿名")}</td><td class="optional-column"><StatusBadge value={String(item.status || item.outcome || "unknown")} /></td><td><code>{String(item.requestId || "—")}</code></td></tr>)}</tbody></table></div> : <EmptyState title="当前没有日志" description="采样、保留或管理员操作产生记录后会显示在这里。" /> : null}
  </>;
}

interface BackupItem { id: string; kind: string; status: string; createdAt?: string; sizeBytes?: number; checksum?: string }

export function BackupsPage({ client, notify }: { client: ApiClient; notify: (message: string, tone?: string) => void }) {
  const resource = useRouteData<{ items: BackupItem[] }>((signal) => client.get("/api/admin/v1/system/backups", signal), [client]);
  const [restore, setRestore] = useState<BackupItem | null>(null); const [passphrase, setPassphrase] = useState(""); const [createFull, setCreateFull] = useState(false); const [createPassphrase, setCreatePassphrase] = useState(""); const [createPassphraseConfirm, setCreatePassphraseConfirm] = useState(""); const createPassphraseRef = useRef<HTMLInputElement>(null); const createPassphraseConfirmRef = useRef<HTMLInputElement>(null); const [busy, setBusy] = useState(false);
  const create = async (kind: "full" | "data-only", recoveryPassphrase?: string, recoveryPassphraseConfirm?: string) => { if (kind === "full" && (recoveryPassphrase || "").length < 12) { notify("完整备份恢复口令至少需要 12 个字符", "error"); return; } if (kind === "full" && recoveryPassphrase !== recoveryPassphraseConfirm) { notify("两次输入的备份恢复口令不一致", "error"); return; } setBusy(true); try { await client.post("/api/admin/v1/system/backups", kind === "full" ? { kind, passphrase: recoveryPassphrase } : { kind }); notify("备份任务已创建"); setCreateFull(false); setCreatePassphrase(""); setCreatePassphraseConfirm(""); resource.retry(); } catch (error) { notify(error instanceof Error ? error.message : "无法创建备份", "error"); } finally { setBusy(false); } };
  const runRestore = async () => { if (!restore) return; setBusy(true); try { await client.post(`/api/admin/v1/system/backups/${encodeURIComponent(restore.id)}/restore`, { passphrase: passphrase || undefined }); notify("恢复已启动，服务将进入维护模式"); setRestore(null); } catch (error) { notify(error instanceof Error ? error.message : "无法恢复备份", "error"); } finally { setBusy(false); } };
  return <>
    <PageHeading eyebrow="安全与维护" title="备份与恢复" description={getOperationConfig("backups").description} actions={<><button class="button secondary" type="button" onClick={() => create("data-only")} disabled={busy}>创建 data-only</button><button class="button primary" type="button" onClick={() => setCreateFull(true)} disabled={busy}><HardDrive size={16} aria-hidden="true" />创建完整备份</button></>} />
    <SystemNav current="backups" />
    {resource.loading && !resource.data ? <RouteSkeleton label="正在加载备份" /> : null}{resource.error ? <InlineError error={resource.error} onRetry={resource.retry} /> : null}{resource.data ? resource.data.items.length ? <ul class="backup-list">{resource.data.items.map((item) => <li key={item.id}><FileClock aria-hidden="true" /><div><strong>{item.id}</strong><p>{item.kind} · {formatBytes(item.sizeBytes)} · {formatDate(item.createdAt)}</p><code>{item.checksum || "校验和由 manifest 记录"}</code></div><StatusBadge value={item.status} label={item.status === "ready" ? "可恢复" : undefined} /><button class="button secondary" type="button" aria-label={`恢复 ${item.id}`} onClick={() => setRestore(item)}><ArchiveRestore size={16} aria-hidden="true" />恢复</button></li>)}</ul> : <EmptyState title="还没有可用备份" description="创建完整或 data-only 备份后会显示在这里。" /> : null}
    <ConfirmDialog open={createFull} title="创建加密完整备份" description={<div class="stack-form"><p>完整备份会包含经 Argon2id 与 AES-256-GCM 加密的服务 secrets。恢复口令不会保存，遗失后无法恢复。</p><label htmlFor="create-backup-passphrase">备份恢复口令</label><input ref={createPassphraseRef} id="create-backup-passphrase" type="password" autoComplete="new-password" value={createPassphrase} onInput={(e) => setCreatePassphrase(e.currentTarget.value)} /><label htmlFor="create-backup-passphrase-confirm">再次输入备份恢复口令</label><input ref={createPassphraseConfirmRef} id="create-backup-passphrase-confirm" type="password" autoComplete="new-password" value={createPassphraseConfirm} onInput={(e) => setCreatePassphraseConfirm(e.currentTarget.value)} /></div>} confirmLabel="创建加密备份" busy={busy} onCancel={() => { setCreateFull(false); setCreatePassphrase(""); setCreatePassphraseConfirm(""); }} onConfirm={() => create("full", createPassphraseRef.current?.value || "", createPassphraseConfirmRef.current?.value || "")} />
    <ConfirmDialog open={Boolean(restore)} title="恢复备份" description={<div class="stack-form"><p>恢复会先排空请求和维护任务，再原子替换数据库并正常退出进程。请确认 supervisor 已配置重启策略。</p>{restore?.kind === "full" ? <><label htmlFor="restore-passphrase">恢复口令</label><input id="restore-passphrase" type="password" autoComplete="off" value={passphrase} onInput={(e) => setPassphrase(e.currentTarget.value)} /></> : null}</div>} confirmLabel="确认恢复" busy={busy} onCancel={() => { setRestore(null); setPassphrase(""); }} onConfirm={runRestore} />
  </>;
}

interface SystemPayload { startup?: { listenAddress?: string; dataDirectory?: string; adminCidrs?: string[]; trustedProxies?: string[] }; health?: Array<{ id?: string; label?: string; status?: string; detail?: string }>; settings?: Record<string, unknown>; items?: Array<Record<string, unknown>>; storage?: { databaseBytes?: number; freeBytes?: number; softLimitBytes?: number } }
interface SMTPFormState { provider: SMTPProviderId; host: string; port: string; tls: "tls" | "starttls" | "none"; from: string; username: string; password: string; recipient: string; passwordConfigured: boolean }
const emptySMTP: SMTPFormState = { provider: "custom", host: "", port: "587", tls: "starttls", from: "", username: "", password: "", recipient: "", passwordConfigured: false };

const quotaFields: Array<[string, string]> = [
  ["maxUsers", "最大用户数"],
  ["profileKiB", "单配置 KiB"],
  ["storageGiB", "数据库软水位 GiB"],
  ["versionsPerUser", "每用户保留版本数"],
  ["accessLogDays", "访问日志天数"],
  ["auditLogDays", "审计日志天数"]
];

export function SystemAreaPage({ client, route, notify }: { client: ApiClient; route: AdminRoute; notify: (message: string, tone?: string) => void }) {
  const config = getOperationConfig(route.page); const resource = useRouteData<SystemPayload>((signal) => client.get(config.endpoint, signal), [client, config.endpoint]); const [settings, setSettings] = useState<Record<string, string | boolean>>({}); const [smtp, setSMTP] = useState<SMTPFormState>(emptySMTP); const smtpRef = useRef<SMTPFormState>(emptySMTP); const [smtpVerified, setSMTPVerified] = useState(false); const [busy, setBusy] = useState(false);
  useEffect(() => { if (resource.data?.settings) { setSettings(toEditableSettings(resource.data.settings)); const nextSMTP = toSMTPForm(resource.data.settings.smtp); smtpRef.current = nextSMTP; setSMTP(nextSMTP); setSMTPVerified(Boolean((resource.data.settings.smtp as Record<string, unknown> | null)?.verified)); } }, [resource.data]);
  const patchSMTP = <K extends keyof SMTPFormState>(key: K, value: SMTPFormState[K]) => { const next = { ...smtpRef.current, [key]: value, ...((key === "host" || key === "port" || key === "tls") ? { provider: "custom" as const } : {}) }; smtpRef.current = next; setSMTP(next); if (key !== "recipient" && key !== "provider") setSMTPVerified(false); };
  const chooseSMTPProvider = (provider: SMTPProviderId) => {
    if (provider === "custom") {
      const next = { ...smtpRef.current, provider };
      smtpRef.current = next;
      setSMTP(next);
      return;
    }
    const preset = getSMTPPreset(provider);
    if (!preset) return;
    const next: SMTPFormState = { ...smtpRef.current, provider, host: preset.host, port: preset.port, tls: preset.tls };
    smtpRef.current = next;
    setSMTP(next);
    setSMTPVerified(false);
  };
  const testSMTP = async () => { const snapshot = { ...smtpRef.current }; if (!snapshot.recipient.trim()) { notify("请输入测试收件人", "error"); return; } const testedConfiguration = smtpConfigurationKey(snapshot); setBusy(true); try { await client.post("/api/admin/v1/system/settings/smtp-test", smtpPayload(snapshot, true)); if (smtpConfigurationKey(smtpRef.current) !== testedConfiguration) { setSMTPVerified(false); notify("SMTP 配置在测试期间已修改，请重新测试", "error"); return; } setSMTPVerified(true); notify("测试邮件已发送，当前 SMTP 配置已验证"); } catch (error) { setSMTPVerified(false); notify(error instanceof Error ? error.message : "测试邮件发送失败", "error"); } finally { setBusy(false); } };
  const save = async (event: Event) => { event.preventDefault(); if (Boolean(settings.registrationEnabled) && !smtpVerified) { notify("开放注册前必须使用当前 SMTP 配置成功发送测试邮件", "error"); return; } setBusy(true); try { await client.put("/api/admin/v1/system/settings", editableSettingsPayload(settings, smtp)); notify("安全设置已保存"); resource.retry(); } catch (error) { notify(error instanceof Error ? error.message : "保存失败", "error"); } finally { setBusy(false); } };
  const runMaintenance = async (kind: string) => { setBusy(true); try { await client.post("/api/admin/v1/system/maintenance/jobs", { kind }); notify("维护任务已创建"); resource.retry(); } catch (error) { notify(error instanceof Error ? error.message : "任务创建失败", "error"); } finally { setBusy(false); } };
  return <>
    <PageHeading eyebrow={config.eyebrow} title={config.title} description={config.description} actions={route.page === "maintenance" ? <button class="button primary" type="button" onClick={() => runMaintenance("cleanup")} disabled={busy}><Play size={16} aria-hidden="true" />运行清理</button> : undefined} />
    <SystemNav current={route.page} />
    {resource.loading && !resource.data ? <RouteSkeleton label={`正在加载${config.title}`} /> : null}{resource.error ? <InlineError error={resource.error} onRetry={resource.retry} /> : null}
    {resource.data && route.page === "system" ? <div class="detail-sections"><section><h2><ShieldCheck size={18} aria-hidden="true" />健康与 readiness</h2>{resource.data.health?.length ? <ul class="event-list">{resource.data.health.map((item, index) => <li key={item.id || index}><StatusBadge value={item.status} /><div><strong>{item.label || item.id}</strong><p>{item.detail}</p></div></li>)}</ul> : <EmptyState title="没有健康详情" description="服务接入新版 health detail 后会显示组件状态。" />}</section><section><h2>启动级配置</h2><p class="inline-note">只能通过环境变量或 CLI 修改</p><dl class="definition-grid"><div><dt>监听地址</dt><dd>{resource.data.startup?.listenAddress || "—"}</dd></div><div><dt>数据目录</dt><dd>{resource.data.startup?.dataDirectory || "—"}</dd></div><div><dt>管理员 CIDR</dt><dd>{resource.data.startup?.adminCidrs?.join(", ") || "—"}</dd></div><div><dt>可信代理</dt><dd>{resource.data.startup?.trustedProxies?.join(", ") || "空列表"}</dd></div></dl></section><section><h2>存储水位</h2><dl class="definition-grid"><div><dt>数据库</dt><dd>{formatBytes(resource.data.storage?.databaseBytes)}</dd></div><div><dt>剩余空间</dt><dd>{formatBytes(resource.data.storage?.freeBytes)}</dd></div><div><dt>软水位</dt><dd>{formatBytes(resource.data.storage?.softLimitBytes)}</dd></div></dl></section></div> : null}
    {resource.data && route.page === "security" ? <form class="settings-form" onSubmit={save}><section><h2>注册与来源</h2><label class="switch-row" htmlFor="registration"><span><strong>开放注册</strong><small>只有当前 SMTP 配置已通过测试时后端才会接受开启。</small></span><input id="registration" type="checkbox" checked={Boolean(settings.registrationEnabled)} onChange={(e) => setSettings((v) => ({ ...v, registrationEnabled: e.currentTarget.checked }))} /></label><div class="field-block"><label htmlFor="public-base-url">公网 API URL</label><input id="public-base-url" type="url" placeholder="https://tab.kekeio.com" value={String(settings.publicBaseUrl || "")} onInput={(e) => setSettings((v) => ({ ...v, publicBaseUrl: e.currentTarget.value }))} /></div><div class="field-block"><label htmlFor="allowed-origins">允许的 Web 来源</label><textarea id="allowed-origins" rows={4} value={String(settings.webOrigins || "")} onInput={(e) => setSettings((v) => ({ ...v, webOrigins: e.currentTarget.value }))} /></div></section><section><div class="section-heading"><div><h2>SMTP 邮件服务</h2><p class="muted-copy">密码不会回显；留空会保留已保存的密码。修改任何 SMTP 字段后必须重新测试。</p></div><StatusBadge value={smtpVerified ? "verified" : "warning"} label={smtpVerified ? "已验证" : "待测试"} /></div><div class="form-grid two-columns"><div class="field-block"><label htmlFor="smtp-provider">邮箱服务商</label><select id="smtp-provider" value={smtp.provider} onChange={(e) => chooseSMTPProvider(e.currentTarget.value as SMTPProviderId)}>{smtpProviderOptions.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}</select><p class="muted-copy">{getSMTPPreset(smtp.provider)?.help}</p></div><div class="field-block"><label htmlFor="smtp-host">SMTP 主机</label><input id="smtp-host" value={smtp.host} onInput={(e) => patchSMTP("host", e.currentTarget.value)} /></div><div class="field-block"><label htmlFor="smtp-port">SMTP 端口</label><input id="smtp-port" inputMode="numeric" value={smtp.port} onInput={(e) => patchSMTP("port", e.currentTarget.value)} /></div><div class="field-block"><label htmlFor="smtp-tls">TLS</label><select id="smtp-tls" value={smtp.tls} onChange={(e) => patchSMTP("tls", e.currentTarget.value as SMTPFormState["tls"])}><option value="tls">直接 TLS</option><option value="starttls">STARTTLS</option><option value="none">无（仅可信内网）</option></select></div><div class="field-block"><label htmlFor="smtp-from">发件人</label><input id="smtp-from" type="email" value={smtp.from} onInput={(e) => patchSMTP("from", e.currentTarget.value)} /></div><div class="field-block"><label htmlFor="smtp-username">SMTP 用户名</label><input id="smtp-username" autoComplete="username" value={smtp.username} onInput={(e) => patchSMTP("username", e.currentTarget.value)} /></div><div class="field-block"><label htmlFor="smtp-password">SMTP 密码（留空保留现有密码）</label><input id="smtp-password" type="password" autoComplete="new-password" value={smtp.password} placeholder={smtp.passwordConfigured ? "已保存；留空保留" : "未保存密码"} onInput={(e) => patchSMTP("password", e.currentTarget.value)} /></div><div class="field-block"><label htmlFor="smtp-recipient">测试收件人</label><input id="smtp-recipient" type="email" value={smtp.recipient} onInput={(e) => patchSMTP("recipient", e.currentTarget.value)} /></div><div class="field-block smtp-test-action"><button class="button secondary" type="button" onClick={testSMTP} disabled={busy}>发送测试邮件</button></div></div></section><section><h2>配额与保留</h2><div class="form-grid two-columns">{quotaFields.map(([key, label]) => <div class="field-block" key={key}><label htmlFor={key}>{label}</label><input id={key} inputMode="numeric" value={String(settings[key] || "")} onInput={(e) => setSettings((v) => ({ ...v, [key]: e.currentTarget.value }))} /></div>)}</div></section><footer class="sticky-actions"><span>启动级安全配置不在此表单中</span><button class="button primary" type="submit" disabled={busy}>保存设置</button></footer></form> : null}
    {resource.data && route.page === "maintenance" ? resource.data.items?.length ? <ul class="record-list">{resource.data.items.map((item) => <li key={String(item.id)}><div><strong>{String(item.kind || item.name || "维护任务")}</strong><p>{String(item.detail || item.error || "—")}</p></div><div><StatusBadge value={String(item.status || "unknown")} /><time>{formatDate(item.createdAt)}</time></div></li>)}</ul> : <EmptyState title="没有维护任务" description="定时清理、checkpoint 和手动任务会显示在这里。" /> : null}
  </>;
}

function ReleaseAuditNav({ current }: { current: "releases" | "admin" | "access" }) { return <nav class="subnav" aria-label="发布与审计二级导航"><a href="/admin/releases" aria-current={current === "releases" ? "page" : undefined}>版本发布</a><a href="/admin/audit/admin" aria-current={current === "admin" ? "page" : undefined}>管理员审计</a><a href="/admin/audit/access" aria-current={current === "access" ? "page" : undefined}>HTTP 访问日志</a></nav>; }
function SystemNav({ current }: { current: PageId }) { return <nav class="subnav" aria-label="安全与维护二级导航"><a href="/admin/security" aria-current={current === "security" ? "page" : undefined}>安全设置</a><a href="/admin/maintenance" aria-current={current === "maintenance" ? "page" : undefined}>维护任务</a><a href="/admin/backups" aria-current={current === "backups" ? "page" : undefined}>备份恢复</a><a href="/admin/system" aria-current={current === "system" ? "page" : undefined}>系统状态</a></nav>; }
function formatDate(value: unknown) { if (typeof value !== "string" || !value) return "—"; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN"); }
function formatBytes(value?: number) { if (value === undefined) return "—"; if (value < 1024) return `${value} B`; if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KiB`; return `${(value / 1024 ** 2).toFixed(1)} MiB`; }
function toEditableSettings(value: Record<string, unknown>): Record<string, string | boolean> { const result: Record<string, string | boolean> = {}; for (const [key, item] of Object.entries(value)) { if (typeof item === "boolean" || typeof item === "string" || typeof item === "number") result[key] = typeof item === "number" ? String(item) : item; else if (Array.isArray(item)) result[key] = item.join("\n"); } return result; }
function toSMTPForm(value: unknown): SMTPFormState { if (!value || typeof value !== "object") return { ...emptySMTP }; const smtp = value as Record<string, unknown>; const host = String(smtp.host || ""); const port = String(smtp.port || "587"); const tls = ["tls", "starttls", "none"].includes(String(smtp.tls)) ? String(smtp.tls) as SMTPFormState["tls"] : "starttls"; return { provider: detectSMTPProvider({ host, port, tls }), host, port, tls, from: String(smtp.from || ""), username: String(smtp.username || ""), password: "", recipient: "", passwordConfigured: Boolean(smtp.passwordConfigured) }; }
function smtpPayload(value: SMTPFormState, includeRecipient = false) { return { host: value.host.trim(), port: Number(value.port), tls: value.tls, from: value.from.trim(), username: value.username.trim(), password: value.password, ...(includeRecipient ? { recipient: value.recipient.trim() } : {}) }; }
function smtpConfigurationKey(value: SMTPFormState) { return JSON.stringify(smtpPayload(value)); }
function editableSettingsPayload(value: Record<string, string | boolean>, smtp?: SMTPFormState) { const payload: Record<string, unknown> = { registrationEnabled: Boolean(value.registrationEnabled), publicBaseUrl: String(value.publicBaseUrl || ""), webOrigins: String(value.webOrigins || ""), maxUsers: String(value.maxUsers || ""), profileKiB: String(value.profileKiB || ""), storageGiB: String(value.storageGiB || ""), versionsPerUser: String(value.versionsPerUser || ""), accessLogDays: String(value.accessLogDays || ""), auditLogDays: String(value.auditLogDays || "") }; if (smtp && (smtp.host || smtp.from || smtp.username || smtp.passwordConfigured || smtp.password)) payload.smtp = smtpPayload(smtp); return payload; }
