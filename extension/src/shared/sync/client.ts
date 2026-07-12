export type SyncPreviewState = {
  provider: "github";
  status: "not-configured" | "ready";
  message: string;
};

export type GitHubGistSaveResult = {
  gistId: string;
  htmlUrl?: string;
  updatedAt: string;
  canonicalProfileSha256: string;
};

export class GistConflictError extends Error {
  readonly code = "GIST_REMOTE_CHANGED";

  constructor(readonly currentRemoteSha256: string) {
    super("GitHub Gist 已在其他位置发生变化，请先加载或明确确认覆盖");
    this.name = "GistConflictError";
  }
}

const githubApiBase = "https://api.github.com";
export const githubProfileFilename = "full-pro-profile.json";
export const githubTokenCreateUrl =
  "https://github.com/settings/tokens/new?scopes=gist&description=KeKeIO%20Tab";

export function getGitHubSyncPreview(): SyncPreviewState {
  return {
    provider: "github",
    status: "ready",
    message: "GitHub Gist 同步可用：只同步 profile.json，不上传图片。"
  };
}

function githubHeaders(token: string) {
  return {
    Accept: "application/vnd.github+json",
    Authorization: `Bearer ${token.trim()}`,
    "Content-Type": "application/json",
    "X-GitHub-Api-Version": "2022-11-28"
  };
}

async function githubRequest<T>(path: string, token: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`${githubApiBase}${path}`, {
    ...options,
    headers: {
      ...githubHeaders(token),
      ...(options.headers || {})
    }
  });
  const text = await response.text();
  const data = text ? JSON.parse(text) : null;
  if (!response.ok) {
    const message =
      data && typeof data === "object" && "message" in data && typeof data.message === "string"
        ? data.message
        : `GitHub 请求失败：${response.status}`;
    throw new Error(message);
  }
  return data as T;
}

export async function saveProfileToGitHubGist(input: {
  token: string;
  gistId?: string;
  profile: Profile;
  description?: string;
  expectedRemoteSha256?: string;
  allowOverwrite?: boolean;
}): Promise<GitHubGistSaveResult> {
  const envelope = await createGistBackupEnvelope(input.profile);
  if (input.gistId) {
    const remote = await loadProfileFromGitHubGist({ token: input.token, gistId: input.gistId });
    if (!input.allowOverwrite && remote.canonicalProfileSha256 !== input.expectedRemoteSha256) {
      throw new GistConflictError(remote.canonicalProfileSha256);
    }
  }
  const body = {
    description: input.description || "KeKeIO Tab profile backup",
    public: false,
    files: {
      [githubProfileFilename]: {
        content: JSON.stringify(envelope, null, 2)
      }
    }
  };
  const gist = input.gistId
    ? await githubRequest<{ id: string; html_url?: string; updated_at?: string }>(`/gists/${encodeURIComponent(input.gistId)}`, input.token, {
        method: "PATCH",
        body: JSON.stringify(body)
      })
    : await githubRequest<{ id: string; html_url?: string; updated_at?: string }>("/gists", input.token, {
        method: "POST",
        body: JSON.stringify(body)
      });

  const verified = await loadProfileFromGitHubGist({ token: input.token, gistId: gist.id });
  if (verified.canonicalProfileSha256 !== envelope.canonicalProfileSha256) {
    throw new Error("GitHub Gist 写入后校验失败");
  }
  return {
    gistId: gist.id,
    htmlUrl: gist.html_url,
    updatedAt: gist.updated_at || new Date().toISOString(),
    canonicalProfileSha256: verified.canonicalProfileSha256
  };
}

export async function loadProfileFromGitHubGist(input: { token: string; gistId: string }) {
  const gist = await githubRequest<{ files?: Record<string, { content?: string; raw_url?: string; truncated?: boolean }> }>(
    `/gists/${encodeURIComponent(input.gistId)}`,
    input.token
  );
  const file = gist.files?.[githubProfileFilename];
  let content = file?.content;
  if ((!content || file?.truncated) && file?.raw_url) {
    const response = await fetch(file.raw_url, { headers: githubHeaders(input.token), credentials: "omit", redirect: "error" });
    if (!response.ok) throw new Error(`GitHub Gist 原始文件加载失败：${response.status}`);
    content = await response.text();
  }
  if (!content) {
    throw new Error(`Gist 中没有 ${githubProfileFilename}`);
  }
  const profile = await parseGistBackup(content);
  return {
    profile,
    canonicalProfileSha256: await sha256Hex(canonicalJson(profile))
  };
}
import type { Profile } from "../profile/types";
import { canonicalJson, createGistBackupEnvelope, parseGistBackup, sha256Hex } from "./gistBackup";
