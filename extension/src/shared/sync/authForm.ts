export type BackendAuthMode = "login" | "register";

export type BackendAuthFormError = {
  field: "email" | "password";
  message: string;
};

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

  const minimumLength = input.mode === "register" ? 8 : 4;
  if (input.password.length < minimumLength) {
    return {
      field: "password",
      message: input.mode === "register" ? "注册密码至少需要 8 位。" : "登录密码至少需要 4 位。"
    };
  }

  return null;
}
