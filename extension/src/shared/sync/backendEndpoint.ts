export const fixedBackendUrl = "https://tab.kekeio.com";

export function isFixedBackendUrl(input: unknown) {
  if (typeof input !== "string") return false;
  try {
    return new URL(input.trim()).toString().replace(/\/+$/, "") === fixedBackendUrl;
  } catch {
    return false;
  }
}
