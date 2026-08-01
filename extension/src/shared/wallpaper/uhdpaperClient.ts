import { sendWorkerMessage } from "../sync/workerProtocol";
import { parseUhdpaperListPage, UHD_HOME_URL, type UhdpaperListResult } from "./uhdpaper";

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

async function fetchDevProxy<T>(kind: ProxyKind, url: string): Promise<T> {
  const response = await fetch(getUhdpaperDevProxyUrl(kind, url));
  if (!response.ok) throw new Error(`本地 UHDpaper 代理失败：${response.status}`);
  return response.json() as Promise<T>;
}

async function fetchPageHtml(url: string) {
  if (canUseExtensionRuntime()) {
    const result = await sendWorkerMessage<{ html: string }>({
      type: "catalog:get",
      kind: "uhdpaper-page",
      query: url
    });
    return result.html;
  }

  if (canUseDevProxy()) {
    const result = await fetchDevProxy<{ html: string }>("page", url);
    return result.html;
  }

  throw new Error("当前环境不能通过扩展后台加载 UHDpaper 页面");
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

  const result = await sendWorkerMessage<{ dataUrl: string; mimeType: string }>({
    type: "catalog:get",
    kind: "uhdpaper-image",
    query: url
  });
  return result.dataUrl;
}

export async function fetchUhdpaperImageBlob(url: string) {
  const dataUrl = await fetchUhdpaperImageDataUrl(url);
  const response = await fetch(dataUrl);
  return response.blob();
}
