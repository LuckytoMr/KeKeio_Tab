import { afterEach, describe, expect, test, vi } from "vitest";
import { createDefaultProfile } from "../profile/defaults";
import { githubProfileFilename, githubTokenCreateUrl, loadProfileFromGitHubGist, saveProfileToGitHubGist } from "./client";

afterEach(() => vi.unstubAllGlobals());

describe("githubTokenCreateUrl", () => {
  test("opens GitHub token creation with only gist scope", () => {
    const url = new URL(githubTokenCreateUrl);

    expect(url.origin).toBe("https://github.com");
    expect(url.pathname).toBe("/settings/tokens/new");
    expect(url.searchParams.get("scopes")).toBe("gist");
    expect(url.searchParams.get("description")).toBe("kekeio");
  });
});

describe("versioned GitHub Gist backups", () => {
  test("writes and verifies the exact versioned envelope", async () => {
    let content = "";
    let description = "";
    const fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        const body = JSON.parse(String(init.body));
        description = body.description;
        content = body.files[githubProfileFilename].content;
        return new Response(JSON.stringify({ id: "gist:one", updated_at: "2026-07-12T00:00:00.000Z" }), {
          status: 201,
          headers: { "content-type": "application/json" }
        });
      }
      return new Response(JSON.stringify({ files: { [githubProfileFilename]: { content } } }), {
        status: 200,
        headers: { "content-type": "application/json" }
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    const result = await saveProfileToGitHubGist({ token: "token", profile: createDefaultProfile() });
    const envelope = JSON.parse(content);

    expect(Object.keys(envelope).sort()).toEqual([
      "canonicalProfileSha256",
      "exportedAt",
      "format",
      "formatVersion",
      "profile",
      "schemaVersion"
    ]);
    expect(description).toBe("kekeio profile backup");
    expect(result.canonicalProfileSha256).toBe(envelope.canonicalProfileSha256);
    expect(fetchMock.mock.calls.every(([, init]) => init?.credentials === "omit" && init.redirect === "error")).toBe(true);
  });

  test("refuses to overwrite a Gist that changed since the last observed hash", async () => {
    const remoteProfile = createDefaultProfile();
    remoteProfile.theme.showBrand = true;
    const { createGistBackupEnvelope } = await import("./gistBackup");
    const remoteEnvelope = await createGistBackupEnvelope(remoteProfile);
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      files: { [githubProfileFilename]: { content: JSON.stringify(remoteEnvelope) } }
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(saveProfileToGitHubGist({
      token: "token",
      gistId: "gist:one",
      expectedRemoteSha256: "stale-hash",
      profile: createDefaultProfile()
    })).rejects.toMatchObject({ code: "GIST_REMOTE_CHANGED" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  test("loads the exact truncated file from a validated raw URL without forwarding the token", async () => {
    const { createGistBackupEnvelope } = await import("./gistBackup");
    const envelope = await createGistBackupEnvelope(createDefaultProfile());
    const rawUrl = `https://gist.githubusercontent.com/owner/gist/raw/revision/${githubProfileFilename}`;
    const fetchMock = vi.fn(async (url: string, _init?: RequestInit) => {
      if (url === rawUrl) {
        return new Response(JSON.stringify(envelope), { status: 200, headers: { "content-type": "application/json" } });
      }
      return new Response(JSON.stringify({
        files: {
          "unrelated.txt": {
            content: "this file must never be parsed"
          },
          [githubProfileFilename]: {
            content: "{\"truncated\":true}",
            truncated: true,
            raw_url: rawUrl
          }
        }
      }), { status: 200, headers: { "content-type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(loadProfileFromGitHubGist({ token: " token ", gistId: "gist/one" }))
      .resolves.toMatchObject({ canonicalProfileSha256: envelope.canonicalProfileSha256 });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      "https://api.github.com/gists/gist%2Fone",
      rawUrl
    ]);
    expect(fetchMock.mock.calls[1][1]).toMatchObject({
      credentials: "omit",
      redirect: "error",
      referrerPolicy: "no-referrer"
    });
    expect(fetchMock.mock.calls[1][1]?.headers).toBeUndefined();
  });

  test("rejects untrusted raw URLs before making a cross-origin request", async () => {
    const fetchMock = vi.fn(async () => {
      return new Response(JSON.stringify({
        files: {
          [githubProfileFilename]: {
            truncated: true,
            raw_url: `https://evil.example/raw/${githubProfileFilename}`
          }
        }
      }), { status: 200, headers: { "content-type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(loadProfileFromGitHubGist({ token: "token", gistId: "gist:one" }))
      .rejects.toThrow("GitHub Gist 返回了不受信任的原始文件地址");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
