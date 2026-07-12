import { describe, expect, it } from "vitest";
import { detectSMTPProvider, getSMTPPreset, smtpProviderOptions } from "./smtpPresets";

describe("SMTP provider presets", () => {
  it("publishes the supported providers in the intended order", () => {
    expect(smtpProviderOptions.map(({ id, label }) => ({ id, label }))).toEqual([
      { id: "qq", label: "QQ 邮箱" },
      { id: "netease163", label: "网易 163 邮箱" },
      { id: "gmail", label: "Google Gmail" },
      { id: "custom", label: "自定义 SMTP" }
    ]);
  });

  it.each([
    ["qq", "smtp.qq.com", "465", "tls"],
    ["netease163", "smtp.163.com", "465", "tls"],
    ["gmail", "smtp.gmail.com", "587", "starttls"]
  ] as const)("maps %s to its SMTP connection settings", (id, host, port, tls) => {
    expect(getSMTPPreset(id)).toMatchObject({ host, port, tls });
    expect(detectSMTPProvider({ host: host.toUpperCase(), port: Number(port), tls })).toBe(id);
  });

  it("treats partially matching or unknown settings as custom", () => {
    expect(detectSMTPProvider({ host: "smtp.gmail.com", port: 465, tls: "tls" })).toBe("custom");
    expect(detectSMTPProvider({ host: "smtp.example.com", port: 587, tls: "starttls" })).toBe("custom");
  });
});
