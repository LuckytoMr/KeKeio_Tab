import { normalizeShortcutUrl } from "../url/normalize";
import type { ShortcutIcon } from "../profile/types";
import { db, type LocalAsset } from "../storage/db";

export type ShortcutIconMode = "auto" | "text" | "url";

export type ShortcutIconBuildInput = {
  mode: ShortcutIconMode;
  title: string;
  url: string;
  iconText?: string;
  iconUrl?: string;
};

export function getShortcutFallbackText(title: string) {
  const chars = Array.from(title.trim());
  return chars.slice(0, Math.min(chars.length, 2)).join("").toUpperCase() || "•";
}

export function getShortcutFaviconUrl(shortcutUrl: string) {
  const normalized = normalizeShortcutUrl(shortcutUrl);
  const url = new URL(normalized);
  return `${url.origin}/favicon.ico`;
}

export function getKnownShortcutIconCandidates(shortcutUrl: string) {
  const normalized = normalizeShortcutUrl(shortcutUrl);
  const hostname = new URL(normalized).hostname.toLowerCase();

  if (hostname === "mail.google.com") {
    return ["https://ssl.gstatic.com/ui/v1/icons/mail/rfr/gmail.ico"];
  }

  if (hostname === "google.com" || hostname === "www.google.com") {
    return ["https://www.gstatic.com/images/branding/product/2x/googleg_48dp.png"];
  }

  if (hostname === "github.com") {
    return ["https://github.githubassets.com/favicons/favicon.svg"];
  }

  if (hostname === "youtube.com" || hostname === "www.youtube.com" || hostname === "m.youtube.com") {
    return ["https://www.gstatic.com/youtube/img/branding/favicon/favicon_144x144.png"];
  }

  return [];
}

export function getShortcutPrimaryIconUrl(shortcutUrl: string) {
  return getKnownShortcutIconCandidates(shortcutUrl)[0] ?? getShortcutFaviconUrl(shortcutUrl);
}

export function getShortcutIconImageUrl(icon: ShortcutIcon, localUrl: string) {
  if (icon.kind === "favicon" || icon.kind === "url") return icon.url;
  if (icon.kind === "local") return localUrl;
  return "";
}

type IconCandidate = {
  url: string;
  score: number;
};

function parseLinkAttributes(tag: string) {
  const attributes: Record<string, string> = {};
  const pattern = /([:\w-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+))/g;
  let match: RegExpExecArray | null;

  while ((match = pattern.exec(tag))) {
    attributes[match[1].toLowerCase()] = match[2] ?? match[3] ?? match[4] ?? "";
  }

  return attributes;
}

function getDeclaredIconSize(sizes: string | undefined) {
  if (!sizes) return 0;
  if (sizes.toLowerCase().split(/\s+/).includes("any")) return 1024;

  let maxSize = 0;
  const pattern = /(\d+)\s*x\s*(\d+)/gi;
  let match: RegExpExecArray | null;

  while ((match = pattern.exec(sizes))) {
    maxSize = Math.max(maxSize, Number(match[1]), Number(match[2]));
  }

  return maxSize;
}

function isSvgIcon(url: string, type: string | undefined) {
  if (type?.toLowerCase().includes("svg")) return true;

  try {
    return new URL(url).pathname.toLowerCase().endsWith(".svg");
  } catch {
    return url.toLowerCase().split("?")[0].endsWith(".svg");
  }
}

function getIconScore(rel: string, attributes: Record<string, string>, url: string) {
  const tokens = rel.toLowerCase().split(/\s+/).filter(Boolean);
  const isApple = tokens.some((token) => token.includes("apple-touch-icon"));
  const isShortcut = tokens.includes("shortcut") && tokens.includes("icon");
  const isIcon = tokens.includes("icon") && !isApple;
  if (!isApple && !isIcon) return undefined;

  if (isSvgIcon(url, attributes.type)) return 9000;

  const sizeScore = Math.min(getDeclaredIconSize(attributes.sizes), 512) * 10;
  if (isIcon && !isShortcut) return 3000 + sizeScore;
  if (isShortcut) return 2800 + sizeScore;
  if (isApple) return 2500 + sizeScore;
  return undefined;
}

export function resolveIconCandidatesFromHtml(pageUrl: string, html: string) {
  const baseUrl = normalizeShortcutUrl(pageUrl);
  const origin = new URL(baseUrl).origin;
  const candidates: IconCandidate[] = getKnownShortcutIconCandidates(baseUrl).map((url) => ({ url, score: 10000 }));
  const linkPattern = /<link\b[^>]*>/gi;
  let match: RegExpExecArray | null;

  while ((match = linkPattern.exec(html))) {
    const attributes = parseLinkAttributes(match[0]);
    if (!attributes.href || !attributes.rel) continue;

    try {
      const url = new URL(attributes.href, baseUrl).toString();
      const score = getIconScore(attributes.rel, attributes, url);
      if (score === undefined) continue;

      candidates.push({
        url,
        score
      });
    } catch {
      // Ignore malformed icon links and continue to the favicon fallback.
    }
  }

  candidates.push({
    url: `${origin}/favicon.ico`,
    score: 1000
  });

  const seen = new Set<string>();
  return candidates
    .sort((a, b) => b.score - a.score)
    .map((candidate) => candidate.url)
    .filter((url) => {
      if (seen.has(url)) return false;
      seen.add(url);
      return true;
    });
}

