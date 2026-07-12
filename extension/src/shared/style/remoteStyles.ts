import { sha256Hex } from "../sync/gistBackup";

export type RemoteStylePackage = {
  id: string;
  name: string;
  version: string;
  description?: string;
  previewUrl?: string;
  css: string;
  config?: Record<string, unknown>;
  sha256?: string;
  styleSchemaVersion?: number;
  minExtensionVersion?: string;
  maxExtensionVersion?: string;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function normalizeRemoteStyle(value: unknown): RemoteStylePackage | undefined {
  if (!isRecord(value)) return undefined;
  if (typeof value.id !== "string" || typeof value.name !== "string" || typeof value.version !== "string") return undefined;
  if (typeof value.css !== "string" || !value.css.trim() || !validateRemoteStyleCss(value.id, value.css)) return undefined;

  return {
    id: value.id.trim(),
    name: value.name.trim(),
    version: value.version.trim(),
    description: typeof value.description === "string" ? value.description : undefined,
    previewUrl: typeof value.previewUrl === "string" ? value.previewUrl : undefined,
    css: value.css,
    config: isRecord(value.config) ? value.config : undefined,
    sha256: typeof value.sha256 === "string" ? value.sha256.toLowerCase() : undefined,
    styleSchemaVersion: typeof value.styleSchemaVersion === "number" ? value.styleSchemaVersion : undefined,
    minExtensionVersion: typeof value.minExtensionVersion === "string" ? value.minExtensionVersion : undefined,
    maxExtensionVersion: typeof value.maxExtensionVersion === "string" ? value.maxExtensionVersion : undefined
  };
}

export function normalizeRemoteStyles(values: unknown): RemoteStylePackage[] {
  if (!Array.isArray(values)) return [];
  return values.map(normalizeRemoteStyle).filter((item): item is RemoteStylePackage => Boolean(item));
}

export function remoteStyleCacheKey(style: RemoteStylePackage) {
  return `${style.id}@${style.version}`;
}

const allowedSelectorStart = /^(?:\.app-shell|\.newtab-main|\.shortcut(?:-tile|-icon|-title|-grid)?|\.search(?:-box|-engine[^\s,{]*)?|\.side-rail|\.group(?:-scroll-rail|-switcher)?|\.brand)(?:[\s.:[#>,+~]|$)/;
const allowedProperties = new Set([
  "color",
  "background",
  "background-color",
  "background-image",
  "border",
  "border-color",
  "border-radius",
  "border-width",
  "box-shadow",
  "opacity",
  "filter",
  "backdrop-filter",
  "font-size",
  "font-weight",
  "letter-spacing",
  "line-height",
  "text-shadow",
  "transform",
  "transition",
  "padding",
  "margin",
  "gap"
]);

export function validateRemoteStyleCss(styleId: string, css: string) {
  if (!/^[A-Za-z0-9:_-]{1,256}$/.test(styleId) || !css.trim() || css.length > 100 * 1024) return false;
  if (/@|url\s*\(|expression\s*\(|javascript:|behavior\s*:|-moz-binding|!important/i.test(css)) return false;
  const prefix = `.newtab-root[data-style-id="${styleId}"]`;
  const rulePattern = /([^{}]+)\{([^{}]*)\}/g;
  let matchedLength = 0;
  let match: RegExpExecArray | null;
  while ((match = rulePattern.exec(css))) {
    matchedLength += match[0].length;
    const selectors = match[1].split(",").map((selector) => selector.trim());
    if (!selectors.length || selectors.some((selector) => {
      if (!selector.startsWith(prefix)) return true;
      const remainder = selector.slice(prefix.length).trim();
      return remainder !== "" && !allowedSelectorStart.test(remainder);
    })) return false;
    const declarations = match[2].split(";").map((part) => part.trim()).filter(Boolean);
    if (!declarations.length || declarations.some((declaration) => {
      const separator = declaration.indexOf(":");
      if (separator <= 0) return true;
      const property = declaration.slice(0, separator).trim().toLowerCase();
      const value = declaration.slice(separator + 1).trim();
      if (!value || /url\s*\(|expression\s*\(|javascript:|!important/i.test(value)) return true;
      return !property.startsWith("--full-pro-") && !allowedProperties.has(property);
    })) return false;
  }
  return matchedLength > 0 && css.replace(rulePattern, "").trim() === "";
}

function compareVersions(left: string, right: string) {
  const a = left.split(/[.-]/).map((part) => Number(part) || 0);
  const b = right.split(/[.-]/).map((part) => Number(part) || 0);
  for (let index = 0; index < Math.max(a.length, b.length); index += 1) {
    if ((a[index] ?? 0) !== (b[index] ?? 0)) return (a[index] ?? 0) - (b[index] ?? 0);
  }
  return 0;
}

export async function normalizeVerifiedRemoteStyles(values: unknown, extensionVersion: string) {
  if (!Array.isArray(values)) return [];
  const verified = await Promise.all(values.map(async (value) => {
    if (!isRecord(value) || value.status !== "published") return undefined;
    const style = normalizeRemoteStyle(value);
    if (
      !style?.sha256 ||
      style.styleSchemaVersion !== 1 ||
      !style.minExtensionVersion ||
      compareVersions(extensionVersion, style.minExtensionVersion) < 0 ||
      (style.maxExtensionVersion && compareVersions(extensionVersion, style.maxExtensionVersion) > 0)
    ) return undefined;
    if ((await sha256Hex(style.css)) !== style.sha256) return undefined;
    return style;
  }));
  return verified.filter((style): style is RemoteStylePackage => Boolean(style));
}
