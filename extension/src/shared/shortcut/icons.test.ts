import { describe, expect, it } from "vitest";
import {
  buildShortcutIcon,
  fetchFirstUsableIcon,
  fetchShortcutIconThroughRuntime,
  fetchShortcutPageHtmlThroughRuntime,
  getKnownShortcutIconCandidates,
  getShortcutIconImageUrl,
  getShortcutFaviconUrl,
  getShortcutFallbackText,
  resolveIconCandidatesFromPage,
  resolveIconCandidatesFromHtml
} from "./icons";

describe("shortcut icons", () => {
  it("builds a first-party favicon URL from a shortcut URL", () => {
    expect(getShortcutFaviconUrl("example.com/docs")).toBe("https://example.com/favicon.ico");
  });

  it("uses compact fallback text for failed favicon images", () => {
    expect(getShortcutFallbackText("GitHub")).toBe("GI");
    expect(getShortcutFallbackText("谷歌邮箱")).toBe("谷歌");
  });

  it("builds automatic, text, and custom URL icons", () => {
    expect(buildShortcutIcon({ mode: "auto", title: "Example", url: "example.com" })).toEqual({
      kind: "favicon",
      url: "https://example.com/favicon.ico",
      fallbackText: "EX"
    });
    expect(buildShortcutIcon({ mode: "text", title: "Example", url: "example.com", iconText: "E" })).toEqual({
      kind: "text",
      text: "E"
    });
    expect(
      buildShortcutIcon({
        mode: "url",
        title: "Example",
        url: "example.com",
        iconUrl: "https://cdn.example.com/icon.png"
      })
    ).toEqual({
      kind: "url",
      url: "https://cdn.example.com/icon.png",
      fallbackText: "EX"
    });
  });

  it("builds Gmail automatic icons from a stable Google static asset", () => {
    expect(buildShortcutIcon({ mode: "auto", title: "Gmail", url: "https://mail.google.com" })).toEqual({
      kind: "favicon",
      url: "https://ssl.gstatic.com/ui/v1/icons/mail/rfr/gmail.ico",
      fallbackText: "GM"
    });
  });

  it("uses stable high resolution icon candidates for common shortcuts", () => {
    expect(getKnownShortcutIconCandidates("https://www.google.com")[0]).toBe(
      "https://www.gstatic.com/images/branding/product/2x/googleg_48dp.png"
    );
    expect(getKnownShortcutIconCandidates("https://github.com")[0]).toBe(
      "https://github.githubassets.com/favicons/favicon.svg"
    );
    expect(getKnownShortcutIconCandidates("https://www.youtube.com")[0]).toBe(
      "https://www.gstatic.com/youtube/img/branding/favicon/favicon_144x144.png"
    );
    expect(buildShortcutIcon({ mode: "auto", title: "Google", url: "https://www.google.com" })).toEqual({
      kind: "favicon",
      url: "https://www.gstatic.com/images/branding/product/2x/googleg_48dp.png",
      fallbackText: "GO"
    });
  });

  it("uses favicon icons as image sources while local cache is still warming", () => {
    expect(
      getShortcutIconImageUrl(
        {
          kind: "favicon",
          url: "https://www.google.com/favicon.ico",
          fallbackText: "G"
        },
        ""
      )
    ).toBe("https://www.google.com/favicon.ico");
  });

  it("resolves page icon candidates before falling back to favicon.ico", () => {
    const html = `
      <html>
        <head>
          <link rel="apple-touch-icon" href="/apple.png">
          <link rel="shortcut icon" href="https://static.example.com/favicon.svg">
          <link rel="icon" href="/favicon-32.png">
        </head>
      </html>
    `;

    expect(resolveIconCandidatesFromHtml("https://example.com/docs/page", html)).toEqual([
      "https://static.example.com/favicon.svg",
      "https://example.com/favicon-32.png",
      "https://example.com/apple.png",
      "https://example.com/favicon.ico"
    ]);
  });

  it("prefers vector and large declared icon sizes over tiny favicon links", () => {
    const html = `
      <link rel="icon" sizes="16x16" href="/favicon-16.png">
      <link rel="icon" sizes="192x192" href="/icon-192.png">
      <link rel="apple-touch-icon" sizes="180x180" href="/apple.png">
      <link rel="shortcut icon" type="image/svg+xml" href="/icon.svg">
    `;

    expect(resolveIconCandidatesFromHtml("https://example.com/app", html)).toEqual([
      "https://example.com/icon.svg",
      "https://example.com/icon-192.png",
      "https://example.com/apple.png",
      "https://example.com/favicon-16.png",
      "https://example.com/favicon.ico"
    ]);
  });

  it("resolves icon candidates from a fetched page", async () => {
    const candidates = await resolveIconCandidatesFromPage("example.com", async () => `
      <link rel="icon" href="/brand.png">
    `);

    expect(candidates[0]).toBe("https://example.com/brand.png");
  });

  it("uses known stable icon candidates before blocked service favicon redirects", async () => {
    const candidates = await resolveIconCandidatesFromPage("https://mail.google.com", async () => "<html></html>");

    expect(candidates[0]).toBe("https://ssl.gstatic.com/ui/v1/icons/mail/rfr/gmail.ico");
    expect(candidates).toContain("https://mail.google.com/favicon.ico");
  });

  it("uses the first successful image response when fetching icon candidates", async () => {
    const blob = new Blob(["icon"], { type: "image/png" });
    const responses = [
      new Response("", { status: 404 }),
      new Response(blob, { status: 200, headers: { "content-type": "image/png" } })
    ];
    const fetched: string[] = [];

    const result = await fetchFirstUsableIcon(["https://example.com/a.ico", "https://example.com/b.png"], async (url) => {
      fetched.push(url);
      return responses.shift()!;
    });

    expect(fetched).toEqual(["https://example.com/a.ico", "https://example.com/b.png"]);
    expect(result?.blob.type).toBe("image/png");
    expect(result?.sourceUrl).toBe("https://example.com/b.png");
  });

  it("can fetch shortcut page HTML through the extension runtime", async () => {
    const messages: unknown[] = [];
    const html = await fetchShortcutPageHtmlThroughRuntime("https://example.com", {
      sendMessage: async (message: unknown) => {
        messages.push(message);
        return { ok: true, data: { html: "<link rel=\"icon\" href=\"/icon.png\">" } };
      }
    });

    expect(messages).toEqual([{ type: "shortcut-icon:fetch-page", url: "https://example.com" }]);
    expect(html).toContain("rel=\"icon\"");
  });

  it("can fetch shortcut icon images through the extension runtime", async () => {
    const response = await fetchShortcutIconThroughRuntime("https://example.com/icon.png", {
      sendMessage: async () => ({
        ok: true,
        data: {
          mimeType: "image/png",
          dataUrl: "data:image/png;base64,aWNvbg=="
        }
      })
    });

    expect(response.ok).toBe(true);
    expect(response.headers.get("content-type")).toBe("image/png");
    expect(await response.text()).toBe("icon");
  });
});
