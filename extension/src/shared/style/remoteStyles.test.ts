import { describe, expect, test } from "vitest";
import { sha256Hex } from "../sync/gistBackup";
import {
  normalizeRemoteStyles,
  normalizeVerifiedRemoteStyles,
  remoteStyleCacheKey,
  validateRemoteStyleCss
} from "./remoteStyles";

describe("remote styles", () => {
  test("keeps valid CSS packages and drops unusable records", () => {
    const styles = normalizeRemoteStyles([
      {
        id: "style:glass",
        name: "玻璃风",
        version: "1.0.0",
        css: '.newtab-root[data-style-id="style:glass"] .shortcut-tile{color:red}',
        config: { density: "compact" }
      },
      { id: "style:empty", name: "空", version: "1.0.0", css: "" },
      { id: "bad" }
    ]);

    expect(styles).toHaveLength(1);
    expect(styles[0].id).toBe("style:glass");
    expect(remoteStyleCacheKey(styles[0])).toBe("style:glass@1.0.0");
  });

  test("rejects unscoped selectors, network loads, interaction overrides, and at-rules", () => {
    expect(validateRemoteStyleCss("style:glass", '.newtab-root[data-style-id="style:glass"] .shortcut-tile{color:red}')).toBe(true);
    expect(validateRemoteStyleCss("style:glass", ".shortcut-tile{color:red}")).toBe(false);
    expect(validateRemoteStyleCss("style:glass", '.newtab-root[data-style-id="style:glass"] .shortcut-tile{background:url(https://evil.test/x)}')).toBe(false);
    expect(validateRemoteStyleCss("style:glass", '.newtab-root[data-style-id="style:glass"] .settings-panel{pointer-events:none}')).toBe(false);
    expect(validateRemoteStyleCss("style:glass", '@import "https://evil.test/x.css";')).toBe(false);
  });

  test("applies only published packages whose hash and extension range verify", async () => {
    const css = '.newtab-root[data-style-id="style:glass"] .shortcut-tile{color:red}';
    const sha256 = await sha256Hex(css);
    const styles = await normalizeVerifiedRemoteStyles([{
      id: "style:glass",
      name: "玻璃风",
      version: "1.0.0",
      css,
      sha256,
      styleSchemaVersion: 1,
      minExtensionVersion: "0.1.0",
      maxExtensionVersion: "1.0.0",
      status: "published"
    }, {
      id: "style:bad-hash",
      name: "Bad",
      version: "1.0.0",
      css: css.replaceAll("style:glass", "style:bad-hash"),
      sha256: "0".repeat(64),
      styleSchemaVersion: 1,
      minExtensionVersion: "0.1.0",
      status: "published"
    }], "0.1.0");

    expect(styles.map((style) => style.id)).toEqual(["style:glass"]);
  });
});
