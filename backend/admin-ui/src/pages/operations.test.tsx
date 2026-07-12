import { fireEvent, render, screen, waitFor } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "../lib/api";
import { parseAdminLocation } from "../lib/router";
import { AuditPage, BackupsPage, ReleasesPage, SyncPage, SystemAreaPage, getOperationConfig } from "./operations";

function clientStub(overrides: Partial<ApiClient>): ApiClient {
  return { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn(), ...overrides } as unknown as ApiClient;
}

describe("operation route contracts", () => {
  it("maps every remaining canonical page to one route-local endpoint", () => {
    expect(getOperationConfig("sync-attempts")).toMatchObject({ title: "同步尝试", endpoint: "/api/admin/v1/sync/attempts" });
    expect(getOperationConfig("sync-conflicts")).toMatchObject({ title: "同步冲突", endpoint: "/api/admin/v1/sync/conflicts" });
    expect(getOperationConfig("admin-audit")).toMatchObject({ title: "管理员审计", endpoint: "/api/admin/v1/audit/admin" });
    expect(getOperationConfig("access-audit")).toMatchObject({ title: "HTTP 访问日志", endpoint: "/api/admin/v1/audit/access" });
    expect(getOperationConfig("security")).toMatchObject({ endpoint: "/api/admin/v1/system/settings" });
    expect(getOperationConfig("maintenance")).toMatchObject({ endpoint: "/api/admin/v1/system/maintenance/jobs" });
    expect(getOperationConfig("backups")).toMatchObject({ endpoint: "/api/admin/v1/system/backups" });
    expect(getOperationConfig("system")).toMatchObject({ endpoint: "/api/admin/v1/system/health" });
    expect(getOperationConfig("not-found")).toMatchObject({ eyebrow: "KeKeIO Tab" });
  });
});

describe("SyncPage", () => {
  it("renders request IDs without exposing profile content", async () => {
    const client = clientStub({ get: vi.fn().mockResolvedValue({ items: [{ id: "a-1", userEmail: "alice@example.com", status: "failed", code: "PROFILE_CONFLICT", requestId: "req-42", createdAt: "2026-07-12T01:00:00Z", durationMs: 187 }] }) });
    render(<SyncPage client={client} route={parseAdminLocation("/admin/sync/attempts?status=failed")} onNavigate={vi.fn()} />);

    expect(await screen.findByRole("heading", { name: "同步尝试" })).toBeInTheDocument();
    expect(screen.getByText("req-42")).toBeInTheDocument();
    expect(client.get).toHaveBeenCalledWith("/api/admin/v1/sync/attempts?status=failed", expect.any(AbortSignal));
  });
});

