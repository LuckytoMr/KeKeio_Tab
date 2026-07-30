import { defaultSearchEngines, mergeSearchEngines } from "../search/engines";
import { buildShortcutIcon, getKnownShortcutIconCandidates, getShortcutPrimaryIconUrl } from "../shortcut/icons";
import { fixedBackendUrl, isFixedBackendUrl } from "../sync/backendEndpoint";
import { builtinWallpapers } from "../wallpaper/repository";
import { createDefaultProfile } from "./defaults";
import { normalizeShortcutIconSize } from "./theme";
import type { Profile, Shortcut } from "./types";

const legacyWashedOverlayOpacity = 0.58;
const previousDefaultOverlayOpacity = 0.16;

function migrateOverlayOpacity(value: number | undefined, defaultValue: number) {
  if (value === undefined) return defaultValue;
  return value === legacyWashedOverlayOpacity || value === previousDefaultOverlayOpacity ? defaultValue : value;
}

function migrateShortcutIcon(shortcut: Shortcut, shouldUpgradeTextIcon: boolean): Shortcut {
  if (shortcut.icon.kind === "favicon") {
    try {
      const stableIconUrl = getShortcutPrimaryIconUrl(shortcut.url);
      if (stableIconUrl === shortcut.icon.url) return shortcut;

      return {
        ...shortcut,
        icon: {
          ...shortcut.icon,
          url: stableIconUrl
        }
      };
    } catch {
      return shortcut;
    }
  }

  if (shortcut.icon.kind === "local") {
    try {
      if (!getKnownShortcutIconCandidates(shortcut.url).length) return shortcut;

      return {
        ...shortcut,
        icon: buildShortcutIcon({
          mode: "auto",
          title: shortcut.title,
          url: shortcut.url
        })
      };
    } catch {
      return shortcut;
    }
  }

  if (!shouldUpgradeTextIcon || shortcut.icon.kind !== "text") return shortcut;

  try {
    return {
      ...shortcut,
      icon: buildShortcutIcon({
        mode: "auto",
        title: shortcut.title,
        url: shortcut.url
      })
    };
  } catch {
    return shortcut;
  }
}

export function migrateProfile(input: Profile | undefined): Profile {
  const defaults = createDefaultProfile();
  if (!input) return defaults;

  const engines = mergeSearchEngines(input.search?.engines);
  const selectedEngineId = engines.some((engine) => engine.id === input.search?.selectedEngineId)
    ? input.search.selectedEngineId
    : defaultSearchEngines[0].id;

  const inputWallpaper = input.wallpaper?.selected;
  const selectedWallpaper =
    inputWallpaper?.kind === "builtin" && !builtinWallpapers.some((wallpaper) => wallpaper.id === inputWallpaper.id)
      ? defaults.wallpaper.selected
      : inputWallpaper ?? defaults.wallpaper.selected;
  const shouldUpgradeTextIcons = input.theme?.rows === undefined || input.wallpaper?.rotationIntervalSeconds === undefined;
  const shortcuts = input.shortcuts?.length
    ? input.shortcuts.map((shortcut) => migrateShortcutIcon(shortcut, shouldUpgradeTextIcons))
    : defaults.shortcuts;
  const theme = {
    ...defaults.theme,
    ...input.theme,
    iconSize: normalizeShortcutIconSize(input.theme?.iconSize, defaults.theme.iconSize)
  };
  const usesPreviousVisualDefaults =
    input.theme?.styleId === "quark-flow" &&
    input.theme.density === "comfortable" &&
    input.theme.sidebarSide === "left" &&
    input.theme.showBrand === false &&
    input.theme.columns === 6 &&
    input.theme.rows === 2 &&
    input.theme.iconSize === "medium" &&
    (input.theme.iconShape === undefined || input.theme.iconShape === "squircle");
  const usesCurrentVisualDefaults =
    input.theme?.styleId === "quark-flow" &&
    input.theme.density === "comfortable" &&
    input.theme.sidebarSide === "left" &&
    input.theme.showBrand === false &&
    input.theme.columns === 8 &&
    input.theme.rows === 2 &&
    input.theme.iconSize === "tiny" &&
    input.theme.iconShape === "circle";

  if (usesPreviousVisualDefaults || usesCurrentVisualDefaults) {
    theme.columns = defaults.theme.columns;
    theme.iconSize = defaults.theme.iconSize;
    theme.iconShape = defaults.theme.iconShape;
  }

  const pointsToFormerBackend = input.sync?.provider === "backend" && !isFixedBackendUrl(input.sync.backendUrl);

  return {
    ...defaults,
    ...input,
    groups: input.groups?.length ? input.groups : defaults.groups,
    shortcuts,
    search: {
      ...defaults.search,
      ...input.search,
      mode: input.search?.mode ?? "custom",
      selectedEngineId,
      engines
    },
    wallpaper: {
      ...defaults.wallpaper,
      ...input.wallpaper,
      selected: selectedWallpaper,
      portableSelected:
        input.wallpaper?.portableSelected ??
        (selectedWallpaper.kind === "local" ? defaults.wallpaper.portableSelected : selectedWallpaper),
      selectedIds: input.wallpaper?.selectedIds?.length
        ? input.wallpaper.selectedIds
        : [inputWallpaper?.kind === "local" ? `local:${inputWallpaper.assetId}` : inputWallpaper?.kind === "remote" ? `web:${inputWallpaper.id}:${inputWallpaper.variantId}` : inputWallpaper?.id ?? "mist"],
      activeSourceTab: input.wallpaper?.activeSourceTab ?? defaults.wallpaper.activeSourceTab,
      rotationHistory: input.wallpaper?.rotationHistory ?? [],
      activeCategory: input.wallpaper?.activeCategory ?? defaults.wallpaper.activeCategory,
      overlayOpacity: migrateOverlayOpacity(input.wallpaper?.overlayOpacity, defaults.wallpaper.overlayOpacity),
      blur: input.wallpaper?.blur ?? defaults.wallpaper.blur
    },
    theme,
    sync: {
      ...defaults.sync,
      ...input.sync,
      ...(pointsToFormerBackend
        ? {
            status: "signed-out" as const,
            backendUrl: fixedBackendUrl,
            errorMessage: "云端服务地址已升级，请重新登录。"
          }
        : {})
    }
  };
}
