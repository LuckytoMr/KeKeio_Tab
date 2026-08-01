import { describe, expect, it } from "vitest";
import {
  buildShortcutIcon,
  getKnownShortcutIconCandidates,
  getShortcutIconImageUrl,
  getShortcutFaviconUrl,
  getShortcutFallbackText
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

  it("uses favicon icons directly as image sources", () => {
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

});
