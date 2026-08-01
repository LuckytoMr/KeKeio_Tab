import { describe, expect, it } from "vitest";
import { minimumPluginPasswordLength, validateBackendAuthForm } from "./authForm";

describe("validateBackendAuthForm", () => {
  it("fixes the plugin password minimum at four Unicode characters", () => {
    expect(minimumPluginPasswordLength).toBe(4);
    expect(validateBackendAuthForm({ mode: "login", email: "user@local.test", password: "2231" })).toBeNull();
    expect(validateBackendAuthForm({ mode: "register", email: "user@local.test", password: "2231" })).toBeNull();
    expect(validateBackendAuthForm({ mode: "register", email: "user@local.test", password: "密码四位" })).toBeNull();
  });

  it("rejects login and registration passwords shorter than four Unicode characters", () => {
    expect(validateBackendAuthForm({ mode: "login", email: "user@local.test", password: "223" })).toEqual({
      field: "password",
      message: "密码至少需要 4 位。"
    });
    expect(validateBackendAuthForm({ mode: "register", email: "user@local.test", password: "🔐🔐🔐" })).toEqual({
      field: "password",
      message: "密码至少需要 4 位。"
    });
  });

  it("explains that an email address is required before any network request", () => {
    expect(validateBackendAuthForm({ mode: "login", email: "   ", password: "2231" })).toEqual({
      field: "email",
      message: "请填写邮箱后再登录。"
    });
  });
});
