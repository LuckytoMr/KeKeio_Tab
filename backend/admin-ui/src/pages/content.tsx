import { useEffect, useMemo, useState } from "preact/hooks";
import { ArrowLeft, Archive, CheckCircle2, Eye, FilePlus2, History, Save, Send, ShieldBan } from "lucide-preact";
import type { ApiClient } from "../lib/api";
import type { AdminRoute, ContentType } from "../lib/router";
import { buildDetailHref, buildListHref, getReturnHref, readListState } from "../lib/router";
import { ConfirmDialog, FormErrorSummary } from "../components/common";
import { EmptyState, InlineError, PageHeading, RouteSkeleton, StatusBadge, useRouteData } from "../components/page";

const contentMeta = {
  official: { title: "官方壁纸", singular: "官方壁纸", legacy: "/api/admin/wallpapers/official" },
  web: { title: "Web 资源", singular: "Web 资源", legacy: "/api/admin/wallpapers/web" },
  styles: { title: "风格", singular: "风格", legacy: "/api/admin/styles" }
};

interface CatalogItem { id: string; name?: string; title?: string; status?: string; visibility?: string; updatedAt?: string; revision?: number; previewUrl?: string; description?: string }
interface CatalogList { items: CatalogItem[]; nextCursor?: string }

export function ContentListPage({ client, route, onNavigate }: { client: ApiClient; route: AdminRoute; onNavigate: (href: string) => void }) {
  const type = route.contentType || "official";
  const meta = contentMeta[type];
  const listState = readListState(route.search);
  const query = route.search.toString();
  const listHref = `${route.pathname}${query ? `?${query}` : ""}`;
  const resource = useRouteData(async (signal) => {
    const value = await client.getWithLegacy<CatalogList | CatalogItem[]>(`/api/admin/v1/catalog/${type}${query ? `?${query}` : ""}`, meta.legacy, signal);
    return Array.isArray(value) ? { items: value } : value;
  }, [client, query, type]);

  const applyFilters = (event: Event) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget as HTMLFormElement);
    onNavigate(buildListHref(`/admin/content/${type}`, { query: String(form.get("q") || ""), status: String(form.get("status") || ""), sort: String(form.get("sort") || "-updatedAt") }));
  };

  return <>
    <PageHeading eyebrow="内容" title={meta.title} description="草稿、校验和已发布 revision 明确分离；保存不会覆盖线上版本。" actions={<button class="button primary" type="button" onClick={() => onNavigate(buildDetailHref(type, "new", listHref))}><FilePlus2 size={17} aria-hidden="true" />新建草稿</button>} />
    <nav class="subnav" aria-label="内容类型"><a href="/admin/content/official" aria-current={type === "official" ? "page" : undefined} onClick={(e) => { e.preventDefault(); onNavigate("/admin/content/official"); }}>官方壁纸</a><a href="/admin/content/web" aria-current={type === "web" ? "page" : undefined} onClick={(e) => { e.preventDefault(); onNavigate("/admin/content/web"); }}>Web 资源</a><a href="/admin/content/styles" aria-current={type === "styles" ? "page" : undefined} onClick={(e) => { e.preventDefault(); onNavigate("/admin/content/styles"); }}>风格</a></nav>
    <form class="filter-bar" onSubmit={applyFilters}><label class="sr-only" htmlFor="content-search">搜索内容</label><input id="content-search" name="q" type="search" placeholder={`搜索${meta.title}`} defaultValue={listState.query} /><label htmlFor="content-status">Revision 状态</label><select id="content-status" name="status" defaultValue={listState.status}><option value="">全部状态</option><option value="draft">草稿</option><option value="validating">校验中</option><option value="ready">待发布</option><option value="published">已发布</option><option value="disabled">已停用</option></select><label htmlFor="content-sort">排序</label><select id="content-sort" name="sort" defaultValue={listState.sort || "-updatedAt"}><option value="-updatedAt">最近更新</option><option value="name">名称 A–Z</option><option value="status">状态</option></select><button class="button secondary" type="submit">应用筛选</button></form>
    <div class="list-detail-layout"><section class="list-pane" aria-label={`${meta.title}列表`}>
      {resource.loading && !resource.data ? <RouteSkeleton label={`正在加载${meta.title}`} /> : null}
      {resource.error ? <InlineError error={resource.error} onRetry={resource.retry} /> : null}
      {resource.data ? resource.data.items.length ? <ul class="resource-rows">{resource.data.items.map((item) => { const label = item.name || item.title || item.id; const href = buildDetailHref(type, item.id, listHref); return <li key={item.id}><a href={href} aria-label={`编辑 ${label}`} onClick={(e) => { e.preventDefault(); onNavigate(href); }}>{item.previewUrl ? <img src={item.previewUrl} alt="" loading="lazy" referrerPolicy="no-referrer" /> : <span class="resource-placeholder" aria-hidden="true">{label.slice(0, 1).toUpperCase()}</span>}<div><strong>{label}</strong><code>{item.id}</code><p>revision {item.revision ?? "—"} · {formatDate(item.updatedAt)}</p></div><div class="resource-status"><StatusBadge value={item.status} /><StatusBadge value={item.visibility} /></div></a></li>; })}</ul> : <EmptyState title={`还没有${meta.title}`} description={`创建第一个${meta.singular}草稿，校验后再发布。`} /> : null}
    </section><aside class="detail-placeholder" aria-label="资源详情"><Eye aria-hidden="true" /><strong>选择资源查看详情</strong><p>桌面端详情显示在此处；窄屏会进入独立详情路由，返回时保留筛选和滚动位置。</p></aside></div>
  </>;
}

