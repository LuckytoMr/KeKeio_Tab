import type { Profile, Shortcut, ShortcutGroup } from "./types";
import { defaultSearchEngines } from "../search/engines";
import { buildShortcutIcon } from "../shortcut/icons";

const now = () => new Date().toISOString();

function group(id: string, title: string, sortIndex: number, timestamp: string): ShortcutGroup {
  return { id, title, sortIndex, createdAt: timestamp, updatedAt: timestamp };
}

function shortcut(
  id: string,
  groupId: string,
  title: string,
  url: string,
  text: string,
  sortIndex: number,
  timestamp: string
): Shortcut {
  return {
    id,
    groupId,
    title,
    url,
    icon: buildShortcutIcon({ mode: "auto", title, url, iconText: text }),
    sortIndex,
    createdAt: timestamp,
    updatedAt: timestamp
  };
}

export function createDefaultProfile(): Profile {
  const timestamp = now();
  const groups = [group("group:media", "常用", 0, timestamp)];

  return {
    schemaVersion: 1,
    profileId: crypto.randomUUID(),
    deviceId: `device:${crypto.randomUUID()}`,
    updatedAt: timestamp,
    groups,
    shortcuts: [
      shortcut("shortcut:youtube", "group:media", "YouTube", "https://www.youtube.com", "YT", 0, timestamp),
      shortcut("shortcut:github", "group:media", "GitHub", "https://github.com", "GH", 1, timestamp),
      shortcut("shortcut:cloudflare", "group:media", "Cloudflare", "https://dash.cloudflare.com", "CF", 2, timestamp),
      shortcut("shortcut:google", "group:media", "Google", "https://www.google.com", "G", 3, timestamp),
      shortcut("shortcut:gmail", "group:media", "Gmail", "https://mail.google.com", "M", 4, timestamp)
    ],
    search: {
      mode: "custom",
      disposition: "CURRENT_TAB",
      selectedEngineId: "baidu",
      engines: defaultSearchEngines
    },
    wallpaper: {
      selected: { kind: "builtin", id: "mist" },
      portableSelected: { kind: "builtin", id: "mist" },
      selectedIds: ["mist"],
      activeSourceTab: "official",
      rotationMode: "manual",
      rotationSource: "selected",
      rotationIntervalSeconds: 60,
      rotationHistory: [],
      activeCategory: "all",
      overlayOpacity: 0.06,
      blur: 0
    },
    theme: {
      styleId: "quark-flow",
      density: "comfortable",
      sidebarSide: "left",
      showBrand: false,
      columns: 8,
      rows: 2,
      iconSize: "tiny",
      iconShape: "circle"
    },
    sync: {
      provider: "none",
      status: "disabled"
    }
  };
}
