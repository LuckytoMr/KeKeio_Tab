const BLOCKED_PROTOCOLS = new Set(["javascript:", "data:", "vbscript:"]);
const ALLOWED_PROTOCOLS = new Set(["https:", "http:", "chrome:", "edge:"]);

export function normalizeShortcutUrl(input: string) {
  const trimmed = input.trim();
  if (!trimmed) {
    throw new Error("EMPTY_URL");
  }

  const candidate = /^[a-zA-Z][a-zA-Z\d+.-]*:/.test(trimmed) ? trimmed : `https://${trimmed}`;
  const url = new URL(candidate);

  if (BLOCKED_PROTOCOLS.has(url.protocol)) {
    throw new Error("BLOCKED_PROTOCOL");
  }

  if (!ALLOWED_PROTOCOLS.has(url.protocol)) {
    throw new Error("UNSUPPORTED_PROTOCOL");
  }

  return url.toString();
}
