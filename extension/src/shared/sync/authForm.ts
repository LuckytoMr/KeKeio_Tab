export type BackendAuthMode = "login" | "register";

export type BackendAuthFormError = {
  field: "email" | "password";
  message: string;
};

export const minimumPluginPasswordLength = 4;

export function validateBackendAuthForm(input: {
  mode: BackendAuthMode;
  email: string;
  password: string;
}): BackendAuthFormError | null {
  if (!input.email.trim()) {
    return {
      field: "email",
      message: input.mode === "register" ? "请填写邮箱后再注册。" : "请填写邮箱后再登录。"
    };
  }

  if (Array.from(input.password).length < minimumPluginPasswordLength) {
    return {
      field: "password",
      message: `密码至少需要 ${minimumPluginPasswordLength} 位。`
    };
  }

  return null;
}
