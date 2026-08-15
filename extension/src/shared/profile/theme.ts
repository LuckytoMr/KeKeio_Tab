import type { ThemeSettings } from "./types";

export const shortcutIconSizeOptions = [
  { id: "tiny", label: "迷你" },
  { id: "mini", label: "小" },
  { id: "small", label: "中" },
  { id: "medium", label: "大" }
] as const;

export type ShortcutIconSize = (typeof shortcutIconSizeOptions)[number]["id"];

export function normalizeShortcutIconSize(value: unknown, fallback: ShortcutIconSize = "mini"): ShortcutIconSize {
  if (value === "tiny" || value === "mini" || value === "small" || value === "medium") return value;
  if (value === "large" || value === "xlarge") return "medium";
  return fallback;
}

export const shortcutIconShapeOptions = [
  { id: "squircle", label: "圆角方块" },
  { id: "circle", label: "圆形" },
  { id: "rounded", label: "轻圆角" },
  { id: "soft", label: "小圆角" }
] as const;

export type ShortcutIconShape = (typeof shortcutIconShapeOptions)[number]["id"];

export type ShortcutIconSizeMetrics = {
  tileSize: number;
  tileMinHeight: number;
  iconSize: number;
  imageSize: number;
  fallbackFontSize: number;
  titleFontSize: number;
};

export type ShortcutDensityMetrics = {
  paddingTop: number;
  paddingBottom: number;
  contentGap: number;
  rowGap: number;
  preferredColumnGap: number;
};

export const shortcutGridLayout = {
  contentWidth: 820,
  paddingInline: 0,
  titleHeight: 40
} as const;

const metricsBySize: Record<ShortcutIconSize, ShortcutIconSizeMetrics> = {
  tiny: {
    tileSize: 56,
    tileMinHeight: 98,
    iconSize: 56,
    imageSize: 34,
    fallbackFontSize: 22,
    titleFontSize: 13
  },
  mini: {
    tileSize: 64,
    tileMinHeight: 108,
    iconSize: 64,
    imageSize: 40,
    fallbackFontSize: 24,
    titleFontSize: 14
  },
  small: {
    tileSize: 82,
    tileMinHeight: 122,
    iconSize: 82,
    imageSize: 52,
    fallbackFontSize: 30,
    titleFontSize: 15
  },
  medium: {
    tileSize: 96,
    tileMinHeight: 136,
    iconSize: 96,
    imageSize: 62,
    fallbackFontSize: 36,
    titleFontSize: 16
  }
};

const densityMetricsByMode: Record<
  ThemeSettings["density"],
  Omit<ShortcutDensityMetrics, "rowGap" | "preferredColumnGap"> & {
    spacingBySize: Record<ShortcutIconSize, Pick<ShortcutDensityMetrics, "rowGap" | "preferredColumnGap">>;
  }
> = {
  comfortable: {
    paddingTop: 50,
    paddingBottom: 28,
    contentGap: 13,
    spacingBySize: {
      tiny: { rowGap: 18, preferredColumnGap: 18 },
      mini: { rowGap: 22, preferredColumnGap: 22 },
      small: { rowGap: 28, preferredColumnGap: 28 },
      medium: { rowGap: 34, preferredColumnGap: 34 }
    }
  },
  compact: {
    paddingTop: 32,
    paddingBottom: 20,
    contentGap: 9,
    spacingBySize: {
      tiny: { rowGap: 12, preferredColumnGap: 12 },
      mini: { rowGap: 14, preferredColumnGap: 14 },
      small: { rowGap: 16, preferredColumnGap: 16 },
      medium: { rowGap: 18, preferredColumnGap: 18 }
    }
  }
};

export function getShortcutIconSizeMetrics(iconSize: ShortcutIconSize | string | null | undefined) {
  return metricsBySize[normalizeShortcutIconSize(iconSize)];
}

export function getShortcutDensityMetrics(
  density: ThemeSettings["density"],
  iconSize: ShortcutIconSize | string | null | undefined
): ShortcutDensityMetrics {
  const definition = densityMetricsByMode[density];
  const spacing = definition.spacingBySize[normalizeShortcutIconSize(iconSize)];

  return {
    paddingTop: definition.paddingTop,
    paddingBottom: definition.paddingBottom,
    contentGap: definition.contentGap,
    ...spacing
  };
}

export function getShortcutGridColumnGap(
  columnCount: number,
  metrics: ShortcutIconSizeMetrics,
  preferredColumnGap: number
) {
  if (columnCount <= 1) return 0;

  const innerWidth = shortcutGridLayout.contentWidth - shortcutGridLayout.paddingInline * 2;
  const availableGap = (innerWidth - columnCount * metrics.tileSize) / (columnCount - 1);

  return Math.max(0, Math.min(preferredColumnGap, availableGap));
}

export function getShortcutGridJustification(density: ThemeSettings["density"], columnCount: number) {
  return density === "comfortable" && columnCount > 1 ? "space-between" : "center";
}

export function getShortcutRowHeight(metrics: ShortcutIconSizeMetrics, density: ShortcutDensityMetrics) {
  const contentHeight = metrics.iconSize + density.contentGap + shortcutGridLayout.titleHeight;
  return Math.max(metrics.tileMinHeight, contentHeight);
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
