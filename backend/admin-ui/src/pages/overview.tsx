import { ArrowRight, Clock3, Database, Gauge, Mail, Server, ShieldAlert } from "lucide-preact";
import type { ApiClient } from "../lib/api";
import { EmptyState, InlineError, PageHeading, RouteSkeleton, StatusBadge, useRouteData } from "../components/page";

interface HealthItem { id: string; label: string; status: string; detail?: string; checkedAt?: string }
interface AttentionItem { id: string; kind: string; severity: string; title: string; detail?: string; href: string }
interface OverviewData {
  health: HealthItem[];
  attention: AttentionItem[];
  sync24h: { successRate?: number; p95Ms?: number; unauthorized?: number; conflicts?: number; throttled?: number; serverErrors?: number; idempotentReplays?: number };
  recent: Array<{ id: string; label: string; detail?: string; at?: string }>;
}

function normalizeOverview(value: OverviewData | Record<string, number>): OverviewData {
  if ("health" in value) return value as OverviewData;
  return {
    health: [
      { id: "api", label: "API", status: "healthy", detail: "服务可用" },
      { id: "sqlite", label: "SQLite", status: "healthy", detail: `${value.profiles ?? 0} 个配置` },
      { id: "smtp", label: "邮件", status: "unknown", detail: "旧后端未提供状态" },
      { id: "backup", label: "最近备份", status: "unknown", detail: "旧后端未提供状态" },
      { id: "storage", label: "磁盘水位", status: "unknown", detail: "旧后端未提供状态" }
    ],
    attention: [],
    sync24h: {},
    recent: []
  };
}

const healthIcons = { api: Server, sqlite: Database, smtp: Mail, backup: Clock3, storage: Gauge };

export function OverviewPage({ client, onNavigate }: { client: ApiClient; onNavigate: (href: string) => void }) {
  const resource = useRouteData(
    async (signal) => normalizeOverview(await client.getWithLegacy<OverviewData | Record<string, number>>("/api/admin/v1/overview", "/api/admin/summary", signal)),
    [client]
  );

  return <>
    <PageHeading eyebrow="运维概览" title="概览" description="先处理异常，再查看容量与库存。每个状态都来自当前路由的独立请求。" />
    {resource.loading && !resource.data ? <RouteSkeleton label="正在加载概览" /> : null}
    {resource.error ? <InlineError error={resource.error} onRetry={resource.retry} /> : null}
    {resource.data ? <div class="overview-layout">
      <section class="status-strip" aria-label="服务状态">
        {resource.data.health.map((item) => { const Icon = healthIcons[item.id as keyof typeof healthIcons] || Server; return <article key={item.id}><Icon size={19} aria-hidden="true" /><div><span>{item.label}</span><strong>{item.detail || "—"}</strong></div><StatusBadge value={item.status} /></article>; })}
      </section>
      <section class="attention-section" aria-labelledby="attention-heading">
        <div class="section-heading"><div><p class="section-label">按影响排序</p><h2 id="attention-heading">需要关注</h2></div><ShieldAlert aria-hidden="true" /></div>
        {resource.data.attention.length ? <ul class="attention-list">{resource.data.attention.map((item) => <li key={item.id}><a href={item.href} onClick={(event) => { event.preventDefault(); onNavigate(item.href); }}><div><StatusBadge value={item.severity} /><strong>{item.title}</strong><p>{item.detail}</p></div><ArrowRight aria-hidden="true" /></a></li>)}</ul> : <EmptyState title="当前无待处理问题" description="同步、邮件、发布和安全配置没有报告异常。" />}
      </section>
      <section class="sync-summary" aria-labelledby="sync-heading"><div class="section-heading"><div><p class="section-label">最近 24 小时</p><h2 id="sync-heading">同步摘要</h2></div></div><dl><div><dt>成功率</dt><dd>{formatPercent(resource.data.sync24h.successRate)}</dd></div><div><dt>P95 延迟</dt><dd>{formatMs(resource.data.sync24h.p95Ms)}</dd></div><div><dt>冲突</dt><dd>{resource.data.sync24h.conflicts ?? "—"}</dd></div><div><dt>401 / 429 / 5xx</dt><dd>{resource.data.sync24h.unauthorized ?? 0} / {resource.data.sync24h.throttled ?? 0} / {resource.data.sync24h.serverErrors ?? 0}</dd></div><div><dt>幂等重放</dt><dd>{resource.data.sync24h.idempotentReplays ?? "—"}</dd></div></dl></section>
      <section class="recent-section" aria-labelledby="recent-heading"><div class="section-heading"><div><p class="section-label">最近变化</p><h2 id="recent-heading">发布与维护</h2></div></div>{resource.data.recent.length ? <ol class="timeline">{resource.data.recent.map((item) => <li key={item.id}><span /><div><strong>{item.label}</strong><p>{item.detail}</p><time>{item.at}</time></div></li>)}</ol> : <EmptyState title="暂无最近任务" description="发布、备份和维护任务会显示在这里。" />}</section>
    </div> : null}
  </>;
}

function formatPercent(value?: number) { return value === undefined ? "—" : `${value.toFixed(1)}%`; }
function formatMs(value?: number) { return value === undefined ? "—" : `${value} ms`; }
