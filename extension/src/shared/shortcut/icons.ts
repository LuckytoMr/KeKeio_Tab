import { normalizeShortcutUrl } from "../url/normalize";
import type { ShortcutIcon } from "../profile/types";

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