describe("ReleasesPage", () => {
  it("creates a draft instead of publishing directly", async () => {
    const post = vi.fn().mockResolvedValue({ item: { id: "release-1" } });
    const client = clientStub({ get: vi.fn().mockResolvedValue({ items: [] }), post });
    const notify = vi.fn();
    const user = userEvent.setup();
    render(<ReleasesPage client={client} notify={notify} />);

    await screen.findByRole("heading", { name: "版本发布" });
    await user.type(screen.getByLabelText("版本号"), "0.2.0");
    await user.selectOptions(screen.getByLabelText("通道"), "stable");
    await user.type(screen.getByLabelText("更新说明"), "可靠同步与新后台");
    await user.click(screen.getByRole("button", { name: "保存版本草稿" }));

    expect(post).toHaveBeenCalledWith("/api/admin/v1/releases", expect.objectContaining({ version: "0.2.0", channel: "stable", status: "draft" }));
    await waitFor(() => expect(screen.getByLabelText("版本号")).toHaveValue(""));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(notify).toHaveBeenCalledWith("版本草稿已保存");
  });

  it("publishes a draft only after explicit version confirmation", async () => {
    const post = vi.fn().mockResolvedValue({ item: { id: "release-1", status: "published" } });
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ items: [{ id: "release-1", version: "0.2.0", channel: "stable", status: "draft", notes: "Ready" }] }),
      post
    });
    const notify = vi.fn();
    const user = userEvent.setup();
    render(<ReleasesPage client={client} notify={notify} />);

    await user.click(await screen.findByRole("button", { name: "发布 0.2.0" }));
    expect(screen.getByRole("dialog", { name: "发布版本 0.2.0" })).toBeInTheDocument();
    expect(screen.getByText("发布后 stable 通道客户端将看到此版本公告。此操作会写入管理员审计。")).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "确认发布" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/admin/v1/releases/release-1/publish", {}));
    expect(notify).toHaveBeenCalledWith("版本 0.2.0 已发布");
  });

  it("disables a published release only after explicit version confirmation", async () => {
    const post = vi.fn().mockResolvedValue({ item: { id: "release-1", status: "disabled" } });
    const client = clientStub({
      get: vi.fn().mockResolvedValue({ items: [{ id: "release-1", version: "0.2.0", channel: "stable", status: "published", notes: "Ready" }] }),
      post
    });
    const notify = vi.fn();
    const user = userEvent.setup();
    render(<ReleasesPage client={client} notify={notify} />);

    await user.click(await screen.findByRole("button", { name: "停用 0.2.0" }));
    expect(screen.getByRole("dialog", { name: "停用版本 0.2.0" })).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "确认停用" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/admin/v1/releases/release-1/disable", {}));
    expect(notify).toHaveBeenCalledWith("版本 0.2.0 已停用");
  });

  it("loads and displays the immutable history for a selected release", async () => {
    const get = vi.fn()
      .mockResolvedValueOnce({ items: [{ id: "release-1", version: "0.2.0", channel: "stable", status: "published", notes: "Ready" }] })
      .mockResolvedValueOnce({ items: [{ id: "event-1", action: "publish", fromStatus: "draft", toStatus: "published", adminEmail: "admin@example.com", requestId: "req-1", createdAt: "2026-07-12T01:00:00Z" }] });
    const client = clientStub({ get });
    const user = userEvent.setup();
    render(<ReleasesPage client={client} notify={vi.fn()} />);

    await user.click(await screen.findByRole("button", { name: "查看 0.2.0 的历史" }));

    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/admin/v1/releases/release-1/history", expect.any(AbortSignal)));
    expect(await screen.findByRole("heading", { name: "0.2.0 版本历史" })).toBeInTheDocument();
    expect(screen.getByText("draft → published")).toBeInTheDocument();
    expect(screen.getByText(/req-1/)).toBeInTheDocument();
  });

  it("ignores a release history response that arrives after the history panel is closed", async () => {
    let resolveHistory!: (value: { items: Array<Record<string, string>> }) => void;
    const delayedHistory = new Promise<{ items: Array<Record<string, string>> }>((resolve) => { resolveHistory = resolve; });
    const get = vi.fn()
      .mockResolvedValueOnce({ items: [{ id: "release-1", version: "0.2.0", channel: "stable", status: "published", notes: "Ready" }] })
      .mockReturnValueOnce(delayedHistory);
    const user = userEvent.setup();
    render(<ReleasesPage client={clientStub({ get })} notify={vi.fn()} />);

    await user.click(await screen.findByRole("button", { name: "查看 0.2.0 的历史" }));
    expect(await screen.findByRole("heading", { name: "0.2.0 版本历史" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "关闭历史" }));
    expect(screen.queryByRole("heading", { name: "0.2.0 版本历史" })).not.toBeInTheDocument();

    resolveHistory({ items: [{ id: "event-late", action: "publish", fromStatus: "draft", toStatus: "published" }] });
    await waitFor(() => expect(screen.queryByRole("heading", { name: "0.2.0 版本历史" })).not.toBeInTheDocument());
  });
});

