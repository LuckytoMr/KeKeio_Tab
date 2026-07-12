import { render, screen, waitFor } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "../lib/api";
import { ApiError } from "../lib/api";
import { InstallWizard, LoginPage, serializeInstallProgress, validateInstallStepInput } from "./auth";

function clientStub(overrides: Partial<ApiClient>): ApiClient {
  return {
    get: vi.fn(),
    post: vi.fn(),
    setCsrfToken: vi.fn(),
    ...overrides
  } as unknown as ApiClient;
}

describe("LoginPage", () => {
  it("uses the v1 admin login and returns the authenticated session", async () => {
    const session = { user: { id: "admin-1", email: "admin@example.com", displayName: "站点管理员" }, csrfToken: "csrf-admin" };
    const client = clientStub({ post: vi.fn().mockResolvedValue(session) });
    const onAuthenticated = vi.fn();
    const user = userEvent.setup();
    render(<LoginPage client={client} preAuthCsrf="preauth" onAuthenticated={onAuthenticated} />);

    await user.type(screen.getByLabelText("管理员邮箱"), "admin@example.com");
    await user.type(screen.getByLabelText("密码"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "登录后台" }));

    expect(client.post).toHaveBeenCalledWith("/api/admin/v1/auth/login", {
      email: "admin@example.com",
      password: "correct horse battery staple"
    });
    expect(onAuthenticated).toHaveBeenCalledWith(session);
  });

  it("keeps a failed login in a focused persistent error summary", async () => {
    const client = clientStub({
      post: vi.fn().mockRejectedValue(new ApiError({ status: 401, code: "INVALID_CREDENTIALS", message: "邮箱或密码不正确" }))
    });
    const user = userEvent.setup();
    render(<LoginPage client={client} preAuthCsrf="preauth" onAuthenticated={vi.fn()} />);

    await user.type(screen.getByLabelText("管理员邮箱"), "admin@example.com");
    await user.type(screen.getByLabelText("密码"), "wrong password");
    await user.click(screen.getByRole("button", { name: "登录后台" }));

    const alert = await screen.findByRole("alert", { name: "登录失败" });
    expect(alert).toHaveFocus();
    expect(alert).toHaveTextContent("邮箱或密码不正确");
  });
});

