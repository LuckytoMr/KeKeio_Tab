import { parseUhdpaperListPage, UHD_HOME_URL, type UhdpaperListResult } from "./uhdpaper";

type RuntimeResponse<T> =
  | {
      ok: true;
      data: T;
    }
  | {
      ok: false;
      error: string;
    };

type ProxyKind = "page" | "image";

function canUseExtensionRuntime() {
  return typeof chrome !== "undefined" && Boolean(chrome.runtime?.id && chrome.runtime.sendMessage);
}

function canUseDevProxy() {
  return (
    typeof window !== "undefined" &&
    window.location.protocol.startsWith("http") &&
    ["localhost", "127.0.0.1"].includes(window.location.hostname)
  );
}

export function getUhdpaperDevProxyUrl(kind: ProxyKind, url: string) {
  return `/__fullpro_proxy/uhdpaper/${kind}?url=${encodeURIComponent(url)}`;
}

async function sendRuntimeMessage<T>(message: unknown): Promise<T> {
  const response = (await chrome.runtime.sendMessage(message)) as RuntimeResponse<T> | undefined;
  if (!response) throw new Error("扩展后台没有响应");
  if (!response.ok) throw new Error(response.error);
  return response.data;
}

async function fetchDevProxy<T>(kind: ProxyKind, url: string): Promise<T> {
  const response = await fetch(getUhdpaperDevProxyUrl(kind, url));
  if (!response.ok) throw new Error(`本地 UHDpaper 代理失败：${response.status}`);
  return response.json() as Promise<T>;
}

async function fetchPageHtml(url: string) {
  if (canUseExtensionRuntime()) {
    const result = await sendRuntimeMessage<{ html: string }>({
      type: "uhdpaper:fetch-page",
      url
    });
    return result.html;
  }

  if (canUseDevProxy()) {
    const result = await fetchDevProxy<{ html: string }>("page", url);
    return result.html;
  }

  const response = await fetch(url);
  if (!response.ok) throw new Error(`UHDpaper 页面加载失败：${response.status}`);
  return response.text();
}

export async function loadUhdpaperWallpaperPage(url = UHD_HOME_URL): Promise<UhdpaperListResult> {
  const html = await fetchPageHtml(url);
  return parseUhdpaperListPage(html, url);
}

export async function fetchUhdpaperImageDataUrl(url: string) {
  if (canUseDevProxy()) {
    const result = await fetchDevProxy<{ dataUrl: string; mimeType: string }>("image", url);
    return result.dataUrl;
  }

  if (!canUseExtensionRuntime()) throw new Error("当前环境不能通过扩展后台加载 UHDpaper 图片");

  const result = await sendRuntimeMessage<{ dataUrl: string; mimeType: string }>({
    type: "uhdpaper:fetch-image",
    url
  });
  return result.dataUrl;
}

export async function fetchUhdpaperImageBlob(url: string) {
  const dataUrl = await fetchUhdpaperImageDataUrl(url);
  const response = await fetch(dataUrl);
  return response.blob();
}
