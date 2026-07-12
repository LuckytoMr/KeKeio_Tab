import type { ComponentChildren } from "preact";
import { useEffect, useState } from "preact/hooks";
import { AlertTriangle, ChevronDown, ChevronRight, Inbox, RefreshCw } from "lucide-preact";
import { ApiError } from "../lib/api";

export function PageHeading({ eyebrow, title, description, actions }: { eyebrow: string; title: string; description?: string; actions?: ComponentChildren }) {
  return <header class="page-heading"><div><p class="section-label">{eyebrow}</p><h1 tabIndex={-1}>{title}</h1>{description ? <p class="page-lede">{description}</p> : null}</div>{actions ? <div class="page-actions">{actions}</div> : null}</header>;
}

export function RouteSkeleton({ label = "正在加载" }: { label?: string }) {
  return <div class="route-skeleton" aria-label={label} aria-busy="true"><span /><span /><span /><span /></div>;
}

export function InlineError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const message = error instanceof ApiError ? error.message : error instanceof Error ? error.message : "请求失败，请稍后重试";
  const requestId = error instanceof ApiError ? error.requestId : undefined;
  return <div class="inline-alert error" role="alert"><AlertTriangle aria-hidden="true" /><div><strong>无法加载这个区域</strong><p>{message}</p>{requestId ? <small>请求 ID：{requestId}</small> : null}</div><button class="button secondary" type="button" onClick={onRetry}><RefreshCw size={16} aria-hidden="true" />重试</button></div>;
}

export function EmptyState({ title, description, action }: { title: string; description: string; action?: ComponentChildren }) {
  return <div class="empty-state"><Inbox aria-hidden="true" /><strong>{title}</strong><p>{description}</p>{action}</div>;
}

export function StatusBadge({ value, label }: { value?: string; label?: string }) {
  const normalized = (value || "unknown").toLowerCase();
  const tone = ["healthy", "active", "enabled", "published", "ready", "verified", "success"].includes(normalized)
    ? "positive"
    : ["degraded", "warning", "pending", "draft", "validating", "legacy_unverified"].includes(normalized)
      ? "warning"
      : ["failed", "error", "disabled", "suspended", "banned", "conflict"].includes(normalized)
        ? "negative"
        : "neutral";
  const labels: Record<string, string> = { healthy: "正常", active: "活跃", enabled: "已启用", disabled: "已停用", published: "已发布", ready: "待发布", draft: "草稿", validating: "校验中", verified: "已验证", pending: "待处理", suspended: "已暂停", banned: "已封禁", degraded: "降级", failed: "失败", warning: "警告", conflict: "冲突", legacy_unverified: "旧账号待验证", unknown: "未知" };
  return <span class={`status-badge ${tone}`}><span aria-hidden="true" />{label || labels[normalized] || value || "未知"}</span>;
}

export function ExpandButton({ expanded, controls, onClick, label = "更多字段" }: { expanded: boolean; controls: string; onClick: () => void; label?: string }) {
  return <button class="icon-button compact" type="button" aria-label={expanded ? `收起${label}` : `展开${label}`} aria-expanded={expanded} aria-controls={controls} onClick={onClick}>{expanded ? <ChevronDown aria-hidden="true" /> : <ChevronRight aria-hidden="true" />}</button>;
}

export function useRouteData<T>(loader: (signal: AbortSignal) => Promise<T>, dependencies: readonly unknown[]) {
  const [state, setState] = useState<{ data: T | null; error: unknown; loading: boolean; revision: number }>({ data: null, error: null, loading: true, revision: 0 });

  useEffect(() => {
    const controller = new AbortController();
    setState((current) => ({ ...current, error: null, loading: true }));
    void loader(controller.signal)
      .then((data) => setState((current) => ({ ...current, data, error: null, loading: false })))
      .catch((error) => {
        if (!controller.signal.aborted) setState((current) => ({ ...current, error, loading: false }));
      });
    return () => controller.abort();
  }, [...dependencies, state.revision]);

  return {
    ...state,
    retry: () => setState((current) => ({ ...current, revision: current.revision + 1 }))
  };
}