interface CatalogDetail { item: CatalogItem & { fields?: Record<string, unknown> }; revisions?: Array<{ id: string; revision: number; status: string; createdAt?: string }> }

export function ContentDetailPage({ client, route, onNavigate, notify }: { client: ApiClient; route: AdminRoute; onNavigate: (href: string) => void; notify: (message: string, tone?: string) => void }) {
  const type = route.contentType || "official";
  const id = route.id || "new";
  const meta = contentMeta[type];
  const returnTo = getReturnHref(route.search, `/admin/content/${type}`);
  const isNew = id === "new";
  const resource = useRouteData(
    (signal) => isNew
      ? Promise.resolve<CatalogDetail>({ item: { id: "", status: "draft", visibility: "enabled", fields: {} }, revisions: [] })
      : client.get<CatalogDetail>(`/api/admin/v1/catalog/${type}/${encodeURIComponent(id)}`, signal),
    [client, id, isNew, type]
  );
  const [fields, setFields] = useState<Record<string, string>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [confirmAction, setConfirmAction] = useState<"publish" | "disable" | "rollback" | "archive" | null>(null);

  useEffect(() => {
    if (!resource.data) return;
    const item = resource.data.item;
    setFields(toCatalogFormFields(item, type));
  }, [resource.data]);

  const title = fields.name || resource.data?.item.name || resource.data?.item.title || (isNew ? `新建${meta.singular}` : id);
  const base = `/api/admin/v1/catalog/${type}`;

  const save = async (event: Event) => {
    event.preventDefault();
    const nextErrors: Record<string, string> = {};
    if (!fields.name?.trim()) nextErrors.name = "请输入名称";
    if (!isNew && !fields.id?.trim()) nextErrors.id = "资源 ID 不能为空";
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length) return;
    setBusy(true);
    try {
      const result = isNew
        ? await client.post<{ item?: CatalogItem }>(base, toCatalogPayload(fields, type))
        : await client.put<{ item?: CatalogItem }>(`${base}/${encodeURIComponent(id)}/draft`, toCatalogPayload(fields, type, resource.data?.item.fields));
      notify("草稿已保存");
      if (isNew && result.item?.id) onNavigate(buildDetailHref(type, result.item.id, returnTo));
      else resource.retry();
    } catch (error) { setErrors({ form: error instanceof Error ? error.message : "保存失败" }); }
    finally { setBusy(false); }
  };

  const runAction = async (action: "validate" | "publish" | "disable" | "rollback" | "archive") => {
    setBusy(true);
    try { await client.post(`${base}/${encodeURIComponent(id)}/${action}`, {}); notify(action === "validate" ? "校验已开始" : "操作已完成"); setConfirmAction(null); resource.retry(); }
    catch (error) { setErrors({ form: error instanceof Error ? error.message : "操作失败" }); }
    finally { setBusy(false); }
  };

  if (resource.loading && !resource.data) return <RouteSkeleton label={`正在加载${meta.singular}详情`} />;
  if (resource.error) return <><button class="back-link" type="button" onClick={() => onNavigate(returnTo)}><ArrowLeft size={17} aria-hidden="true" />返回{meta.title}</button><InlineError error={resource.error} onRetry={resource.retry} /></>;
  if (!resource.data) return null;
  const item = resource.data.item;
  return <>
    <button class="back-link" type="button" onClick={() => onNavigate(returnTo)}><ArrowLeft size={17} aria-hidden="true" />返回{meta.title}</button>
    <PageHeading eyebrow={`${meta.title} · ${isNew ? "新建" : `revision ${item.revision ?? "—"}`}`} title={title} description="已发布 revision 保持不可变；编辑只影响当前草稿。" actions={<div class="status-pair"><StatusBadge value={item.status} /><StatusBadge value={item.visibility} /></div>} />
    <div class="editor-workspace"><form class="editor-form" onSubmit={save} noValidate><FormErrorSummary errors={errors} focusOnMount /><section><h2>基本信息</h2><div class="form-grid two-columns"><div class="field-block"><label htmlFor="id">资源 ID</label><input id="id" value={fields.id || ""} onInput={(e) => setFields((v) => ({ ...v, id: e.currentTarget.value }))} disabled={!isNew} placeholder={type === "styles" ? "style:quiet" : `${type}:resource-id`} /></div><div class="field-block"><label htmlFor="name">名称</label><input id="name" value={fields.name || ""} onInput={(e) => setFields((v) => ({ ...v, name: e.currentTarget.value }))} /></div><div class="field-block wide"><label htmlFor="description">描述</label><textarea id="description" rows={3} value={fields.description || ""} onInput={(e) => setFields((v) => ({ ...v, description: e.currentTarget.value }))} /></div><div class="field-block wide"><label htmlFor="previewUrl">预览图 URL</label><input id="previewUrl" type="url" value={fields.previewUrl || ""} onInput={(e) => setFields((v) => ({ ...v, previewUrl: e.currentTarget.value }))} /></div>{type === "styles" ? <><div class="field-block"><label htmlFor="version">语义版本</label><input id="version" value={fields.version || ""} onInput={(e) => setFields((v) => ({ ...v, version: e.currentTarget.value }))} placeholder="1.0.0" /></div><div class="field-block"><label htmlFor="schemaVersion">Style schema</label><input id="schemaVersion" value={fields.schemaVersion || "2"} onInput={(e) => setFields((v) => ({ ...v, schemaVersion: e.currentTarget.value }))} /></div><div class="field-block wide"><label htmlFor="css">受限 CSS</label><textarea id="css" rows={12} value={fields.css || ""} onInput={(e) => setFields((v) => ({ ...v, css: e.currentTarget.value }))} spellcheck={false} /></div><div class="field-block wide"><label htmlFor="config">结构化配置 JSON</label><textarea id="config" rows={6} value={fields.config || "{}"} onInput={(e) => setFields((v) => ({ ...v, config: e.currentTarget.value }))} spellcheck={false} /></div></> : <WallpaperFields fields={fields} setFields={setFields} web={type === "web"} />}</div></section><footer class="sticky-actions"><span>{busy ? "正在处理…" : "更改只保存到草稿"}</span><button class="button primary" type="submit" disabled={busy}><Save size={17} aria-hidden="true" />保存草稿</button></footer></form><aside class="editor-side"><section><h2>发布流程</h2><ol class="release-steps"><li data-current={item.status === "draft"}><span>1</span><div><strong>草稿</strong><p>编辑并保存字段</p></div></li><li data-current={item.status === "validating" || item.status === "ready"}><span>2</span><div><strong>校验</strong><p>安全、URL 与兼容范围</p></div></li><li data-current={item.status === "published"}><span>3</span><div><strong>发布</strong><p>原子切换 active revision</p></div></li></ol><div class="action-stack"><button class="button secondary" type="button" disabled={busy || isNew} onClick={() => runAction("validate")}><CheckCircle2 size={16} aria-hidden="true" />运行校验</button>{type === "styles" && !isNew ? <a class="button secondary" href={`${base}/${encodeURIComponent(id)}/preview`} target="style-preview" rel="noreferrer"><Eye size={16} aria-hidden="true" />隔离预览</a> : null}<button class="button primary" type="button" disabled={busy || isNew || item.status !== "ready"} onClick={() => setConfirmAction("publish")}><Send size={16} aria-hidden="true" />发布 ready revision</button><button class="button secondary" type="button" disabled={busy || isNew || item.visibility === "disabled"} onClick={() => setConfirmAction("disable")}><ShieldBan size={16} aria-hidden="true" />停用内容</button><button class="button secondary" type="button" disabled={busy || isNew || !(resource.data?.revisions?.length)} onClick={() => setConfirmAction("rollback")}><History size={16} aria-hidden="true" />回滚历史 revision</button><button class="button danger" type="button" disabled={busy || isNew || item.visibility !== "disabled"} onClick={() => setConfirmAction("archive")}><Archive size={16} aria-hidden="true" />归档</button></div></section>{type === "styles" && !isNew ? <section><h2>隔离预览</h2><iframe class="style-preview" name="style-preview" title={`${title} 隔离预览`} sandbox="" src={`${base}/${encodeURIComponent(id)}/preview`} /></section> : null}<section><h2>Revision 历史</h2>{resource.data.revisions?.length ? <ol class="revision-list">{resource.data.revisions.map((revision) => <li key={revision.id}><span>r{revision.revision}</span><StatusBadge value={revision.status} /><time>{formatDate(revision.createdAt)}</time></li>)}</ol> : <p class="muted-copy">保存草稿后会产生首个 revision。</p>}</section></aside></div>
    <ConfirmDialog open={Boolean(confirmAction)} title={actionTitle(confirmAction)} description="此操作会改变扩展可见内容并写入管理员审计。历史 revision 不会被改写。" confirmLabel={actionTitle(confirmAction)} busy={busy} onCancel={() => setConfirmAction(null)} onConfirm={() => confirmAction && runAction(confirmAction)} />
  </>;
}

