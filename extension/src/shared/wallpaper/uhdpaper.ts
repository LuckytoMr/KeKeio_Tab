import type { RemoteWallpaper, WallpaperVariant } from "./repository";

export const UHD_HOME_URL = "https://www.uhdpaper.com/";

export type UhdpaperListResult = {
  wallpapers: RemoteWallpaper[];
  nextPageUrl?: string;
};

const FALLBACK_PREVIEW_CSS =
  "linear-gradient(135deg, #102436 0%, #0b1521 42%, #4c1944 78%, #10131d 100%)";

function decodeHtml(input: string) {
  return input
    .replace(/&amp;/g, "&")
    .replace(/&#187;/g, "»")
    .replace(/&#8623;/g, "↯")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">");
}

function stripTags(input: string) {
  return decodeHtml(input.replace(/<[^>]+>/g, " ")).replace(/\s+/g, " ").trim();
}

function getAttr(tag: string, name: string) {
  const pattern = new RegExp(`${name}\\s*=\\s*(["'])(.*?)\\1`, "i");
  return decodeHtml(tag.match(pattern)?.[2] ?? "");
}

function normalizeUrl(rawUrl: string, baseUrl: string) {
  if (!rawUrl) return "";
  if (rawUrl.startsWith("//")) return `https:${rawUrl}`;
  return new URL(decodeHtml(rawUrl), baseUrl).href;
}

function imageBaseFromPreview(previewUrl: string) {
  return previewUrl
    .split("?")[0]
    .replace(/-thumb\.jpg$/i, "")
    .replace(/-thumb$/i, "")
    .replace(/\.jpg$/i, "");
}

function thumbnailUrlFromImageUrl(imageUrl: string) {
  if (/-thumb(?:\.jpg)?(?:\?|$)/i.test(imageUrl)) return imageUrl;
  return `${imageBaseFromPreview(imageUrl)}-thumb.jpg?dl`;
}

function inferCategory(title: string): RemoteWallpaper["category"] {
  const lower = title.toLowerCase();
  if (/\b(anime|manga|gojo|jujutsu|jjk|goku|dragon ball|luffy|one piece|pokemon|gundam)\b/.test(lower)) {
    return "anime";
  }
  if (/\b(cyberpunk|sci-fi|synthwave|retrowave|digital art|fantasy|abstract|space)\b/.test(lower)) {
    return "digital";
  }
  if (/\b(city|urban|gotham|metro|street)\b/.test(lower)) {
    return "city";
  }
  if (/\b(nature|scenery|mountain|ocean|sky|forest|flower|sunset|sunrise|beach|moon|lights)\b/.test(lower)) {
    return "nature";
  }
  return "digital";
}

function variantsFromImageBase(imageBaseUrl: string): WallpaperVariant[] {
  return [
    {
      id: "4k",
      label: "3840x2160",
      width: 3840,
      height: 2160,
      url: `${imageBaseUrl}-pc-4k.jpg`
    },
    {
      id: "2k",
      label: "2560x1440",
      width: 2560,
      height: 1440,
      url: `${imageBaseUrl}-pc-2k.jpg`
    },
    {
      id: "hd",
      label: "1920x1080",
      width: 1920,
      height: 1080,
      url: `${imageBaseUrl}-pc-hd.jpg`
    }
  ];
}

function tagsFromTitle(title: string, resolution: string) {
  const words = title
    .split(/\s+/)
    .map((word) => word.replace(/[^\w-]/g, ""))
    .filter(Boolean)
    .slice(0, 4);
  return Array.from(new Set([resolution, ...words]));
}

function parseArticle(articleHtml: string, pageUrl: string): RemoteWallpaper | undefined {
  const linkTag = articleHtml.match(/<a\b[^>]*href=(["']).*?\1[^>]*>/i)?.[0];
  const imageTag = articleHtml.match(/<img\b[^>]*src=(["']).*?\1[^>]*>/i)?.[0];
  const titleHtml = articleHtml.match(/<h2\b[^>]*>([\s\S]*?)<\/h2>/i)?.[1];
  const resolution = stripTags(articleHtml.match(/<b\b[^>]*>([\s\S]*?)<\/b>/i)?.[1] ?? "4K");

  if (!linkTag || !imageTag || !titleHtml) return undefined;

  const sourcePageUrl = normalizeUrl(getAttr(linkTag, "href"), pageUrl);
  const rawPreviewUrl = normalizeUrl(getAttr(imageTag, "src"), pageUrl);
  const title = stripTags(titleHtml);
  const imageBaseUrl = imageBaseFromPreview(rawPreviewUrl);
  const previewUrl = thumbnailUrlFromImageUrl(rawPreviewUrl);
  const key = imageBaseUrl.match(/(\d+@\d+@[a-z0-9]+)$/i)?.[1];

  if (!sourcePageUrl || !previewUrl || !title || !key) return undefined;

  return {
    id: `uhdpaper:${key}`,
    title,
    provider: "UHDpaper",
    sourcePageUrl,
    previewUrl,
    previewCss: FALLBACK_PREVIEW_CSS,
    category: inferCategory(title),
    orientation: "landscape",
    tags: tagsFromTitle(title, resolution),
    variants: variantsFromImageBase(imageBaseUrl)
  };
}

function findNextPageUrl(html: string, pageUrl: string) {
  const anchors = html.match(/<a\b[\s\S]*?<\/a>/gi) ?? [];
  for (const anchor of anchors) {
    const text = stripTags(anchor);
    if (!/Next|»/i.test(text)) continue;
    const openTag = anchor.match(/<a\b[^>]*>/i)?.[0] ?? "";
    const href = getAttr(openTag, "href");
    if (href && !href.startsWith("javascript:")) return normalizeUrl(href, pageUrl);
  }
  return undefined;
}

export function parseUhdpaperListPage(html: string, pageUrl = UHD_HOME_URL): UhdpaperListResult {
  const articles = html.match(/<article\b[\s\S]*?<\/article>/gi) ?? [];
  const wallpapers = articles
    .map((article) => parseArticle(article, pageUrl))
    .filter((wallpaper): wallpaper is RemoteWallpaper => Boolean(wallpaper));

  return {
    wallpapers,
    nextPageUrl: findNextPageUrl(html, pageUrl)
  };
}
