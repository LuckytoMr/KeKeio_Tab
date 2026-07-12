import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks";
import { Check, ChevronLeft, ChevronRight, KeyRound, LoaderCircle, LockKeyhole, MailCheck, ServerCog } from "lucide-preact";
import type { ApiClient } from "../lib/api";
import { ApiError } from "../lib/api";
import { FormErrorSummary } from "../components/common";
import type { AdminUser } from "../components/shell";
import { detectSMTPProvider, getSMTPPreset, smtpProviderOptions, type SMTPProviderId } from "../lib/smtpPresets";

export interface AdminSession {
  user: AdminUser;
  csrfToken: string;
}

export function LoginPage({
  client,
  preAuthCsrf,
  onAuthenticated
}: {
  client: ApiClient;
  preAuthCsrf?: string;
  onAuthenticated: (session: AdminSession) => void;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const errorRef = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    if (error) errorRef.current?.focus();
  }, [error]);

  const submit = async (event: Event) => {
    event.preventDefault();
    setError("");
    if (!email.trim() || !password) {
      setError("请输入管理员邮箱和密码");
      return;
    }
    setBusy(true);
    try {
      client.setCsrfToken(preAuthCsrf);
      const session = await client.post<AdminSession>("/api/admin/v1/auth/login", { email: email.trim(), password });
      client.setCsrfToken(session.csrfToken);
      onAuthenticated(session);
    } catch (value) {
      setError(value instanceof ApiError ? value.message : "暂时无法登录，请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthFrame title="登录运维工作台" copy="管理入口仅对已配置的本机或局域网开放。管理员身份与插件账号完全隔离。" icon={<LockKeyhole aria-hidden="true" />}>
      {error ? <div ref={errorRef} class="error-summary" role="alert" aria-label="登录失败" tabIndex={-1}><strong>登录失败</strong><p>{error}</p></div> : null}
      <form class="stack-form" onSubmit={submit} noValidate>
        <label htmlFor="login-email">管理员邮箱</label>
        <input id="login-email" type="email" autoComplete="username" value={email} onInput={(event) => setEmail(event.currentTarget.value)} required />
        <label htmlFor="login-password">密码</label>
        <input id="login-password" type="password" autoComplete="current-password" value={password} onInput={(event) => setPassword(event.currentTarget.value)} required />
        <button class="button primary full-width" type="submit" disabled={busy}>
          {busy ? <><LoaderCircle class="spin" size={18} aria-hidden="true" /> 正在登录</> : "登录后台"}
        </button>
      </form>
      <p class="auth-footnote">没有默认账号或密码。若管理员凭据遗失，请在服务器本机使用维护命令进入重置流程。</p>
    </AuthFrame>
  );
}

interface InstallStatus {
  state: "uninitialized" | "requires_admin_reset" | "installed";
  mode: "fresh_install" | "admin_reset";
  requiresCode?: boolean;
}

interface InstallSession {
  mode: "fresh_install" | "admin_reset";
  csrfToken: string;
  expiresAt?: string;
}

export interface InstallDraft {
  adminEmail: string;
  displayName: string;
  password: string;
  passwordConfirm: string;
  publicBaseUrl: string;
  extensionIds: string;
  webOrigins: string;
  registrationEnabled: boolean;
  smtpProvider?: SMTPProviderId;
  smtpHost: string;
  smtpPort: string;
  smtpTls: "tls" | "starttls" | "none";
  smtpFrom: string;
  smtpUser: string;
  smtpPassword: string;
  maxUsers: string;
  profileKiB: string;
  storageGiB: string;
  versionsPerUser: string;
  accessLogDays: string;
  auditLogDays: string;
  backupDirectory: string;
}

const defaultPublicBaseUrl = "https://tab.kekeio.com";
const defaultSMTPPreset = getSMTPPreset("cloudflare");

const defaultDraft: InstallDraft = {
  adminEmail: "",
  displayName: "",
  password: "",
  passwordConfirm: "",
  publicBaseUrl: defaultPublicBaseUrl,
  extensionIds: "",
  webOrigins: "",
  registrationEnabled: false,
  smtpProvider: defaultSMTPPreset.id,
  smtpHost: defaultSMTPPreset.host,
  smtpPort: defaultSMTPPreset.port,
  smtpTls: defaultSMTPPreset.tls,
  smtpFrom: defaultSMTPPreset.defaultFrom ?? "",
  smtpUser: defaultSMTPPreset.username ?? "",
  smtpPassword: "",
  maxUsers: "100",
  profileKiB: "512",
  storageGiB: "1",
  versionsPerUser: "50",
  accessLogDays: "30",
  auditLogDays: "180",
  backupDirectory: "/backups"
};

const persistedKeys: Array<keyof InstallDraft> = [
  "adminEmail", "displayName", "publicBaseUrl", "extensionIds", "webOrigins", "registrationEnabled", "smtpProvider", "smtpHost", "smtpPort",
  "smtpTls", "smtpFrom", "smtpUser", "maxUsers", "profileKiB", "storageGiB", "versionsPerUser", "accessLogDays", "auditLogDays", "backupDirectory"
];

function hasConfiguredSMTPDraft(draft: Partial<InstallDraft>): boolean {
  if (draft.smtpProvider && draft.smtpProvider !== "custom") return true;
  return [draft.smtpHost, draft.smtpFrom, draft.smtpUser].some((value) => typeof value === "string" && value.trim() !== "");
}

function loadProgress(): { draft: InstallDraft; stepIndex: number } {
  try {
    const raw = window.sessionStorage.getItem("fullpro:install:draft");
    if (!raw) return { draft: defaultDraft, stepIndex: 0 };
    const parsed = JSON.parse(raw) as { draft?: Partial<InstallDraft>; stepIndex?: number };
    const persistedDraft = parsed.draft ?? {};
    const publicBaseUrl = typeof persistedDraft.publicBaseUrl === "string" && persistedDraft.publicBaseUrl.trim()
      ? persistedDraft.publicBaseUrl
      : defaultPublicBaseUrl;
    const upgradeBlankSMTP = !hasConfiguredSMTPDraft(persistedDraft);
    const smtpDraft = upgradeBlankSMTP ? {
      smtpProvider: defaultDraft.smtpProvider,
      smtpHost: defaultDraft.smtpHost,
      smtpPort: defaultDraft.smtpPort,
      smtpTls: defaultDraft.smtpTls,
      smtpFrom: defaultDraft.smtpFrom,
      smtpUser: defaultDraft.smtpUser
    } : {};
    const mergedDraft = { ...defaultDraft, ...persistedDraft, ...smtpDraft };
    const smtpProvider = upgradeBlankSMTP
      ? defaultDraft.smtpProvider
      : persistedDraft.smtpProvider ?? detectSMTPProvider({
        host: mergedDraft.smtpHost,
        port: mergedDraft.smtpPort,
        tls: mergedDraft.smtpTls
      });
    return {
      draft: { ...mergedDraft, publicBaseUrl, smtpProvider },
      stepIndex: Number.isInteger(parsed.stepIndex) ? Math.max(0, parsed.stepIndex ?? 0) : 0
    };
  } catch {
    return { draft: defaultDraft, stepIndex: 0 };
  }
}

export function serializeInstallProgress(draft: InstallDraft, stepIndex: number): string {
  const safe: Partial<InstallDraft> = {};
  for (const key of persistedKeys) (safe as Record<string, unknown>)[key] = draft[key];
  return JSON.stringify({ draft: safe, stepIndex: Math.max(0, Math.round(stepIndex)) });
}

function persistProgress(draft: InstallDraft, stepIndex: number): void {
  window.sessionStorage.setItem("fullpro:install:draft", serializeInstallProgress(draft, stepIndex));
}

const freshSteps = ["环境检查", "管理员账号", "公网 API", "邮件服务", "容量与保留", "复核并安装"];
const resetSteps = ["环境检查", "管理员账号", "复核并安装"];
const smtpVerificationInputs = new Set<keyof InstallDraft>([
  "adminEmail",
  "smtpHost",
  "smtpPort",
  "smtpTls",
  "smtpFrom",
  "smtpUser",
  "smtpPassword"
]);

export function validateInstallStepInput(
  step: string,
  draft: InstallDraft,
  mode: InstallSession["mode"],
  preflightReady: boolean,
  smtpVerified: boolean
): Record<string, string> {
  const result: Record<string, string> = {};
  if (step === "环境检查" && !preflightReady) result.preflight = "请先运行环境检查";
  if (step === "管理员账号") {
    if (!draft.adminEmail.trim()) result.adminEmail = "请输入管理员邮箱";
    if (!draft.displayName.trim()) result.displayName = "请输入显示名";
    if (draft.password.length < 12) result.adminPassword = "管理员密码至少 12 位";
    if (draft.password !== draft.passwordConfirm) result.passwordConfirm = "两次密码不一致";
  }
  if (step === "公网 API" && mode === "fresh_install") {
    const value = draft.publicBaseUrl.trim();
    if (!value) result.publicBaseUrl = "请输入绝对 HTTPS 公网 API 地址";
    else {
      try {
        const url = new URL(value);
        if (url.protocol !== "https:" || !url.hostname) result.publicBaseUrl = "请输入绝对 HTTPS 公网 API 地址";
      } catch {
        result.publicBaseUrl = "请输入绝对 HTTPS 公网 API 地址";
      }
    }
  }
  if (step === "邮件服务" && draft.registrationEnabled && !smtpVerified) result.smtpTest = "开启注册前必须成功发送测试邮件";
  return result;
}

export function InstallWizard({ client, onInstalled }: { client: ApiClient; onInstalled: () => void }) {
  const initialProgress = useRef(loadProgress());
  const [status, setStatus] = useState<InstallStatus | null>(null);
  const [session, setSession] = useState<InstallSession | null>(null);
  const [installCode, setInstallCode] = useState("");
  const [draft, setDraft] = useState<InstallDraft>(initialProgress.current.draft);
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const [stepIndex, setStepIndex] = useState(initialProgress.current.stepIndex);
  const [preflight, setPreflight] = useState<Array<{ label: string; status: string; detail?: string }> | null>(null);
  const [smtpVerified, setSmtpVerified] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  const loadStatus = async () => {
    setLoadError("");
    try {
      const next = await client.get<InstallStatus>("/install/api/v1/status");
      setStatus(next);
      if (next.state === "installed") onInstalled();
    } catch (value) {
      setLoadError(value instanceof ApiError ? value.message : "无法读取安装状态");
    }
  };

  useEffect(() => void loadStatus(), []);
  useEffect(() => persistProgress(draft, stepIndex), [draft, stepIndex]);

  const steps = useMemo(() => (session?.mode === "admin_reset" ? resetSteps : freshSteps), [session?.mode]);
  const step = steps[stepIndex] ?? steps[0] ?? "环境检查";

  const patchDraft = <K extends keyof InstallDraft>(key: K, value: InstallDraft[K]) => {
    setDraft((current) => ({ ...current, [key]: value }));
    if (smtpVerificationInputs.has(key)) setSmtpVerified(false);
    setErrors((current) => {
      if (!current[key]) return current;
      const next = { ...current };
      delete next[key];
      return next;
    });
  };

  const handleExpiredSession = (value: unknown): boolean => {
    if (!(value instanceof ApiError) || (value.status !== 401 && value.code !== "INSTALL_SESSION_EXPIRED")) return false;
    client.setCsrfToken(null);
    setSession(null);
    setInstallCode("");
    setErrors({ installCode: value.message || "安装会话已过期，请重新验证安装码" });
    return true;
  };

  const establishSession = async (event: Event) => {
    event.preventDefault();
    if (!installCode.trim()) {
      setErrors({ installCode: "请输入一次性安装码" });
      return;
    }
    setBusy(true);
    setErrors({});
    try {
      const next = await client.post<InstallSession>("/install/api/v1/session", { installCode: installCode.trim() });
      client.setCsrfToken(next.csrfToken);
      setSession(next);
      setInstallCode("");
      setStepIndex(0);
      setPreflight(null);
      setSmtpVerified(false);
    } catch (value) {
      setErrors({ installCode: value instanceof ApiError ? value.message : "无法建立安装会话" });
    } finally {
      setBusy(false);
    }
  };

  const validateStep = (): Record<string, string> => {
    return validateInstallStepInput(step, draft, session?.mode || "fresh_install", Boolean(preflight), smtpVerified);
  };

  const advance = () => {
    const nextErrors = validateStep();
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length) return;
    setStepIndex((current) => Math.min(steps.length - 1, current + 1));
  };

  const runPreflight = async () => {
    setBusy(true);
    setErrors({});
    try {
      const result = await client.post<{ checks: Array<{ label: string; status: string; detail?: string }> }>("/install/api/v1/preflight", {});
      setPreflight(result.checks ?? []);
    } catch (value) {
      if (!handleExpiredSession(value)) setErrors({ preflight: value instanceof ApiError ? value.message : "环境检查失败" });
    } finally {
      setBusy(false);
    }
  };

  const testSmtp = async () => {
    const testedConfiguration = installSMTPConfigurationKey(draft);
    setBusy(true);
    setErrors({});
    try {
      await client.post("/install/api/v1/smtp-test", installSMTPTestPayload(draft));
      if (installSMTPConfigurationKey(draftRef.current) !== testedConfiguration) {
        setSmtpVerified(false);
        setErrors({ smtpTest: "SMTP 配置在测试期间已修改，请重新测试" });
        return;
      }
      setSmtpVerified(true);
    } catch (value) {
      if (!handleExpiredSession(value)) setErrors({ smtpTest: value instanceof ApiError ? value.message : "测试邮件发送失败" });
    } finally {
      setBusy(false);
    }
  };

  const complete = async () => {
    setBusy(true);
    setErrors({});
    try {
      await client.post("/install/api/v1/complete", {
        mode: session?.mode,
        admin: { email: draft.adminEmail.trim(), displayName: draft.displayName.trim(), password: draft.password },
        publicApi: {
          baseUrl: draft.publicBaseUrl.trim(),
          extensionIds: splitValues(draft.extensionIds),
          webOrigins: splitValues(draft.webOrigins)
        },
        registration: { enabled: draft.registrationEnabled },
        smtp: draft.registrationEnabled
          ? { host: draft.smtpHost, port: Number(draft.smtpPort), tls: draft.smtpTls, from: draft.smtpFrom, username: draft.smtpUser, password: draft.smtpPassword }
          : null,
        limits: {
          maxUsers: Number(draft.maxUsers), profileBytes: Number(draft.profileKiB) * 1024, storageBytes: Number(draft.storageGiB) * 1024 ** 3,
          versionsPerUser: Number(draft.versionsPerUser), accessLogDays: Number(draft.accessLogDays), auditLogDays: Number(draft.auditLogDays), backupDirectory: draft.backupDirectory
        }
      });
      window.sessionStorage.removeItem("fullpro:install:draft");
      onInstalled();
    } catch (value) {
      if (!handleExpiredSession(value)) setErrors({ complete: value instanceof ApiError ? value.message : "安装未完成，请重试" });
    } finally {
      setBusy(false);
    }
  };

  if (loadError) {
    return <AuthFrame title="无法打开安装向导" copy="安装状态暂时不可用。" icon={<ServerCog aria-hidden="true" />}><div class="inline-alert" role="alert">{loadError}</div><button class="button primary" type="button" onClick={loadStatus}>重试</button></AuthFrame>;
  }
  if (!status) return <AuthFrame title="正在检查安装状态" copy="正在确认服务是否需要初始化。" icon={<LoaderCircle class="spin" aria-hidden="true" />}><div class="skeleton-lines" aria-label="正在加载" /></AuthFrame>;
  if (!session) {
    return (
      <AuthFrame title="验证安装码" copy="安装入口只在未初始化或管理员重置期间开放。安装码可在容器日志或数据卷的 install-code 文件中找到。" icon={<KeyRound aria-hidden="true" />}>
        <FormErrorSummary errors={errors} focusOnMount />
        <form class="stack-form" onSubmit={establishSession} noValidate>
          <label htmlFor="installCode">一次性安装码</label>
          <input id="installCode" type="password" autoComplete="off" value={installCode} onInput={(event) => setInstallCode(event.currentTarget.value)} aria-describedby="install-code-help" />
          <p id="install-code-help" class="field-help">安装码不会保存到浏览器。验证失败不会暴露服务配置。</p>
          <button class="button primary full-width" type="submit" disabled={busy}>{busy ? "正在验证…" : "建立安全安装会话"}</button>
        </form>
      </AuthFrame>
    );
  }

  return (
    <div class="install-shell">
      <aside class="install-progress" aria-label="安装步骤">
        <div class="brand-block"><span class="brand-mark" aria-hidden="true">KT</span><div><strong>KeKeIO Tab</strong><span>{session.mode === "admin_reset" ? "管理员重置" : "首次安装"}</span></div></div>
        <ol>{steps.map((label, index) => <li key={label} aria-current={index === stepIndex ? "step" : undefined} data-complete={index < stepIndex ? "true" : "false"}><span>{index < stepIndex ? <Check size={14} aria-hidden="true" /> : index + 1}</span>{label}</li>)}</ol>
        <p class="session-note">会话最长 2 小时。{session.expiresAt ? `本次会话将在 ${formatDate(session.expiresAt)} 前有效。` : "即将过期时页面会提示续期。"}</p>
      </aside>
      <main class="install-main">
        <header class="page-heading"><div><p class="section-label">步骤 {stepIndex + 1} / {steps.length}</p><h1>{step}</h1></div></header>
        <FormErrorSummary errors={errors} focusOnMount />
        {step === "环境检查" ? <EnvironmentStep checks={preflight} busy={busy} onRun={runPreflight} /> : null}
        {step === "管理员账号" ? <AdminStep draft={draft} patch={patchDraft} /> : null}
        {step === "公网 API" ? <PublicApiStep draft={draft} patch={patchDraft} /> : null}
        {step === "邮件服务" ? <MailStep draft={draft} patch={patchDraft} verified={smtpVerified} busy={busy} onTest={testSmtp} /> : null}
        {step === "容量与保留" ? <LimitsStep draft={draft} patch={patchDraft} /> : null}
        {step === "复核并安装" ? <ReviewStep draft={draft} mode={session.mode} /> : null}
        <footer class="wizard-actions">
          <button class="button secondary" type="button" onClick={() => setStepIndex((value) => Math.max(0, value - 1))} disabled={stepIndex === 0 || busy}><ChevronLeft size={17} aria-hidden="true" />上一步</button>
          {stepIndex === steps.length - 1
            ? <button class="button primary" type="button" onClick={complete} disabled={busy}>{busy ? "正在安装…" : "确认并完成安装"}</button>
            : <button class="button primary" type="button" onClick={advance} disabled={busy}>继续<ChevronRight size={17} aria-hidden="true" /></button>}
        </footer>
      </main>
    </div>
  );
}

