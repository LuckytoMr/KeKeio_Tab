import { normalizeShortcutUrl } from "../url/normalize";
import { parseRemoteWallpaperKey } from "../wallpaper/repository";
import type { Profile, Shortcut, ShortcutGroup, ShortcutInput } from "./types";

const now = () => new Date().toISOString();

export function upsertShortcut(profile: Profile, input: ShortcutInput): Profile {
  const timestamp = now();
  const existing = input.id ? profile.shortcuts.find((shortcut) => shortcut.id === input.id) : undefined;
  const activeInGroup = profile.shortcuts.filter(
    (shortcut) => shortcut.groupId === input.groupId && !shortcut.deletedAt
  );

  const nextShortcut: Shortcut = {
    id: existing?.id ?? input.id ?? `shortcut:${crypto.randomUUID()}`,
    groupId: input.groupId,
    title: input.title.trim(),
    url: normalizeShortcutUrl(input.url),
    icon: input.icon,
    sortIndex: existing?.sortIndex ?? activeInGroup.length,
    createdAt: existing?.createdAt ?? timestamp,
    updatedAt: timestamp
  };

  const shortcuts = existing
    ? profile.shortcuts.map((shortcut) => (shortcut.id === existing.id ? nextShortcut : shortcut))
    : [...profile.shortcuts, nextShortcut];

  return {
    ...profile,
    updatedAt: timestamp,
    shortcuts
  };
}

export function deleteShortcut(profile: Profile, shortcutId: string, deviceId: string): Profile {
  const timestamp = now();

  return {
    ...profile,
    updatedAt: timestamp,
    shortcuts: profile.shortcuts.map((shortcut) =>
      shortcut.id === shortcutId
        ? {
            ...shortcut,
            deletedAt: timestamp,
            deletedByDeviceId: deviceId,
            updatedAt: timestamp
          }
        : shortcut
    )
  };
}

export function swapShortcutOrder(profile: Profile, sourceShortcutId: string, targetShortcutId: string): Profile {
  if (sourceShortcutId === targetShortcutId) return profile;

  const timestamp = now();
  const source = profile.shortcuts.find((shortcut) => shortcut.id === sourceShortcutId && !shortcut.deletedAt);
  const target = profile.shortcuts.find((shortcut) => shortcut.id === targetShortcutId && !shortcut.deletedAt);

  if (!source || !target || source.groupId !== target.groupId) return profile;

  return {
    ...profile,
    updatedAt: timestamp,
    shortcuts: profile.shortcuts.map((shortcut) => {
      if (shortcut.id === source.id) {
        return {
          ...shortcut,
          sortIndex: target.sortIndex,
          updatedAt: timestamp
        };
      }

      if (shortcut.id === target.id) {
        return {
          ...shortcut,
          sortIndex: source.sortIndex,
          updatedAt: timestamp
        };
      }

      return shortcut;
    })
  };
}

export function addShortcutGroup(profile: Profile, title: string): Profile {
  const timestamp = now();
  const cleanTitle = title.trim() || "新分组";
  const maxSortIndex = profile.groups.reduce((max, group) => Math.max(max, group.sortIndex), -1);
  const group: ShortcutGroup = {
    id: `group:${crypto.randomUUID()}`,
    title: cleanTitle,
    sortIndex: maxSortIndex + 1,
    createdAt: timestamp,
    updatedAt: timestamp
  };

  return {
    ...profile,
    updatedAt: timestamp,
    groups: [...profile.groups, group]
  };
}

export function renameShortcutGroup(profile: Profile, groupId: string, title: string): Profile {
  const timestamp = now();
  const cleanTitle = title.trim();
  if (!cleanTitle) return profile;

  return {
    ...profile,
    updatedAt: timestamp,
    groups: profile.groups.map((group) =>
      group.id === groupId
        ? {
            ...group,
            title: cleanTitle,
            updatedAt: timestamp
          }
        : group
    )
  };
}

export function swapShortcutGroupOrder(profile: Profile, sourceGroupId: string, targetGroupId: string): Profile {
  if (sourceGroupId === targetGroupId) return profile;

  const timestamp = now();
  const source = profile.groups.find((group) => group.id === sourceGroupId);
  const target = profile.groups.find((group) => group.id === targetGroupId);
  if (!source || !target) return profile;

  return {
    ...profile,
    updatedAt: timestamp,
    groups: profile.groups.map((group) => {
      if (group.id === source.id) {
        return { ...group, sortIndex: target.sortIndex, updatedAt: timestamp };
      }

      if (group.id === target.id) {
        return { ...group, sortIndex: source.sortIndex, updatedAt: timestamp };
      }

      return group;
    })
  };
}

