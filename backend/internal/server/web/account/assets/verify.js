"use strict";

const statusElement = document.querySelector("#account-status");
const fragment = new URLSearchParams(window.location.hash.slice(1));
const token = fragment.get("token");
window.history.replaceState(null, "", window.location.pathname);

async function verifyEmail() {
  if (!token) {
    statusElement.textContent = "验证链接不完整，请在扩展中重新发送验证邮件。";
    statusElement.dataset.tone = "error";
    return;
  }

  try {
    const response = await fetch("../api/v1/auth/verify-email", {
      method: "POST",
      credentials: "omit",
      cache: "no-store",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: JSON.stringify({ token })
    });
    if (!response.ok) throw new Error("invalid token");
    statusElement.textContent = "邮箱验证成功。现在可以返回扩展登录。";
    statusElement.dataset.tone = "success";
  } catch {
    statusElement.textContent = "验证链接无效或已过期，请在扩展中重新发送验证邮件。";
    statusElement.dataset.tone = "error";
  }
}

void verifyEmail();
