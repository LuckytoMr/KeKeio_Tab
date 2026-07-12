import { describe, expect, it } from "vitest";
import {
  getShortcutIconShapeRadius,
  getShortcutGridColumnCount,
  getShortcutIconSizeMetrics,
  shortcutIconShapeOptions,
  shortcutIconSizeOptions
} from "./theme";

describe("theme shortcut icon sizing", () => {
  it("exposes ordered shortcut icon size choices down to a Quark-like compact size", () => {
    expect(shortcutIconSizeOptions.map((option) => option.id)).toEqual([
      "tiny",
      "mini",
      "small",
      "medium",
      "large",
      "xlarge"
    ]);
  });

  it("supports a 56px comfortable icon frame for very dense shortcut layouts", () => {
    expect(getShortcutIconSizeMetrics("tiny", "comfortable")).toEqual({
      tileSize: 74,
      tileGap: 18,
      tileMinHeight: 98,
      iconSize: 56,
      imageSize: 34,
      fallbackFontSize: 22
    });
  });

  it("keeps medium comfortable metrics equal to the current layout", () => {
    expect(getShortcutIconSizeMetrics("medium", "comfortable")).toEqual({
      tileSize: 112,
      tileGap: 34,
      tileMinHeight: 136,
      iconSize: 96,
      imageSize: 62,
      fallbackFontSize: 36
    });
  });

  it("scales compact and large layouts without changing row math separately", () => {
    expect(getShortcutIconSizeMetrics("large", "compact")).toEqual({
      tileSize: 112,
      tileGap: 26,
      tileMinHeight: 132,
      iconSize: 96,
      imageSize: 62,
      fallbackFontSize: 34
    });
  });
});

describe("theme shortcut icon shape", () => {
  it("exposes explicit shape choices instead of baking shape into size", () => {
    expect(shortcutIconShapeOptions.map((option) => option.id)).toEqual([
      "squircle",
      "circle",
      "rounded",
      "soft"
    ]);
  });

  it("keeps squircle corners proportional so small icons do not become circles", () => {
    expect(getShortcutIconShapeRadius("squircle", 56)).toBe("15px");
    expect(getShortcutIconShapeRadius("squircle", 96)).toBe("29px");
  });

  it("supports fully round and flatter icon frames", () => {
    expect(getShortcutIconShapeRadius("circle", 56)).toBe("999px");
    expect(getShortcutIconShapeRadius("rounded", 56)).toBe("10px");
    expect(getShortcutIconShapeRadius("soft", 56)).toBe("6px");
  });
});

describe("theme shortcut grid layout", () => {
  it("centers sparse shortcut groups by using only the visible item columns", () => {
    expect(getShortcutGridColumnCount(8, 3)).toBe(3);
  });

  it("caps visible shortcut columns at the configured maximum", () => {
    expect(getShortcutGridColumnCount(8, 12)).toBe(8);
  });

  it("keeps at least one grid column available for empty groups", () => {
    expect(getShortcutGridColumnCount(8, 0)).toBe(1);
  });
});
