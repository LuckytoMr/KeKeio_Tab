"use strict";

const form = document.querySelector("#reset-form");
const statusElement = document.querySelector("#account-status");
const fragment = new URLSearchParams(window.location.hash.slice(1));
const token = fragment.get("token");
window.history.replaceState(null, "", window.location.pathname);

if (!token) {
  statusElement.textContent = "重置链接不完整，请在扩展中重新申请密码重置邮件。";
  statusElement.dataset.tone = "error";
} else {
  form.hidden = false;
  statusElement.textContent = "请输入至少 8 位的新密码。";
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const password = form.elements.password.value;
  const confirmation = form.elements.passwordConfirmation.value;
  if (password.length < 8) {
    statusElement.textContent = "新密码至少需要 8 位。";
    statusElement.dataset.tone = "error";
    return;
  }
  if (password !== confirmation) {
    statusElement.textContent = "两次输入的密码不一致。";
    statusElement.dataset.tone = "error";
    return;
  }

  const button = form.querySelector("button");
  button.disabled = true;
  statusElement.textContent = "正在保存新密码…";
  delete statusElement.dataset.tone;
  try {
    const response = await fetch("../api/v1/auth/reset-password", {
      method: "POST",
      credentials: "omit",
      cache: "no-store",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: JSON.stringify({ token, password })
    });
    if (!response.ok) throw new Error("invalid token");
    form.hidden = true;
    form.reset();
    statusElement.textContent = "密码已重置。请返回扩展重新登录。";
    statusElement.dataset.tone = "success";
  } catch {
    statusElement.textContent = "重置链接无效、已使用或已过期，请重新申请。";
    statusElement.dataset.tone = "error";
    button.disabled = false;
  }
});