describe("InstallWizard", () => {
  it("starts a fresh install with the KeKeIO Tab public URL", async () => {
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ state: "uninitialized", mode: "fresh_install", requiresCode: true })
    });

    render(<InstallWizard client={client} onInstalled={vi.fn()} />);

    await screen.findByRole("heading", { name: "验证安装码" });
    await waitFor(() => {
      const progress = JSON.parse(window.sessionStorage.getItem("fullpro:install:draft") || "{}");
      expect(progress.draft?.publicBaseUrl).toBe("https://tab.kekeio.com");
      expect(progress.draft).toMatchObject({
        smtpProvider: "cloudflare",
        smtpHost: "smtp.mx.cloudflare.net",
        smtpPort: "465",
        smtpTls: "tls",
        smtpFrom: "noreply@kekeio.com",
        smtpUser: "api_token"
      });
      expect(progress.draft).not.toHaveProperty("smtpPassword");
    });
  });

  it("links the default Cloudflare API Token field to the token dashboard", async () => {
    const post = vi.fn().mockImplementation((path: string) => {
      if (path === "/install/api/v1/session") return Promise.resolve({ mode: "fresh_install", csrfToken: "csrf-install" });
      if (path === "/install/api/v1/preflight") return Promise.resolve({ checks: [{ label: "数据目录", status: "pass" }] });
      return Promise.reject(new Error(`unexpected POST ${path}`));
    });
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ state: "uninitialized", mode: "fresh_install", requiresCode: true }),
      post: post as unknown as ApiClient["post"]
    });
    const user = userEvent.setup();
    render(<InstallWizard client={client} onInstalled={vi.fn()} />);

    await user.type(await screen.findByLabelText("一次性安装码"), "ABCD-EFGH");
    await user.click(screen.getByRole("button", { name: "建立安全安装会话" }));
    await user.click(await screen.findByRole("button", { name: "运行环境检查" }));
    await user.click(await screen.findByRole("button", { name: "继续" }));
    await user.type(screen.getByLabelText("管理员邮箱"), "admin@kekeio.com");
    await user.type(screen.getByLabelText("显示名"), "管理员");
    await user.type(screen.getByLabelText("密码"), "correct horse battery staple");
    await user.type(screen.getByLabelText("再次输入密码"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "继续" }));
    await user.click(screen.getByRole("button", { name: "继续" }));
    await user.click(screen.getByRole("checkbox", { name: /开放插件注册/ }));

    expect(screen.getByLabelText("邮箱服务商")).toHaveValue("cloudflare");
    expect(screen.getByLabelText("SMTP 主机")).toHaveValue("smtp.mx.cloudflare.net");
    expect(screen.getByLabelText("用户名")).toHaveValue("api_token");
    expect(screen.getByLabelText("Cloudflare API Token")).toHaveValue("");
    expect(screen.getByText(/Email Sending: Edit/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "创建 Cloudflare API Token" })).toHaveAttribute(
      "href",
      "https://dash.cloudflare.com/profile/api-tokens/"
    );

    await user.selectOptions(screen.getByLabelText("邮箱服务商"), "qq");
    expect(screen.queryByRole("link", { name: "创建 Cloudflare API Token" })).not.toBeInTheDocument();
  });

  it("upgrades an old blank public URL draft to the default", async () => {
    window.sessionStorage.setItem("fullpro:install:draft", serializeInstallProgress({
      adminEmail: "admin@example.com", displayName: "管理员", password: "", passwordConfirm: "",
      publicBaseUrl: "", extensionIds: "extension-id", webOrigins: "", registrationEnabled: false,
      smtpHost: "", smtpPort: "587", smtpTls: "starttls", smtpFrom: "", smtpUser: "", smtpPassword: "",
      maxUsers: "100", profileKiB: "512", storageGiB: "1", versionsPerUser: "50", accessLogDays: "30", auditLogDays: "180", backupDirectory: "/backups"
    }, 0));
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ state: "uninitialized", mode: "fresh_install", requiresCode: true })
    });

    render(<InstallWizard client={client} onInstalled={vi.fn()} />);

    await screen.findByRole("heading", { name: "验证安装码" });
    await waitFor(() => {
      const progress = JSON.parse(window.sessionStorage.getItem("fullpro:install:draft") || "{}");
      expect(progress.draft?.publicBaseUrl).toBe("https://tab.kekeio.com");
      expect(progress.draft).toMatchObject({
        smtpProvider: "cloudflare",
        smtpHost: "smtp.mx.cloudflare.net",
        smtpPort: "465",
        smtpTls: "tls",
        smtpFrom: "noreply@kekeio.com",
        smtpUser: "api_token"
      });
    });
  });

  it("requires an absolute HTTPS public API URL for fresh installs", () => {
    const draft = JSON.parse(serializeInstallProgress({
      adminEmail: "admin@example.com", displayName: "管理员", password: "abcdefghijkl", passwordConfirm: "abcdefghijkl",
      publicBaseUrl: "", extensionIds: "extension-id", webOrigins: "", registrationEnabled: false,
      smtpHost: "", smtpPort: "587", smtpTls: "starttls", smtpFrom: "", smtpUser: "", smtpPassword: "",
      maxUsers: "100", profileKiB: "512", storageGiB: "1", versionsPerUser: "50", accessLogDays: "30", auditLogDays: "180", backupDirectory: "/backups"
    }, 0)).draft;

    expect(validateInstallStepInput("公网 API", draft, "fresh_install", false, true)).toEqual({
      publicBaseUrl: "请输入绝对 HTTPS 公网 API 地址"
    });
    expect(validateInstallStepInput("公网 API", draft, "admin_reset", false, true)).toEqual({});
  });

  it("persists only the resumable non-sensitive draft and current step", () => {
    const serialized = serializeInstallProgress(
      {
        adminEmail: "admin@example.com",
        displayName: "管理员",
        password: "ADMIN-SECRET-123",
        passwordConfirm: "ADMIN-SECRET-123",
        publicBaseUrl: "https://fullpro.example.com",
        extensionIds: "extension-id",
        webOrigins: "",
        registrationEnabled: true,
        smtpHost: "smtp.example.com",
        smtpPort: "587",
        smtpTls: "starttls",
        smtpFrom: "admin@example.com",
        smtpUser: "mailer",
        smtpPassword: "SMTP-SECRET-456",
        maxUsers: "100",
        profileKiB: "512",
        storageGiB: "1",
        versionsPerUser: "50",
        accessLogDays: "30",
        auditLogDays: "180",
        backupDirectory: "/backups"
      },
      4
    );

    expect(JSON.parse(serialized)).toMatchObject({
      stepIndex: 4,
      draft: { adminEmail: "admin@example.com", smtpHost: "smtp.example.com" }
    });
    expect(serialized).not.toContain("ADMIN-SECRET-123");
    expect(serialized).not.toContain("SMTP-SECRET-456");
    expect(serialized).not.toContain("installCode");
    expect(serialized).not.toContain("csrf");
    expect(JSON.parse(serialized).draft.publicBaseUrl).toBe("https://fullpro.example.com");
  });

  it("treats the install code session as the first explicit step", async () => {
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ state: "uninitialized", mode: "fresh_install", requiresCode: true }),
      post: vi.fn().mockResolvedValue({ mode: "fresh_install", csrfToken: "csrf-install", expiresAt: "2026-07-12T10:00:00Z" })
    });
    const user = userEvent.setup();
    render(<InstallWizard client={client} onInstalled={vi.fn()} />);

    expect(await screen.findByRole("heading", { name: "验证安装码" })).toBeInTheDocument();
    await user.type(screen.getByLabelText("一次性安装码"), "ABCD-EFGH-IJKL");
    await user.click(screen.getByRole("button", { name: "建立安全安装会话" }));

    expect(client.post).toHaveBeenCalledWith("/install/api/v1/session", { installCode: "ABCD-EFGH-IJKL" });
    await waitFor(() => expect(screen.getByRole("heading", { name: "环境检查" })).toBeInTheDocument());
    expect(screen.getByText("KeKeIO Tab")).toBeInTheDocument();
    expect(screen.getByText("KT")).toBeInTheDocument();
    expect(client.setCsrfToken).toHaveBeenCalledWith("csrf-install");
  });

  it("preserves draft fields but restarts safety checks for every new install session", async () => {
    window.sessionStorage.setItem("fullpro:install:draft", serializeInstallProgress({
      adminEmail: "admin@example.com", displayName: "管理员", password: "", passwordConfirm: "",
      publicBaseUrl: "https://fullpro.example.com", extensionIds: "extension-id", webOrigins: "", registrationEnabled: false,
      smtpHost: "smtp.example.com", smtpPort: "587", smtpTls: "starttls", smtpFrom: "admin@example.com", smtpUser: "mailer", smtpPassword: "",
      maxUsers: "100", profileKiB: "512", storageGiB: "1", versionsPerUser: "50", accessLogDays: "30", auditLogDays: "180", backupDirectory: "/backups"
    }, 5));
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ state: "uninitialized", mode: "fresh_install", requiresCode: true }),
      post: vi.fn().mockResolvedValue({ mode: "fresh_install", csrfToken: "csrf-install" })
    });
    const user = userEvent.setup();
    render(<InstallWizard client={client} onInstalled={vi.fn()} />);

    await user.type(await screen.findByLabelText("一次性安装码"), "ABCD-EFGH");
    await user.click(screen.getByRole("button", { name: "建立安全安装会话" }));

    expect(await screen.findByRole("heading", { name: "环境检查" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "确认并完成安装" })).not.toBeInTheDocument();
    expect(window.sessionStorage.getItem("fullpro:install:draft")).toContain("admin@example.com");
    expect(JSON.parse(window.sessionStorage.getItem("fullpro:install:draft") || "{}").draft?.publicBaseUrl).toBe("https://fullpro.example.com");
  });

  it("clears an expired install session and returns to install-code verification", async () => {
    const post = vi
      .fn()
      .mockResolvedValueOnce({ mode: "fresh_install", csrfToken: "csrf-install" })
      .mockRejectedValueOnce(new ApiError({ status: 401, code: "INSTALL_SESSION_EXPIRED", message: "安装会话已过期" }));
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ state: "uninitialized", mode: "fresh_install", requiresCode: true }),
      post
    });
    const user = userEvent.setup();
    render(<InstallWizard client={client} onInstalled={vi.fn()} />);

    await user.type(await screen.findByLabelText("一次性安装码"), "ABCD-EFGH");
    await user.click(screen.getByRole("button", { name: "建立安全安装会话" }));
    await user.click(await screen.findByRole("button", { name: "运行环境检查" }));

    expect(await screen.findByRole("heading", { name: "验证安装码" })).toBeInTheDocument();
    expect(screen.getByRole("alert", { name: "请修正以下问题" })).toHaveTextContent("安装会话已过期");
    expect(client.setCsrfToken).toHaveBeenLastCalledWith(null);
  });

  it("applies a QQ SMTP preset without clearing account credentials", async () => {
    window.sessionStorage.setItem("fullpro:install:draft", serializeInstallProgress({
      adminEmail: "admin@example.com", displayName: "管理员", password: "", passwordConfirm: "",
      publicBaseUrl: "https://tab.kekeio.com", extensionIds: "extension-id", webOrigins: "", registrationEnabled: true,
      smtpHost: "smtp.custom.example", smtpPort: "2525", smtpTls: "starttls", smtpFrom: "sender@example.com", smtpUser: "sender-account", smtpPassword: "",
      maxUsers: "100", profileKiB: "512", storageGiB: "1", versionsPerUser: "50", accessLogDays: "30", auditLogDays: "180", backupDirectory: "/backups"
    }, 0));
    const post = vi.fn().mockImplementation((path: string) => {
      if (path === "/install/api/v1/session") return Promise.resolve({ mode: "fresh_install", csrfToken: "csrf-install" });
      if (path === "/install/api/v1/preflight") return Promise.resolve({ checks: [{ label: "数据目录", status: "pass" }] });
      return Promise.reject(new Error(`unexpected POST ${path}`));
    });
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ state: "uninitialized", mode: "fresh_install", requiresCode: true }),
      post: post as unknown as ApiClient["post"]
    });
    const user = userEvent.setup();
    render(<InstallWizard client={client} onInstalled={vi.fn()} />);

    await user.type(await screen.findByLabelText("一次性安装码"), "ABCD-EFGH");
    await user.click(screen.getByRole("button", { name: "建立安全安装会话" }));
    await user.click(await screen.findByRole("button", { name: "运行环境检查" }));
    await user.click(await screen.findByRole("button", { name: "继续" }));
    await user.type(screen.getByLabelText("密码"), "correct horse battery staple");
    await user.type(screen.getByLabelText("再次输入密码"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "继续" }));
    await user.click(screen.getByRole("button", { name: "继续" }));

    await user.type(screen.getByLabelText("密码"), "AUTH-CODE-123");
    await user.selectOptions(screen.getByLabelText("邮箱服务商"), "qq");
    expect(screen.getByLabelText("SMTP 主机")).toHaveValue("smtp.qq.com");
    expect(screen.getByLabelText("端口")).toHaveValue("465");
    expect(screen.getByLabelText("TLS")).toHaveValue("tls");
    expect(screen.getByLabelText("发件人")).toHaveValue("sender@example.com");
    expect(screen.getByLabelText("用户名")).toHaveValue("sender-account");
    expect(screen.getByLabelText("密码")).toHaveValue("AUTH-CODE-123");

    await user.clear(screen.getByLabelText("端口"));
    await user.type(screen.getByLabelText("端口"), "2525");
    expect(screen.getByLabelText("邮箱服务商")).toHaveValue("custom");
  });

  it("does not accept a stale SMTP test result after the provider changes", async () => {
    window.sessionStorage.setItem("fullpro:install:draft", serializeInstallProgress({
      adminEmail: "admin@example.com", displayName: "管理员", password: "", passwordConfirm: "",
      publicBaseUrl: "https://tab.kekeio.com", extensionIds: "extension-id", webOrigins: "", registrationEnabled: true,
      smtpHost: "smtp.custom.example", smtpPort: "2525", smtpTls: "starttls", smtpFrom: "sender@example.com", smtpUser: "sender-account", smtpPassword: "",
      maxUsers: "100", profileKiB: "512", storageGiB: "1", versionsPerUser: "50", accessLogDays: "30", auditLogDays: "180", backupDirectory: "/backups"
    }, 0));
    let resolveSMTPTest!: (value: { verified: true }) => void;
    const delayedSMTPTest = new Promise<{ verified: true }>((resolve) => { resolveSMTPTest = resolve; });
    const post = vi.fn().mockImplementation((path: string) => {
      if (path === "/install/api/v1/session") return Promise.resolve({ mode: "fresh_install", csrfToken: "csrf-install" });
      if (path === "/install/api/v1/preflight") return Promise.resolve({ checks: [{ label: "数据目录", status: "pass" }] });
      if (path === "/install/api/v1/smtp-test") return delayedSMTPTest;
      return Promise.reject(new Error(`unexpected POST ${path}`));
    });
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ state: "uninitialized", mode: "fresh_install", requiresCode: true }),
      post: post as unknown as ApiClient["post"]
    });
    const user = userEvent.setup();
    render(<InstallWizard client={client} onInstalled={vi.fn()} />);

    await user.type(await screen.findByLabelText("一次性安装码"), "ABCD-EFGH");
    await user.click(screen.getByRole("button", { name: "建立安全安装会话" }));
    await user.click(await screen.findByRole("button", { name: "运行环境检查" }));
    await user.click(await screen.findByRole("button", { name: "继续" }));
    await user.type(screen.getByLabelText("密码"), "correct horse battery staple");
    await user.type(screen.getByLabelText("再次输入密码"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "继续" }));
    await user.click(screen.getByRole("button", { name: "继续" }));

    const smtpTestButton = screen.getByRole("button", { name: "发送测试邮件" });
    await user.click(smtpTestButton);
    await waitFor(() => expect(post).toHaveBeenCalledWith("/install/api/v1/smtp-test", expect.any(Object)));
    await user.selectOptions(screen.getByLabelText("邮箱服务商"), "qq");
    resolveSMTPTest({ verified: true });

    await waitFor(() => expect(smtpTestButton).not.toBeDisabled());
    expect(smtpTestButton).toHaveTextContent("发送测试邮件");
  });

  it("invalidates SMTP verification immediately for SMTP or recipient edits but not quota edits", async () => {
    window.sessionStorage.setItem("fullpro:install:draft", serializeInstallProgress({
      adminEmail: "admin@example.com", displayName: "管理员", password: "", passwordConfirm: "",
      publicBaseUrl: "https://fullpro.example.com", extensionIds: "extension-id", webOrigins: "", registrationEnabled: true,
      smtpHost: "smtp.example.com", smtpPort: "587", smtpTls: "starttls", smtpFrom: "admin@example.com", smtpUser: "", smtpPassword: "",
      maxUsers: "100", profileKiB: "512", storageGiB: "1", versionsPerUser: "50", accessLogDays: "30", auditLogDays: "180", backupDirectory: "/backups"
    }, 0));
    const post = vi.fn().mockImplementation((path: string) => {
      if (path === "/install/api/v1/session") return Promise.resolve({ mode: "fresh_install", csrfToken: "csrf-install" });
      if (path === "/install/api/v1/preflight") return Promise.resolve({ checks: [{ label: "数据目录", status: "pass" }] });
      if (path === "/install/api/v1/smtp-test") return Promise.resolve({ verified: true });
      return Promise.reject(new Error(`unexpected POST ${path}`));
    });
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ state: "uninitialized", mode: "fresh_install", requiresCode: true }),
      post: post as unknown as ApiClient["post"]
    });
    const user = userEvent.setup();
    render(<InstallWizard client={client} onInstalled={vi.fn()} />);

    await user.type(await screen.findByLabelText("一次性安装码"), "ABCD-EFGH");
    await user.click(screen.getByRole("button", { name: "建立安全安装会话" }));
    await user.click(await screen.findByRole("button", { name: "运行环境检查" }));
    await user.click(await screen.findByRole("button", { name: "继续" }));
    await user.type(screen.getByLabelText("密码"), "correct horse battery staple");
    await user.type(screen.getByLabelText("再次输入密码"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "继续" }));
    await user.click(screen.getByRole("button", { name: "继续" }));

    await user.click(await screen.findByRole("button", { name: "发送测试邮件" }));
    expect(await screen.findByRole("button", { name: "测试邮件已送达" })).toBeInTheDocument();
    await user.clear(screen.getByLabelText("SMTP 主机"));
    await user.type(screen.getByLabelText("SMTP 主机"), "smtp.changed.example.com");
    expect(screen.getByRole("button", { name: "发送测试邮件" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "发送测试邮件" }));
    expect(await screen.findByRole("button", { name: "测试邮件已送达" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "上一步" }));
    await user.click(screen.getByRole("button", { name: "上一步" }));
    await user.clear(screen.getByLabelText("管理员邮箱"));
    await user.type(screen.getByLabelText("管理员邮箱"), "new-admin@example.com");
    await user.click(screen.getByRole("button", { name: "继续" }));
    await user.click(screen.getByRole("button", { name: "继续" }));
    expect(await screen.findByRole("button", { name: "发送测试邮件" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "发送测试邮件" }));
    expect(await screen.findByRole("button", { name: "测试邮件已送达" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "继续" }));
    await user.clear(screen.getByLabelText("最大用户数"));
    await user.type(screen.getByLabelText("最大用户数"), "250");
    await user.click(screen.getByRole("button", { name: "上一步" }));
    expect(await screen.findByRole("button", { name: "测试邮件已送达" })).toBeInTheDocument();
  });
});
