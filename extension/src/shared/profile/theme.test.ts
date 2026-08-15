import { describe, expect, it } from "vitest";
import {
  getShortcutDensityMetrics,
  getShortcutGridColumnGap,
  getShortcutGridColumnCount,
  getShortcutGridJustification,
  getShortcutIconShapeRadius,
  getShortcutIconSizeMetrics,
  getShortcutRowHeight,
  shortcutGridLayout,
  shortcutIconShapeOptions,
  shortcutIconSizeOptions
} from "./theme";

describe("theme shortcut icon sizing", () => {
  it("only exposes the four supported semantic sizes", () => {
    expect(shortcutIconSizeOptions).toEqual([
      { id: "tiny", label: "迷你" },
      { id: "mini", label: "小" },
      { id: "small", label: "中" },
      { id: "medium", label: "大" }
    ]);
  });

  it("supports a 56px icon frame for very dense shortcut layouts", () => {
    expect(getShortcutIconSizeMetrics("tiny")).toEqual({
      tileSize: 56,
      tileMinHeight: 98,
      iconSize: 56,
      imageSize: 34,
      fallbackFontSize: 22,
      titleFontSize: 13
    });
  });

  it("scales shortcut labels with the selected icon size", () => {
    expect(shortcutIconSizeOptions.map(({ id }) => getShortcutIconSizeMetrics(id).titleFontSize)).toEqual([
      13, 14, 15, 16
    ]);
  });

  it("keeps the supported large icon frame independent from density", () => {
    expect(getShortcutIconSizeMetrics("medium")).toEqual({
      tileSize: 96,
      tileMinHeight: 136,
      iconSize: 96,
      imageSize: 62,
      fallbackFontSize: 36,
      titleFontSize: 16
    });
  });

  it("maps removed persisted sizes to the supported large size", () => {
    expect(getShortcutIconSizeMetrics("large")).toEqual({
      tileSize: 96,
      tileMinHeight: 136,
      iconSize: 96,
      imageSize: 62,
      fallbackFontSize: 36,
      titleFontSize: 16
    });
    expect(getShortcutIconSizeMetrics("xlarge")).toEqual(getShortcutIconSizeMetrics("medium"));
  });

  it("falls back to the default small metrics for unknown persisted values", () => {
    expect(getShortcutIconSizeMetrics("unknown")).toEqual(getShortcutIconSizeMetrics("mini"));
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
  it("uses the full rendered shortcut content when calculating row height", () => {
    for (const option of shortcutIconSizeOptions) {
      for (const density of ["comfortable", "compact"] as const) {
        const metrics = getShortcutIconSizeMetrics(option.id);
        const densityMetrics = getShortcutDensityMetrics(density, option.id);
        expect(getShortcutRowHeight(metrics, densityMetrics)).toBeGreaterThanOrEqual(metrics.tileMinHeight);
        expect(getShortcutRowHeight(metrics, densityMetrics)).toBeGreaterThanOrEqual(
          metrics.iconSize + densityMetrics.contentGap + shortcutGridLayout.titleHeight
        );
      }
    }
  });

  it("keeps every supported compact size and 4–8 column combination inside the shared rail", () => {
    for (const option of shortcutIconSizeOptions) {
      const metrics = getShortcutIconSizeMetrics(option.id);
      const density = getShortcutDensityMetrics("compact", option.id);

      for (let columns = 4; columns <= 8; columns += 1) {
        const gap = getShortcutGridColumnGap(columns, metrics, density.preferredColumnGap);
        const occupiedWidth = columns * metrics.tileSize + (columns - 1) * gap;

        expect(occupiedWidth).toBeLessThanOrEqual(shortcutGridLayout.contentWidth);
      }
    }
  });

  it("keeps every comfortable track matrix inside the rail before distributing free space", () => {
    for (const option of shortcutIconSizeOptions) {
      const metrics = getShortcutIconSizeMetrics(option.id);

      for (let columns = 4; columns <= 8; columns += 1) {
        expect(columns * metrics.tileSize).toBeLessThanOrEqual(shortcutGridLayout.contentWidth);
      }
    }
  });

  it("spreads comfortable sparse groups across the search rail while compact groups stay centered", () => {
    expect(getShortcutGridJustification("comfortable", 6)).toBe("space-between");
    expect(getShortcutGridJustification("compact", 6)).toBe("center");
    expect(getShortcutGridJustification("comfortable", 1)).toBe("center");
  });

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