describe("audit and system pages", () => {
  it("keeps admin and access audit as distinct views", async () => {
    const client = clientStub({ get: vi.fn().mockResolvedValue({ items: [] }) });
    render(<AuditPage client={client} route={parseAdminLocation("/admin/audit/admin")} onNavigate={vi.fn()} />);
    expect(await screen.findByRole("heading", { name: "管理员审计" })).toBeInTheDocument();
    expect(client.get).toHaveBeenCalledWith("/api/admin/v1/audit/admin", expect.any(AbortSignal));
  });

  it("renders read-only startup configuration separately from editable settings", async () => {
    const client = clientStub({ get: vi.fn().mockResolvedValue({ startup: { listenAddress: "0.0.0.0:8080", adminCidrs: ["192.168.0.0/16"], trustedProxies: [] }, health: [] }) });
    render(<SystemAreaPage client={client} route={parseAdminLocation("/admin/system")} notify={vi.fn()} />);
    expect(await screen.findByRole("heading", { name: "系统状态" })).toBeInTheDocument();
    expect(screen.getByText("0.0.0.0:8080")).toBeInTheDocument();
    expect(screen.getByText("只能通过环境变量或 CLI 修改")).toBeInTheDocument();
  });

  it("submits only editable runtime settings returned by the form", async () => {
    const put = vi.fn().mockResolvedValue({ settings: {} });
    const client = clientStub({
      get: vi.fn().mockResolvedValue({
        settings: {
          registrationEnabled: false,
          publicBaseUrl: "https://sync.example.test",
          webOrigins: ["https://app.example.test"],
          maxUsers: 100,
          profileBytes: 524288,
          profileKiB: 512,
          storageBytes: 1073741824,
          storageGiB: 1,
          versionsPerUser: 50,
          accessLogDays: 30,
          auditLogDays: 180,
          backupDirectory: "/data/backups"
        }
      }),
      put
    });
    const user = userEvent.setup();
    render(<SystemAreaPage client={client} route={parseAdminLocation("/admin/security")} notify={vi.fn()} />);

    await screen.findByRole("heading", { name: "安全设置" });
    const publicBaseUrl = await screen.findByLabelText("公网 API URL");
    await waitFor(() => expect(publicBaseUrl).toHaveValue("https://sync.example.test"));
    expect(publicBaseUrl).toHaveAttribute("placeholder", "https://tab.kekeio.com");
    await user.clear(publicBaseUrl);
    await user.type(publicBaseUrl, "https://custom.example.test");
    await user.click(screen.getByRole("button", { name: "保存设置" }));

    expect(put).toHaveBeenCalledWith("/api/admin/v1/system/settings", {
      registrationEnabled: false,
      publicBaseUrl: "https://custom.example.test",
      webOrigins: "https://app.example.test",
      maxUsers: "100",
      profileKiB: "512",
      storageGiB: "1",
      versionsPerUser: "50",
      accessLogDays: "30",
      auditLogDays: "180"
    });
  });

  it("defaults an unconfigured SMTP form to Cloudflare without embedding a Token", async () => {
    const client = clientStub({
      get: vi.fn().mockResolvedValue({
        settings: {
          registrationEnabled: false,
          publicBaseUrl: "https://tab.kekeio.com",
          webOrigins: [],
          maxUsers: 100,
          profileKiB: 512,
          storageGiB: 1,
          versionsPerUser: 50,
          accessLogDays: 30,
          auditLogDays: 180
        }
      })
    });
    const user = userEvent.setup();
    render(<SystemAreaPage client={client} route={parseAdminLocation("/admin/security")} notify={vi.fn()} />);

    expect(await screen.findByLabelText("邮箱服务商")).toHaveValue("cloudflare");
    expect(screen.getByLabelText("SMTP 主机")).toHaveValue("smtp.mx.cloudflare.net");
    expect(screen.getByLabelText("SMTP 端口")).toHaveValue("465");
    expect(screen.getByLabelText("TLS")).toHaveValue("tls");
    expect(screen.getByLabelText("发件人")).toHaveValue("noreply@kekeio.com");
    expect(screen.getByLabelText("SMTP 用户名")).toHaveValue("api_token");
    expect(screen.getByLabelText("Cloudflare API Token（留空保留现有 Token）")).toHaveValue("");
    expect(screen.getByText(/Email Sending: Edit/)).toBeInTheDocument();
    const tokenLink = screen.getByRole("link", { name: "创建 Cloudflare API Token" });
    expect(tokenLink).toHaveAttribute("href", "https://dash.cloudflare.com/profile/api-tokens/");
    expect(tokenLink).toHaveAttribute("target", "_blank");
    expect(tokenLink).toHaveAttribute("rel", expect.stringContaining("noopener"));

    await user.selectOptions(screen.getByLabelText("邮箱服务商"), "gmail");
    expect(screen.queryByRole("link", { name: "创建 Cloudflare API Token" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("SMTP 密码（留空保留现有密码）")).toHaveValue("");
  });

  it("does not submit an unconfigured Cloudflare preset before an API Token is entered", async () => {
    const put = vi.fn().mockResolvedValue({ settings: {} });
    const client = clientStub({
      get: vi.fn().mockResolvedValue({
        settings: {
          registrationEnabled: false,
          publicBaseUrl: "https://tab.kekeio.com",
          webOrigins: [],
          maxUsers: 100,
          profileKiB: 512,
          storageGiB: 1,
          versionsPerUser: 50,
          accessLogDays: 30,
          auditLogDays: 180
        }
      }),
      put
    });
    const user = userEvent.setup();
    render(<SystemAreaPage client={client} route={parseAdminLocation("/admin/security")} notify={vi.fn()} />);

    await user.selectOptions(await screen.findByLabelText("邮箱服务商"), "qq");
    await user.selectOptions(screen.getByLabelText("邮箱服务商"), "cloudflare");
    await user.click(screen.getByRole("button", { name: "保存设置" }));

    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0]?.[1]).not.toHaveProperty("smtp");
  });

  it("tests SMTP settings before enabling registration and never requires the saved password to be re-entered", async () => {
    const post = vi.fn().mockResolvedValue({ verified: true });
    const put = vi.fn().mockResolvedValue({ settings: {} });
    const client = clientStub({
      get: vi.fn().mockResolvedValue({
        settings: {
          registrationEnabled: false,
          publicBaseUrl: "https://sync.example.test",
          webOrigins: [],
          maxUsers: 100,
          profileKiB: 512,
          accessLogDays: 30,
          auditLogDays: 180,
          smtp: { host: "smtp.example.test", port: 587, tls: "starttls", from: "fullpro@example.test", username: "mailer", passwordConfigured: true, verified: false }
        }
      }),
      post,
      put
    });
    const user = userEvent.setup();
    render(<SystemAreaPage client={client} route={parseAdminLocation("/admin/security")} notify={vi.fn()} />);

    await screen.findByDisplayValue("smtp.example.test");
    expect(screen.getByLabelText("SMTP 密码（留空保留现有密码）")).toHaveValue("");
    await user.type(screen.getByLabelText("测试收件人"), "owner@example.test");
    await user.click(screen.getByRole("button", { name: "发送测试邮件" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/admin/v1/system/settings/smtp-test", expect.objectContaining({
      host: "smtp.example.test", password: "", recipient: "owner@example.test"
    })));
    expect(post.mock.calls[0]?.[1]).not.toHaveProperty("provider");

    await user.click(screen.getByRole("checkbox", { name: /开放注册/ }));
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(put).toHaveBeenCalledWith("/api/admin/v1/system/settings", expect.objectContaining({
      registrationEnabled: true,
      smtp: expect.objectContaining({ host: "smtp.example.test", password: "" })
    })));
    expect((put.mock.calls[0]?.[1] as { smtp?: object }).smtp).not.toHaveProperty("provider");
  });

  it("applies a Gmail SMTP preset while preserving saved credentials and the test recipient", async () => {
    const client = clientStub({
      get: vi.fn().mockResolvedValue({
        settings: {
          registrationEnabled: false,
          publicBaseUrl: "https://tab.kekeio.com",
          webOrigins: [],
          maxUsers: 100,
          profileKiB: 512,
          accessLogDays: 30,
          auditLogDays: 180,
          smtp: { host: "smtp.custom.example", port: 2525, tls: "tls", from: "sender@example.com", username: "saved-account", passwordConfigured: true, verified: true }
        }
      })
    });
    const user = userEvent.setup();
    render(<SystemAreaPage client={client} route={parseAdminLocation("/admin/security")} notify={vi.fn()} />);

    await screen.findByDisplayValue("smtp.custom.example");
    await user.type(screen.getByLabelText("测试收件人"), "owner@example.com");
    await user.selectOptions(screen.getByLabelText("邮箱服务商"), "gmail");

    expect(screen.getByLabelText("SMTP 主机")).toHaveValue("smtp.gmail.com");
    expect(screen.getByLabelText("SMTP 端口")).toHaveValue("587");
    expect(screen.getByLabelText("TLS")).toHaveValue("starttls");
    expect(screen.getByLabelText("发件人")).toHaveValue("sender@example.com");
    expect(screen.getByLabelText("SMTP 用户名")).toHaveValue("saved-account");
    expect(screen.getByLabelText("SMTP 密码（留空保留现有密码）")).toHaveValue("");
    expect(screen.getByLabelText("测试收件人")).toHaveValue("owner@example.com");

    await user.selectOptions(screen.getByLabelText("邮箱服务商"), "custom");
    expect(screen.getByLabelText("SMTP 主机")).toHaveValue("smtp.gmail.com");
    expect(screen.getByLabelText("SMTP 用户名")).toHaveValue("saved-account");

    await user.selectOptions(screen.getByLabelText("邮箱服务商"), "gmail");
    await user.clear(screen.getByLabelText("SMTP 端口"));
    await user.type(screen.getByLabelText("SMTP 端口"), "2525");
    expect(screen.getByLabelText("邮箱服务商")).toHaveValue("custom");
    expect(screen.getByLabelText("SMTP 用户名")).toHaveValue("saved-account");
  });

  it("does not mark edited SMTP values as verified by an earlier test response", async () => {
    let resolveTest!: (value: { verified: true }) => void;
    const delayedTest = new Promise<{ verified: true }>((resolve) => { resolveTest = resolve; });
    const post = vi.fn().mockReturnValue(delayedTest);
    const notify = vi.fn();
    const client = clientStub({
      get: vi.fn().mockResolvedValue({
        settings: {
          registrationEnabled: false,
          publicBaseUrl: "https://sync.example.test",
          webOrigins: [],
          maxUsers: 100,
          profileKiB: 512,
          storageGiB: 1,
          versionsPerUser: 50,
          accessLogDays: 30,
          auditLogDays: 180,
          smtp: { host: "smtp.example.test", port: 587, tls: "starttls", from: "fullpro@example.test", username: "mailer", passwordConfigured: true, verified: false }
        }
      }),
      post
    });
    const user = userEvent.setup();
    render(<SystemAreaPage client={client} route={parseAdminLocation("/admin/security")} notify={notify} />);

    await screen.findByDisplayValue("smtp.example.test");
    await user.type(screen.getByLabelText("测试收件人"), "owner@example.test");
    await user.click(screen.getByRole("button", { name: "发送测试邮件" }));
    await waitFor(() => expect(post).toHaveBeenCalledOnce());
    fireEvent.input(screen.getByLabelText("SMTP 主机"), { target: { value: "smtp.changed.example.test" } });
    resolveTest({ verified: true });

    await waitFor(() => expect(notify).toHaveBeenCalledWith("SMTP 配置在测试期间已修改，请重新测试", "error"));
    expect(screen.getByText("待测试")).toBeInTheDocument();
    expect(screen.queryByText("已验证")).not.toBeInTheDocument();
  });
});