export function deleteShortcutGroup(profile: Profile, groupId: string): Profile {
  const activeGroups = profile.groups.filter((group) => !group.deletedAt);
  if (activeGroups.length <= 1) return profile;

  const timestamp = now();
  const groupToDelete = activeGroups.find((group) => group.id === groupId);
  if (!groupToDelete) return profile;
  const remainingGroups = activeGroups.filter((group) => group.id !== groupId);

  const fallbackGroup = [...remainingGroups].sort((a, b) => a.sortIndex - b.sortIndex)[0];
  const movedShortcuts = profile.shortcuts.filter((shortcut) => shortcut.groupId === groupId && !shortcut.deletedAt);
  const activeInFallback = profile.shortcuts.filter(
    (shortcut) => shortcut.groupId === fallbackGroup.id && !shortcut.deletedAt
  );
  let nextSortIndex = activeInFallback.reduce((max, shortcut) => Math.max(max, shortcut.sortIndex), -1) + 1;

  return {
    ...profile,
    updatedAt: timestamp,
    groups: profile.groups.map((group) => {
      if (group.id === groupId) return { ...group, deletedAt: timestamp, updatedAt: timestamp };
      if (group.deletedAt) return group;
      const index = remainingGroups.findIndex((item) => item.id === group.id);
      return {
        ...group,
        sortIndex: index,
        updatedAt: group.id === fallbackGroup.id ? timestamp : group.updatedAt
      };
    }),
    shortcuts: profile.shortcuts.map((shortcut) => {
      if (shortcut.groupId !== groupId || shortcut.deletedAt) return shortcut;
      const movedIndex = movedShortcuts.findIndex((item) => item.id === shortcut.id);
      const sortIndex = movedIndex >= 0 ? nextSortIndex + movedIndex : nextSortIndex++;
      return {
        ...shortcut,
        groupId: fallbackGroup.id,
        sortIndex,
        updatedAt: timestamp
      };
    })
  };
}

export function setProfileWallpaper(profile: Profile, wallpaperId: string): Profile {
  const timestamp = now();
  const selected = parseWallpaperKey(wallpaperId);
  const selectedIds = profile.wallpaper.selectedIds.includes(wallpaperId)
    ? profile.wallpaper.selectedIds
    : [...profile.wallpaper.selectedIds, wallpaperId];

  return {
    ...profile,
    updatedAt: timestamp,
    wallpaper: {
      ...profile.wallpaper,
      selected,
      portableSelected:
        selected.kind === "local"
          ? profile.wallpaper.portableSelected ?? { kind: "builtin", id: "mist" }
          : selected,
      selectedIds
    }
  };
}

export function addWallpaperToSelection(profile: Profile, wallpaperId: string): Profile {
  const timestamp = now();
  if (profile.wallpaper.selectedIds.includes(wallpaperId)) return profile;

  return {
    ...profile,
    updatedAt: timestamp,
    wallpaper: {
      ...profile.wallpaper,
      selectedIds: [...profile.wallpaper.selectedIds, wallpaperId]
    }
  };
}

export function removeWallpaperFromSelection(profile: Profile, wallpaperId: string): Profile {
  const timestamp = now();
  const selectedIds = profile.wallpaper.selectedIds.filter((id) => id !== wallpaperId);
  const selectedKey = getWallpaperKey(profile);
  const nextSelectedKey = selectedKey === wallpaperId ? (selectedIds[0] ?? "mist") : selectedKey;

  return {
    ...profile,
    updatedAt: timestamp,
    wallpaper: {
      ...profile.wallpaper,
      selected: parseWallpaperKey(nextSelectedKey),
      selectedIds,
      rotationHistory: profile.wallpaper.rotationHistory.filter((id) => id !== wallpaperId)
    }
  };
}

export function getWallpaperKey(profile: Profile) {
  const selected = profile.wallpaper.selected;
  if (selected.kind === "local") return `local:${selected.assetId}`;
  if (selected.kind === "remote") return `web:${selected.id}:${selected.variantId}`;
  return selected.id;
}

export function parseWallpaperKey(wallpaperId: string): Profile["wallpaper"]["selected"] {
  if (wallpaperId.startsWith("local:")) {
    return {
      kind: "local",
      assetId: wallpaperId.slice("local:".length),
      localOnly: true
    };
  }

  if (wallpaperId.startsWith("web:")) {
    const parsed = parseRemoteWallpaperKey(wallpaperId);
    return {
      kind: "remote",
      id: parsed?.id ?? wallpaperId.slice("web:".length),
      variantId: parsed?.variantId ?? "4k"
    };
  }

  return {
    kind: "builtin",
    id: wallpaperId
  };
}
