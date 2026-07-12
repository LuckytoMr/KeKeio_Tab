import type { ThemeSettings } from "./types";

export const shortcutIconSizeOptions = [
  { id: "tiny", label: "超小" },
  { id: "mini", label: "迷你" },
  { id: "small", label: "小" },
  { id: "medium", label: "中" },
  { id: "large", label: "大" },
  { id: "xlarge", label: "超大" }
] as const;

export type ShortcutIconSize = (typeof shortcutIconSizeOptions)[number]["id"];

export const shortcutIconShapeOptions = [
  { id: "squircle", label: "圆角方块" },
  { id: "circle", label: "圆形" },
  { id: "rounded", label: "轻圆角" },
  { id: "soft", label: "小圆角" }
] as const;

export type ShortcutIconShape = (typeof shortcutIconShapeOptions)[number]["id"];

export type ShortcutIconSizeMetrics = {
  tileSize: number;
  tileGap: number;
  tileMinHeight: number;
  iconSize: number;
  imageSize: number;
  fallbackFontSize: number;
};

const metricsBySize: Record<ShortcutIconSize, Record<ThemeSettings["density"], ShortcutIconSizeMetrics>> = {
  tiny: {
    comfortable: {
      tileSize: 74,
      tileGap: 18,
      tileMinHeight: 98,
      iconSize: 56,
      imageSize: 34,
      fallbackFontSize: 22
    },
    compact: {
      tileSize: 64,
      tileGap: 14,
      tileMinHeight: 86,
      iconSize: 48,
      imageSize: 30,
      fallbackFontSize: 18
    }
  },
  mini: {
    comfortable: {
      tileSize: 84,
      tileGap: 22,
      tileMinHeight: 108,
      iconSize: 64,
      imageSize: 40,
      fallbackFontSize: 24
    },
    compact: {
      tileSize: 74,
      tileGap: 16,
      tileMinHeight: 96,
      iconSize: 56,
      imageSize: 34,
      fallbackFontSize: 22
    }
  },
  small: {
    comfortable: {
      tileSize: 96,
      tileGap: 28,
      tileMinHeight: 122,
      iconSize: 82,
      imageSize: 52,
      fallbackFontSize: 30
    },
    compact: {
      tileSize: 84,
      tileGap: 18,
      tileMinHeight: 104,
      iconSize: 72,
      imageSize: 46,
      fallbackFontSize: 26
    }
  },
  medium: {
    comfortable: {
      tileSize: 112,
      tileGap: 34,
      tileMinHeight: 136,
      iconSize: 96,
      imageSize: 62,
      fallbackFontSize: 36
    },
    compact: {
      tileSize: 96,
      tileGap: 22,
      tileMinHeight: 116,
      iconSize: 82,
      imageSize: 52,
      fallbackFontSize: 30
    }
  },
  large: {
    comfortable: {
      tileSize: 128,
      tileGap: 38,
      tileMinHeight: 154,
      iconSize: 112,
      imageSize: 72,
      fallbackFontSize: 40
    },
    compact: {
      tileSize: 112,
      tileGap: 26,
      tileMinHeight: 132,
      iconSize: 96,
      imageSize: 62,
      fallbackFontSize: 34
    }
  },
  xlarge: {
    comfortable: {
      tileSize: 146,
      tileGap: 42,
      tileMinHeight: 176,
      iconSize: 128,
      imageSize: 84,
      fallbackFontSize: 46
    },
    compact: {
      tileSize: 128,
      tileGap: 30,
      tileMinHeight: 150,
      iconSize: 112,
      imageSize: 72,
      fallbackFontSize: 40
    }
  }
};

export function getShortcutIconSizeMetrics(iconSize: ShortcutIconSize, density: ThemeSettings["density"]) {
  return metricsBySize[iconSize]?.[density] ?? metricsBySize.medium[density];
}

export function getShortcutIconShapeRadius(iconShape: ShortcutIconShape, iconSize: number) {
  if (iconShape === "circle") return "999px";
  if (iconShape === "soft") return `${Math.max(4, Math.round(iconSize * 0.1))}px`;
  if (iconShape === "rounded") return `${Math.max(8, Math.round(iconSize * 0.18))}px`;

  return `${Math.max(8, Math.round(iconSize * 0.35 - 4.6))}px`;
}

export function getShortcutGridColumnCount(configuredColumns: ThemeSettings["columns"], visibleItemCount: number) {
  return Math.max(1, Math.min(configuredColumns, visibleItemCount));
}