describe("BackupsPage", () => {
  it("encrypts full backups with a user-entered recovery passphrase", async () => {
    const post = vi.fn().mockResolvedValue({ item: { id: "backup-2", kind: "full", status: "ready" } });
    const client = clientStub({ get: vi.fn().mockResolvedValue({ items: [] }), post });
    const user = userEvent.setup();
    render(<BackupsPage client={client} notify={vi.fn()} />);

    await user.click(await screen.findByRole("button", { name: "创建完整备份" }));
    expect(screen.getByRole("dialog", { name: "创建加密完整备份" })).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
    await user.type(screen.getByLabelText("备份恢复口令"), "correct recovery passphrase");
    await user.type(screen.getByLabelText("再次输入备份恢复口令"), "correct recovery passphrase");
    await user.click(screen.getByRole("button", { name: "创建加密备份" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/admin/v1/system/backups", { kind: "full", passphrase: "correct recovery passphrase" }));
  });

  it("does not create a full backup when the two recovery passphrases differ", async () => {
    const post = vi.fn();
    const notify = vi.fn();
    const client = clientStub({ get: vi.fn().mockResolvedValue({ items: [] }), post });
    const user = userEvent.setup();
    render(<BackupsPage client={client} notify={notify} />);

    await user.click(await screen.findByRole("button", { name: "创建完整备份" }));
    await user.type(screen.getByLabelText("备份恢复口令"), "correct recovery passphrase");
    await user.type(screen.getByLabelText("再次输入备份恢复口令"), "mistyped recovery passphrase");
    await user.click(screen.getByRole("button", { name: "创建加密备份" }));

    expect(post).not.toHaveBeenCalled();
    expect(notify).toHaveBeenCalledWith("两次输入的备份恢复口令不一致", "error");
  });

  it("requires explicit confirmation before restore", async () => {
    const client = clientStub({ get: vi.fn().mockResolvedValue({ items: [{ id: "backup-1", kind: "full", status: "ready", createdAt: "2026-07-12T01:00:00Z", sizeBytes: 1024 }] }), post: vi.fn() });
    const user = userEvent.setup();
    render(<BackupsPage client={client} notify={vi.fn()} />);
    await screen.findByRole("heading", { name: "备份与恢复" });
    await user.click(screen.getByRole("button", { name: "恢复 backup-1" }));
    expect(screen.getByRole("dialog", { name: "恢复备份" })).toBeInTheDocument();
    expect(client.post).not.toHaveBeenCalled();
  });

  it("labels a ready backup as recoverable instead of pending publication", async () => {
    const client = clientStub({ get: vi.fn().mockResolvedValue({ items: [{ id: "backup-ready", kind: "data-only", status: "ready" }] }) });
    render(<BackupsPage client={client} notify={vi.fn()} />);

    expect(await screen.findByText("可恢复")).toBeInTheDocument();
    expect(screen.queryByText("待发布")).not.toBeInTheDocument();
  });
});