function AuthFrame({ title, copy, icon, children }: { title: string; copy: string; icon: preact.ComponentChildren; children: preact.ComponentChildren }) {
  return <main class="auth-shell"><section class="auth-copy"><div class="auth-icon">{icon}</div><p class="section-label">本地优先 · 管理员专用</p><h1>{title}</h1><p>{copy}</p><dl><div><dt>网络边界</dt><dd>仅本机或已配置局域网</dd></div><div><dt>认证边界</dt><dd>独立管理员 Cookie 与 CSRF</dd></div><div><dt>隐私边界</dt><dd>不上传本地图片、凭据或设备状态</dd></div></dl></section><section class="auth-card">{children}</section></main>;
}

function splitValues(value: string): string[] { return value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean); }
function formatDate(value: string): string { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN"); }
function installSMTPTestPayload(draft: InstallDraft) { return { host: draft.smtpHost.trim(), port: Number(draft.smtpPort), tls: draft.smtpTls, from: draft.smtpFrom.trim(), username: draft.smtpUser.trim(), password: draft.smtpPassword, recipient: draft.adminEmail.trim() }; }
function installSMTPConfigurationKey(draft: InstallDraft): string { return JSON.stringify(installSMTPTestPayload(draft)); }

type PatchDraft = <K extends keyof InstallDraft>(key: K, value: InstallDraft[K]) => void;

function EnvironmentStep({ checks, busy, onRun }: { checks: Array<{ label: string; status: string; detail?: string }> | null; busy: boolean; onRun: () => void }) {
  return <section class="workflow-section"><p class="page-lede">检查数据目录、系统时间、磁盘、HTTPS 和可信代理。失败项必须先在服务器配置中处理。</p><button id="preflight" class="button primary" type="button" onClick={onRun} disabled={busy}>{busy ? "正在检查…" : checks ? "重新运行环境检查" : "运行环境检查"}</button>{checks ? <ul class="check-list">{checks.map((check) => <li key={check.label} data-status={check.status}><span><Check size={16} aria-hidden="true" /></span><div><strong>{check.label}</strong><p>{check.detail || check.status}</p></div></li>)}</ul> : null}</section>;
}

function AdminStep({ draft, patch }: { draft: InstallDraft; patch: PatchDraft }) {
  return <section class="workflow-section form-section"><div class="form-grid"><label htmlFor="adminEmail">管理员邮箱</label><input id="adminEmail" type="email" autoComplete="username" value={draft.adminEmail} onInput={(e) => patch("adminEmail", e.currentTarget.value)} /><label htmlFor="displayName">显示名</label><input id="displayName" value={draft.displayName} onInput={(e) => patch("displayName", e.currentTarget.value)} /><label htmlFor="adminPassword">密码</label><input id="adminPassword" type="password" autoComplete="new-password" value={draft.password} onInput={(e) => patch("password", e.currentTarget.value)} /><label htmlFor="passwordConfirm">再次输入密码</label><input id="passwordConfirm" type="password" autoComplete="new-password" value={draft.passwordConfirm} onInput={(e) => patch("passwordConfirm", e.currentTarget.value)} /></div><p class="field-help">至少 12 位并避免常见弱密码。密码不会写入浏览器存储。</p></section>;
}

function PublicApiStep({ draft, patch }: { draft: InstallDraft; patch: PatchDraft }) {
  return <section class="workflow-section form-section"><div class="form-grid"><label htmlFor="publicBaseUrl">外部 HTTPS 基础 URL</label><input id="publicBaseUrl" type="url" placeholder={defaultPublicBaseUrl} value={draft.publicBaseUrl} onInput={(e) => patch("publicBaseUrl", e.currentTarget.value)} /><label htmlFor="extensionIds">允许的扩展 ID</label><textarea id="extensionIds" rows={3} placeholder="每行一个 Chrome / Edge 扩展 ID" value={draft.extensionIds} onInput={(e) => patch("extensionIds", e.currentTarget.value)} /><label htmlFor="webOrigins">Web 开发来源（可选）</label><textarea id="webOrigins" rows={3} placeholder="https://localhost:5173" value={draft.webOrigins} onInput={(e) => patch("webOrigins", e.currentTarget.value)} /></div></section>;
}

function MailStep({ draft, patch, verified, busy, onTest }: { draft: InstallDraft; patch: PatchDraft; verified: boolean; busy: boolean; onTest: () => void }) {
  const provider = draft.smtpProvider ?? detectSMTPProvider({ host: draft.smtpHost, port: draft.smtpPort, tls: draft.smtpTls });
  const providerPreset = getSMTPPreset(provider);
  const selectProvider = (id: SMTPProviderId) => {
    const previousPreset = getSMTPPreset(provider);
    patch("smtpProvider", id);
    if (id === "custom") return;
    const preset = getSMTPPreset(id);
    patch("smtpHost", preset.host);
    patch("smtpPort", preset.port);
    patch("smtpTls", preset.tls);
    if (preset.username) patch("smtpUser", preset.username);
    else if (previousPreset.username && draft.smtpUser === previousPreset.username) patch("smtpUser", "");
  };
  const editSMTPField = <K extends "smtpHost" | "smtpPort" | "smtpTls">(key: K, value: InstallDraft[K]) => {
    patch("smtpProvider", "custom");
    patch(key, value);
  };
  return <section class="workflow-section form-section"><label class="switch-row" htmlFor="registrationEnabled"><span><strong>开放插件注册</strong><small>默认关闭；开启后必须先验证 SMTP。</small></span><input id="registrationEnabled" type="checkbox" checked={draft.registrationEnabled} onChange={(e) => patch("registrationEnabled", e.currentTarget.checked)} /></label>{draft.registrationEnabled ? <><div class="form-grid"><label htmlFor="smtpProvider">邮箱服务商</label><select id="smtpProvider" value={provider} onChange={(e) => selectProvider(e.currentTarget.value as SMTPProviderId)}>{smtpProviderOptions.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}</select><label htmlFor="smtpHost">SMTP 主机</label><input id="smtpHost" value={draft.smtpHost} onInput={(e) => editSMTPField("smtpHost", e.currentTarget.value)} /><label htmlFor="smtpPort">端口</label><input id="smtpPort" inputMode="numeric" value={draft.smtpPort} onInput={(e) => editSMTPField("smtpPort", e.currentTarget.value)} /><label htmlFor="smtpTls">TLS</label><select id="smtpTls" value={draft.smtpTls} onChange={(e) => editSMTPField("smtpTls", e.currentTarget.value as InstallDraft["smtpTls"])}><option value="tls">直接 TLS</option><option value="starttls">STARTTLS</option><option value="none">无（仅开发）</option></select><label htmlFor="smtpFrom">发件人</label><input id="smtpFrom" type="email" value={draft.smtpFrom} onInput={(e) => patch("smtpFrom", e.currentTarget.value)} /><label htmlFor="smtpUser">用户名</label><input id="smtpUser" autoComplete="username" value={draft.smtpUser} readOnly={Boolean(providerPreset.username)} onInput={(e) => patch("smtpUser", e.currentTarget.value)} /><label htmlFor="smtpPassword">{providerPreset.passwordLabel ?? "密码"}</label><input id="smtpPassword" type="password" autoComplete="new-password" value={draft.smtpPassword} aria-describedby="smtp-provider-help" onInput={(e) => patch("smtpPassword", e.currentTarget.value)} /></div><p id="smtp-provider-help" class="field-help">{providerPreset.help}{providerPreset.credentialHelp ? <> {providerPreset.credentialHelp} {providerPreset.credentialUrl && providerPreset.credentialLinkLabel ? <a href={providerPreset.credentialUrl} target="_blank" rel="noopener noreferrer">{providerPreset.credentialLinkLabel}</a> : null}</> : null}</p><button id="smtpTest" class="button secondary" type="button" onClick={onTest} disabled={busy}>{verified ? <><MailCheck size={17} aria-hidden="true" />测试邮件已送达</> : "发送测试邮件"}</button></> : <div class="empty-state compact"><strong>注册保持关闭</strong><p>可跳过邮件配置，安装后在“安全与维护”中完成验证再开放。</p></div>}</section>;
}

function LimitsStep({ draft, patch }: { draft: InstallDraft; patch: PatchDraft }) {
  const fields: Array<[keyof InstallDraft, string, string]> = [["maxUsers", "最大用户数", "100"], ["profileKiB", "单配置上限（KiB）", "512"], ["storageGiB", "数据库软水位（GiB）", "1"], ["versionsPerUser", "每用户版本数", "50"], ["accessLogDays", "访问日志保留（天）", "30"], ["auditLogDays", "管理员审计保留（天）", "180"]];
  return <section class="workflow-section form-section"><div class="form-grid two-columns">{fields.map(([key, label, placeholder]) => <div class="field-block" key={key}><label htmlFor={key}>{label}</label><input id={key} inputMode="numeric" placeholder={placeholder} value={String(draft[key])} onInput={(e) => patch(key, e.currentTarget.value as never)} /></div>)}</div><label htmlFor="backupDirectory">备份目录</label><input id="backupDirectory" aria-describedby="backup-directory-help" value={draft.backupDirectory} onInput={(e) => patch("backupDirectory", e.currentTarget.value)} /><p id="backup-directory-help" class="field-help">Docker 正式部署请保持 `/backups`；启动环境变量会覆盖此处的持久值并验证目录可写。</p><p class="field-help">达到软水位 90% 时停止新注册并告警；100% 时拒绝会增长存储的写入。</p></section>;
}

function ReviewStep({ draft, mode }: { draft: InstallDraft; mode: InstallSession["mode"] }) {
  return <section class="workflow-section"><p class="page-lede">提交后将进入可恢复的两阶段安装。只有数据库、secrets 和安装标记全部落盘后才会显示成功。</p><dl class="review-list"><div><dt>模式</dt><dd>{mode === "admin_reset" ? "仅重建管理员，保留既有数据和设置" : "首次安装"}</dd></div><div><dt>管理员</dt><dd>{draft.displayName || "—"} · {draft.adminEmail || "—"}</dd></div>{mode === "fresh_install" ? <><div><dt>公网 API</dt><dd>{draft.publicBaseUrl || "仅本地配置"}</dd></div><div><dt>开放注册</dt><dd>{draft.registrationEnabled ? "开启，SMTP 已验证" : "关闭"}</dd></div><div><dt>容量</dt><dd>{draft.maxUsers} 用户 · {draft.profileKiB} KiB/配置 · {draft.storageGiB} GiB 软水位</dd></div><div><dt>隐私边界</dt><dd>本地图片、图标 Blob、凭据和设备运行状态不会上传</dd></div></> : null}</dl></section>;
}
