import type { ShortcutIconShape, ShortcutIconSize } from "./theme";

export type ShortcutIcon =
  | {
      kind: "text";
      text: string;
      bg?: string;
      fg?: string;
    }
  | {
      kind: "favicon";
      url: string;
      fallbackText: string;
    }
  | {
      kind: "url";
      url: string;
      fallbackText: string;
    }
  | {
      kind: "preset";
      presetId: string;
    }
  | {
      kind: "local";
      assetId: string;
      localOnly: true;
    };

export type Shortcut = {
  id: string;
  groupId: string;
  title: string;
  url: string;
  icon: ShortcutIcon;
  sortIndex: number;
  createdAt: string;
  updatedAt: string;
  deletedAt?: string;
  deletedByDeviceId?: string;
};

export type ShortcutGroup = {
  id: string;
  title: string;
  sortIndex: number;
  createdAt: string;
  updatedAt: string;
  deletedAt?: string;
};

export type SearchDisposition = "CURRENT_TAB" | "NEW_TAB";

export type SearchEngine = {
  id: string;
  title: string;
  template: string;
};

export type SearchSettings = {
  mode: "browser-default" | "custom";
  disposition: SearchDisposition;
  selectedEngineId: string;
  engines: SearchEngine[];
};

export type WallpaperRef =
  | {
      kind: "builtin";
      id: string;
    }
  | {
      kind: "remote";
      id: string;
      variantId: string;
    }
  | {
      kind: "local";
      assetId: string;
      localOnly: true;
    };

export type WallpaperSettings = {
  selected: WallpaperRef;
  portableSelected?: Exclude<WallpaperRef, { kind: "local" }>;
  selectedIds: string[];
  activeSourceTab: "official" | "web" | "local" | "selected";
  rotationMode: "manual" | "random";
  rotationSource: "selected" | "web";
  rotationIntervalSeconds: number;
  rotationHistory: string[];
  activeCategory: string;
  overlayOpacity: number;
  blur: number;
};

export type ThemeSettings = {
  styleId: "quark-flow" | (string & {});
  density: "comfortable" | "compact";
  sidebarSide: "left" | "right";
  showBrand: boolean;
  columns: 4 | 5 | 6 | 7 | 8;
  rows: 1 | 2 | 3 | 4 | 5;
  iconSize: ShortcutIconSize;
  iconShape: ShortcutIconShape;
};

export type SyncSettings = {
  provider: "none" | "github" | "backend";
  status: "disabled" | "signed-out" | "ready" | "syncing" | "error";
  repo?: string;
  backendUrl?: string;
  lastSyncedAt?: string;
  dirtySince?: string;
  lastSyncAttemptAt?: string;
  errorMessage?: string;
};

export type Profile = {
  schemaVersion: 1;
  profileId: string;
  deviceId: string;
  updatedAt: string;
  groups: ShortcutGroup[];
  shortcuts: Shortcut[];
  search: SearchSettings;
  wallpaper: WallpaperSettings;
  theme: ThemeSettings;
  sync: SyncSettings;
};

export type ShortcutInput = {
  id?: string;
  groupId: string;
  title: string;
  url: string;
  icon: ShortcutIcon;
};