function WallpaperFields({ fields, setFields, web }: { fields: Record<string, string>; setFields: (updater: (current: Record<string, string>) => Record<string, string>) => void; web: boolean }) {
  return <>{web ? <><div class="field-block"><label htmlFor="provider">Provider</label><select id="provider" value={fields.provider || "uhdpaper"} onChange={(e) => setFields((v) => ({ ...v, provider: e.currentTarget.value }))}><option value="uhdpaper">UHD Paper</option></select></div><div class="field-block wide"><label htmlFor="sourcePageUrl">来源页面 URL</label><input id="sourcePageUrl" type="url" value={fields.sourcePageUrl || ""} onInput={(e) => setFields((v) => ({ ...v, sourcePageUrl: e.currentTarget.value }))} /></div></> : null}<div class="field-block"><label htmlFor="category">分类</label><input id="category" value={fields.category || ""} onInput={(e) => setFields((v) => ({ ...v, category: e.currentTarget.value }))} /></div><div class="field-block"><label htmlFor="tags">标签</label><input id="tags" value={fields.tags || ""} onInput={(e) => setFields((v) => ({ ...v, tags: e.currentTarget.value }))} placeholder="nature, 4k" /></div><div class="field-block wide"><label htmlFor="variant4kUrl">4K 图片 URL</label><input id="variant4kUrl" type="url" value={fields.variant4kUrl || ""} onInput={(e) => setFields((v) => ({ ...v, variant4kUrl: e.currentTarget.value }))} /></div><div class="field-block wide"><label htmlFor="variant2kUrl">2K 图片 URL</label><input id="variant2kUrl" type="url" value={fields.variant2kUrl || ""} onInput={(e) => setFields((v) => ({ ...v, variant2kUrl: e.currentTarget.value }))} /></div><div class="field-block wide"><label htmlFor="variantHdUrl">1080P 图片 URL</label><input id="variantHdUrl" type="url" value={fields.variantHdUrl || ""} onInput={(e) => setFields((v) => ({ ...v, variantHdUrl: e.currentTarget.value }))} /></div></>;
}