export type PageHtmlFetcher = (url: string) => Promise<string>;
export type IconFetcher = (url: string) => Promise<Response>;

type RuntimeResponse<T> =
  | {
      ok: true;
      data: T;
    }
  | {
      ok: false;
      error: string;
    };

export type ShortcutIconRuntime = {
  sendMessage(message: unknown): Promise<RuntimeResponse<unknown>>;
};

function getShortcutIconRuntime(runtime?: ShortcutIconRuntime) {
  if (runtime) return runtime;
  if (typeof chrome === "undefined" || !chrome.runtime?.id || !chrome.runtime.sendMessage) return undefined;
  return chrome.runtime as unknown as ShortcutIconRuntime;
}

async function sendShortcutIconRuntimeMessage<T>(message: unknown, runtime?: ShortcutIconRuntime) {
  const targetRuntime = getShortcutIconRuntime(runtime);
  if (!targetRuntime) throw new Error("SHORTCUT_ICON_RUNTIME_UNAVAILABLE");

  const response = (await targetRuntime.sendMessage(message)) as RuntimeResponse<T> | undefined;
  if (!response) throw new Error("SHORTCUT_ICON_RUNTIME_EMPTY_RESPONSE");
  if (!response.ok) throw new Error(response.error);
  return response.data;
}

export async function fetchShortcutPageHtmlThroughRuntime(url: string, runtime?: ShortcutIconRuntime) {
  const result = await sendShortcutIconRuntimeMessage<{ html: string }>(
    {
      type: "shortcut-icon:fetch-page",
      url
    },
    runtime
  );
  return result.html;
}

export async function fetchShortcutIconThroughRuntime(url: string, runtime?: ShortcutIconRuntime) {
  const result = await sendShortcutIconRuntimeMessage<{ dataUrl: string; mimeType: string }>(
    {
      type: "shortcut-icon:fetch-image",
      url
    },
    runtime
  );
  const dataUrlResponse = await fetch(result.dataUrl);
  const blob = await dataUrlResponse.blob();
  return new Response(blob, {
    status: 200,
    headers: {
      "content-type": result.mimeType
    }
  });
}

function canUseShortcutIconRuntime() {
  return Boolean(getShortcutIconRuntime());
}

export async function resolveIconCandidatesFromPage(siteUrl: string, fetchPageHtml?: PageHtmlFetcher) {
  const normalized = normalizeShortcutUrl(siteUrl);
  const loadHtml =
    fetchPageHtml ??
    (async (url: string) => {
      const response = await fetch(url, {
        credentials: "omit",
        redirect: "follow"
      });
      if (!response.ok) throw new Error(`ICON_PAGE_FETCH_FAILED:${response.status}`);
      return response.text();
    });

  try {
    const html = await loadHtml(normalized);
    return resolveIconCandidatesFromHtml(normalized, html);
  } catch {
    return [getShortcutFaviconUrl(normalized)];
  }
}

export async function fetchFirstUsableIcon(candidates: string[], fetchIcon?: IconFetcher) {
  const loadIcon = fetchIcon ?? ((url: string) => fetch(url, { credentials: "omit", redirect: "follow" }));

  for (const url of candidates) {
    try {
      const response = await loadIcon(url);
      if (!response.ok) continue;

      const blob = await response.blob();
      const contentType = blob.type || response.headers.get("content-type") || "";
      if (!contentType.startsWith("image/")) continue;

      return {
        sourceUrl: url,
        blob
      };
    } catch {
      // Try the next candidate. Many sites block direct favicon requests.
    }
  }

  return undefined;
}

export async function saveLocalShortcutIcon(blob: Blob, name: string) {
  const timestamp = new Date().toISOString();
  const asset: LocalAsset = {
    assetId: `icon:${crypto.randomUUID()}`,
    type: "icon",
    name,
    mimeType: blob.type || "image/x-icon",
    size: blob.size,
    blob,
    createdAt: timestamp,
    lastUsedAt: timestamp
  };

  await db.assets.put(asset);
  return asset;
}

export async function cacheShortcutIcon(input: { title: string; url: string }): Promise<ShortcutIcon | undefined> {
  const fetchPageHtml = canUseShortcutIconRuntime() ? fetchShortcutPageHtmlThroughRuntime : undefined;
  const fetchIcon = canUseShortcutIconRuntime() ? fetchShortcutIconThroughRuntime : undefined;
  const candidates = await resolveIconCandidatesFromPage(input.url, fetchPageHtml);
  const usable = await fetchFirstUsableIcon(candidates, fetchIcon);
  if (!usable) return undefined;

  const asset = await saveLocalShortcutIcon(usable.blob, `${input.title || new URL(normalizeShortcutUrl(input.url)).hostname}-icon`);
  return {
    kind: "local",
    assetId: asset.assetId,
    localOnly: true
  };
}

export function buildShortcutIcon(input: ShortcutIconBuildInput): ShortcutIcon {
  const fallbackText = getShortcutFallbackText(input.title);

  if (input.mode === "text") {
    return {
      kind: "text",
      text: input.iconText?.trim().toUpperCase() || fallbackText
    };
  }

  if (input.mode === "url") {
    return {
      kind: "url",
      url: normalizeShortcutUrl(input.iconUrl ?? ""),
      fallbackText
    };
  }

  return {
    kind: "favicon",
    url: getShortcutPrimaryIconUrl(input.url),
    fallbackText
  };
}
