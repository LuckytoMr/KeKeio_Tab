import { describe, expect, it } from "vitest";
import { validateBackendAuthForm } from "./authForm";

describe("validateBackendAuthForm", () => {
  it("allows the four-character local development password for login", () => {
    expect(validateBackendAuthForm({ mode: "login", email: "user@local.test", password: "2231" })).toBeNull();
  });

  it("keeps registration aligned with the server's eight-character requirement", () => {
    expect(validateBackendAuthForm({ mode: "register", email: "user@local.test", password: "2231" })).toEqual({
      field: "password",
      message: "注册密码至少需要 8 位。"
    });
  });

  it("explains that an email address is required before any network request", () => {
    expect(validateBackendAuthForm({ mode: "login", email: "   ", password: "2231" })).toEqual({
      field: "email",
      message: "请填写邮箱后再登录。"
    });
  });
});