function stringFields(value: Record<string, unknown>): Record<string, string> { const result: Record<string, string> = {}; for (const [key, field] of Object.entries(value)) result[key] = typeof field === "string" ? field : JSON.stringify(field); return result; }
function toCatalogFormFields(item: CatalogItem & { fields?: Record<string, unknown> }, type: ContentType): Record<string, string> {
  const raw = item.fields || {};
  const common = {
    id: String(raw.id || item.id || ""),
    name: String(raw.name || raw.title || item.name || item.title || ""),
    description: String(raw.description || item.description || ""),
    previewUrl: String(raw.previewUrl || item.previewUrl || "")
  };
  if (type === "styles") return { ...common, ...stringFields(raw) };
  const variants = Array.isArray(raw.variants) ? raw.variants : [];
  const variantURL = (wanted: string) => {
    const match = variants.find((value) => {
      if (!value || typeof value !== "object") return false;
      const variant = value as Record<string, unknown>;
      const id = String(variant.id || "").toLowerCase();
      const label = String(variant.label || "").toLowerCase();
      return id === wanted || (wanted === "hd" && (id === "1080p" || label.includes("1920x1080"))) || (wanted === "4k" && label.includes("3840x2160")) || (wanted === "2k" && label.includes("2560x1440"));
    }) as Record<string, unknown> | undefined;
    return typeof match?.url === "string" ? match.url : "";
  };
  return {
    ...common,
    category: typeof raw.category === "string" ? raw.category : "",
    tags: Array.isArray(raw.tags) ? raw.tags.filter((value): value is string => typeof value === "string").join(", ") : typeof raw.tags === "string" ? raw.tags : "",
    variant4kUrl: variantURL("4k"),
    variant2kUrl: variantURL("2k"),
    variantHdUrl: variantURL("hd"),
    provider: typeof raw.provider === "string" ? raw.provider : "uhdpaper",
    sourcePageUrl: typeof raw.sourcePageUrl === "string" ? raw.sourcePageUrl : ""
  };
}
function toCatalogPayload(fields: Record<string, string>, type: ContentType, existing: Record<string, unknown> = {}) { const payload: Record<string, unknown> = { ...existing, id: fields.id?.trim(), name: fields.name?.trim(), description: fields.description?.trim(), previewUrl: fields.previewUrl?.trim() }; if (type === "styles") { payload.version = fields.version; payload.schemaVersion = Number(fields.schemaVersion || 2); payload.css = fields.css || ""; try { payload.config = JSON.parse(fields.config || "{}"); } catch { payload.config = fields.config; } } else { payload.category = fields.category; payload.tags = (fields.tags || "").split(",").map((v) => v.trim()).filter(Boolean); payload.variants = [["4k", "3840x2160", fields.variant4kUrl], ["2k", "2560x1440", fields.variant2kUrl], ["hd", "1920x1080", fields.variantHdUrl]].filter(([, , url]) => url).map(([variantId, label, url]) => ({ id: variantId, label, url })); if (type === "web") { payload.provider = fields.provider || "uhdpaper"; payload.sourcePageUrl = fields.sourcePageUrl; } } return payload; }
function actionTitle(action: string | null) { return action === "publish" ? "确认发布" : action === "disable" ? "确认停用" : action === "rollback" ? "创建回滚草稿" : "确认归档"; }
function formatDate(value?: string) { if (!value) return "—"; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN"); }
