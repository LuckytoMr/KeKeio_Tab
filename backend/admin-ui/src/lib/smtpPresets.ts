export type SMTPTlsMode = "tls" | "starttls" | "none";
export type SMTPProviderId = "qq" | "netease163" | "gmail" | "custom";

export interface SMTPProviderOption {
  id: SMTPProviderId;
  label: string;
  host: string;
  port: string;
  tls: SMTPTlsMode;
  help: string;
}

const customSMTPProvider: SMTPProviderOption = {
  id: "custom",
  label: "自定义 SMTP",
  host: "",
  port: "587",
  tls: "starttls",
  help: "保留当前 SMTP 参数，请根据你的邮件服务商文档手动填写。"
};

export const smtpProviderOptions: readonly SMTPProviderOption[] = [
  {
    id: "qq",
    label: "QQ 邮箱",
    host: "smtp.qq.com",
    port: "465",
    tls: "tls",
    help: "请先在 QQ 邮箱中开启 SMTP 服务，并填写邮箱授权码，不要填写 QQ 登录密码。"
  },
  {
    id: "netease163",
    label: "网易 163 邮箱",
    host: "smtp.163.com",
    port: "465",
    tls: "tls",
    help: "请先在网易邮箱中开启 SMTP 服务，并填写客户端授权码，不要填写邮箱登录密码。"
  },
  {
    id: "gmail",
    label: "Google Gmail",
    host: "smtp.gmail.com",
    port: "587",
    tls: "starttls",
    help: "Google 账号通常需要使用应用专用密码；此预设使用 587 端口和 STARTTLS。"
  },
  customSMTPProvider
];

export function getSMTPPreset(id: SMTPProviderId): SMTPProviderOption {
  return smtpProviderOptions.find((provider) => provider.id === id) ?? customSMTPProvider;
}

export function detectSMTPProvider({ host, port, tls }: { host: string; port: string | number; tls: SMTPTlsMode }): SMTPProviderId {
  const normalizedHost = host.trim().toLowerCase();
  const normalizedPort = String(port).trim();
  return smtpProviderOptions.find((provider) => (
    provider.id !== "custom"
    && provider.host === normalizedHost
    && provider.port === normalizedPort
    && provider.tls === tls
  ))?.id ?? "custom";
}
