export type GitHubSyncAuth = {
  token: string;
  gistId: string;
  updatedAt: string;
  canonicalProfileSha256?: string;
};

const GITHUB_SYNC_AUTH_KEY = "fullProGitHubSyncAuth";

function hasChromeStorage() {
  return typeof chrome !== "undefined" && Boolean(chrome.storage?.local);
}

function isGitHubSyncAuth(value: unknown): value is GitHubSyncAuth {
  if (!value || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return (
    typeof record.token === "string" &&
    typeof record.gistId === "string" &&
    typeof record.updatedAt === "string" &&
    (record.canonicalProfileSha256 === undefined || typeof record.canonicalProfileSha256 === "string")
  );
}

export async function loadGitHubSyncAuth(): Promise<GitHubSyncAuth | null> {
  if (hasChromeStorage()) {
    const result = await chrome.storage.local.get(GITHUB_SYNC_AUTH_KEY);
    const value = result[GITHUB_SYNC_AUTH_KEY];
    return isGitHubSyncAuth(value) ? value : null;
  }

  const raw = globalThis.localStorage?.getItem(GITHUB_SYNC_AUTH_KEY);
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    return isGitHubSyncAuth(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

export async function saveGitHubSyncAuth(auth: GitHubSyncAuth) {
  if (hasChromeStorage()) {
    await chrome.storage.local.set({ [GITHUB_SYNC_AUTH_KEY]: auth });
    return;
  }
  globalThis.localStorage?.setItem(GITHUB_SYNC_AUTH_KEY, JSON.stringify(auth));
}

export async function clearGitHubSyncAuth() {
  if (hasChromeStorage()) {
    await chrome.storage.local.remove(GITHUB_SYNC_AUTH_KEY);
    return;
  }
  globalThis.localStorage?.removeItem(GITHUB_SYNC_AUTH_KEY);
}
