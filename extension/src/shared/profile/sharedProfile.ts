import { z } from "zod";
import { migrateProfile } from "./migrate";
import { normalizeShortcutIconSize } from "./theme";
import type { Profile, ShortcutIcon } from "./types";

const shortText = z.string().min(1).max(32);
const identifier = z.string().min(1).max(256);
const timestamp = z.string().datetime({ offset: true });
const httpUrl = z.string().url().max(2048).refine((value) => {
  const url = new URL(value);
  return (url.protocol === "https:" || url.protocol === "http:") && !url.username && !url.password;
}, "Only credential-free HTTP(S) URLs are portable");
const httpsUrl = z.string().url().max(2048).refine((value) => {
  const url = new URL(value);
  return url.protocol === "https:" && !url.username && !url.password;
}, "Remote icon URLs must use credential-free HTTPS");

const portableIconSchema = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("text"), text: shortText, bg: z.string().max(64).optional(), fg: z.string().max(64).optional() }).strict(),
  z.object({ kind: z.literal("favicon"), url: httpsUrl, fallbackText: shortText }).strict(),
  z.object({ kind: z.literal("url"), url: httpsUrl, fallbackText: shortText }).strict(),
  z.object({ kind: z.literal("preset"), presetId: identifier }).strict()
]);

const groupSchema = z.object({
  id: identifier,
  title: z.string().min(1).max(160),
  sortIndex: z.number().int().nonnegative(),
  createdAt: timestamp,
  updatedAt: timestamp,
  deletedAt: timestamp.optional()
}).strict();

const shortcutSchema = z.object({
  id: identifier,
  groupId: identifier,
  title: z.string().min(1).max(240),
  url: httpUrl,
  icon: portableIconSchema,
  sortIndex: z.number().int().nonnegative(),
  createdAt: timestamp,
  updatedAt: timestamp,
  deletedAt: timestamp.optional()
}).strict();

const searchEngineSchema = z.object({
  id: identifier,
  title: z.string().min(1).max(120),
  template: z.string().min(1).max(2048)
}).strict();

const portableWallpaperSchema = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("builtin"), id: identifier }).strict(),
  z.object({ kind: z.literal("remote"), id: identifier, variantId: identifier }).strict()
]);
const shortcutIconSizeSchema = z
  .enum(["tiny", "mini", "small", "medium", "large", "xlarge"])
  .transform((value) => normalizeShortcutIconSize(value));

const sharedProfileV2BaseSchema = z.object({
  schemaVersion: z.literal(2),
  profileId: identifier,
  updatedAt: timestamp,
  groups: z.array(groupSchema).max(256),
  shortcuts: z.array(shortcutSchema).max(4096),
  search: z.object({
    mode: z.enum(["browser-default", "custom"]),
    disposition: z.enum(["CURRENT_TAB", "NEW_TAB"]),
    selectedEngineId: identifier,
    engines: z.array(searchEngineSchema).max(128)
  }).strict(),
  wallpaper: z.object({
    selected: portableWallpaperSchema,
    selectedIds: z.array(z.string().min(1).max(512)).max(1024),
    rotationMode: z.enum(["manual", "random"]),
    rotationSource: z.enum(["selected", "web"]),
    rotationIntervalSeconds: z.number().int().min(1).max(86400),
    overlayOpacity: z.number().min(0).max(1),
    blur: z.number().min(0).max(100)
  }).strict(),
  theme: z.object({
    styleId: identifier,
    density: z.enum(["comfortable", "compact"]),
    sidebarSide: z.enum(["left", "right"]),
    showBrand: z.boolean(),
    columns: z.union([z.literal(4), z.literal(5), z.literal(6), z.literal(7), z.literal(8)]),
    rows: z.union([z.literal(1), z.literal(2), z.literal(3), z.literal(4), z.literal(5)]),
    iconSize: shortcutIconSizeSchema,
    iconShape: z.enum(["circle", "rounded", "squircle", "soft"])
  }).strict()
}).strict();

export const sharedProfileV2Schema = sharedProfileV2BaseSchema.superRefine((profile, context) => {
  const addDuplicateIssues = (items: Array<{ id: string }>, path: "groups" | "shortcuts" | "engines") => {
    const seen = new Set<string>();
    items.forEach((item, index) => {
      if (seen.has(item.id)) {
        context.addIssue({
          code: z.ZodIssueCode.custom,
          path: path === "engines" ? ["search", "engines", index, "id"] : [path, index, "id"],
          message: `Duplicate ${path} id`
        });
      }
      seen.add(item.id);
    });
  };

  addDuplicateIssues(profile.groups, "groups");
  addDuplicateIssues(profile.shortcuts, "shortcuts");
  addDuplicateIssues(profile.search.engines, "engines");

  const groupIDs = new Set(profile.groups.map((group) => group.id));
  profile.shortcuts.forEach((shortcut, index) => {
    if (!groupIDs.has(shortcut.groupId)) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["shortcuts", index, "groupId"],
        message: "Shortcut references an unknown group"
      });
    }
  });

  if (
    profile.search.mode === "custom" &&
    !profile.search.engines.some((engine) => engine.id === profile.search.selectedEngineId)
  ) {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      path: ["search", "selectedEngineId"],
      message: "Selected search engine is missing"
    });
  }

  profile.wallpaper.selectedIds.forEach((id, index) => {
    if (id.startsWith("local:")) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["wallpaper", "selectedIds", index],
        message: "Local wallpaper ids are not portable"
      });
    }
  });
});

export type SharedProfileV2 = z.infer<typeof sharedProfileV2Schema>;

function portableIcon(icon: ShortcutIcon, title: string): SharedProfileV2["shortcuts"][number]["icon"] {
  if (icon.kind !== "local") return portableIconSchema.parse(icon);
  const text = Array.from(title.trim())[0]?.toUpperCase() || "?";
  return { kind: "text", text };
}

function portableWallpaper(profile: Profile): SharedProfileV2["wallpaper"]["selected"] {
  const selected = profile.wallpaper.selected;
  return selected.kind === "local"
    ? profile.wallpaper.portableSelected ?? { kind: "builtin", id: "mist" }
    : selected;
}

export function toSharedProfileV2(profile: Profile): SharedProfileV2 {
  return sharedProfileV2Schema.parse({
    schemaVersion: 2,
    profileId: profile.profileId,
    updatedAt: profile.updatedAt,
    groups: profile.groups.map(({ id, title, sortIndex, createdAt, updatedAt, ...rest }) => ({
      id,
      title,
      sortIndex,
      createdAt,
      updatedAt,
      ...(typeof (rest as { deletedAt?: unknown }).deletedAt === "string" && (rest as { deletedAt: string }).deletedAt
        ? { deletedAt: (rest as { deletedAt: string }).deletedAt }
        : {})
    })),
    shortcuts: profile.shortcuts.map(({ id, groupId, title, url, icon, sortIndex, createdAt, updatedAt, deletedAt }) => ({
      id,
      groupId,
      title,
      url,
      icon: portableIcon(icon, title),
      sortIndex,
      createdAt,
      updatedAt,
      ...(deletedAt ? { deletedAt } : {})
    })),
    search: {
      mode: profile.search.mode,
      disposition: profile.search.disposition,
      selectedEngineId: profile.search.selectedEngineId,
      engines: profile.search.engines.map(({ id, title, template }) => ({ id, title, template }))
    },
    wallpaper: {
      selected: portableWallpaper(profile),
      selectedIds: Array.from(new Set(profile.wallpaper.selectedIds.filter((id) => !id.startsWith("local:")))),
      rotationMode: profile.wallpaper.rotationMode,
      rotationSource: profile.wallpaper.rotationSource,
      rotationIntervalSeconds: profile.wallpaper.rotationIntervalSeconds,
      overlayOpacity: profile.wallpaper.overlayOpacity,
      blur: profile.wallpaper.blur
    },
    theme: {
      styleId: profile.theme.styleId,
      density: profile.theme.density,
      sidebarSide: profile.theme.sidebarSide,
      showBrand: profile.theme.showBrand,
      columns: profile.theme.columns,
      rows: profile.theme.rows,
      iconSize: profile.theme.iconSize,
      iconShape: profile.theme.iconShape
    }
  });
}

export function parseSharedProfileV2(value: unknown): SharedProfileV2 {
  const parsed = sharedProfileV2Schema.safeParse(value);
  if (!parsed.success) {
    throw new Error(`Invalid SharedProfileV2: ${parsed.error.issues.map((issue) => issue.path.join(".")).join(", ")}`);
  }
  return parsed.data;
}

export function sharedProfileToLocalProfile(sharedInput: SharedProfileV2, localInput: Profile): Profile {
  const shared = parseSharedProfileV2(sharedInput);
  const local = migrateProfile(localInput);
  const localIcons = new Map(local.shortcuts.filter((item) => item.icon.kind === "local").map((item) => [item.id, item.icon]));
  const localSelectedIds = local.wallpaper.selectedIds.filter((id) => id.startsWith("local:"));
  const selected = local.wallpaper.selected.kind === "local" ? local.wallpaper.selected : shared.wallpaper.selected;

  return migrateProfile({
    ...local,
    profileId: shared.profileId,
    updatedAt: shared.updatedAt,
    groups: shared.groups.map((group) => ({ ...group })),
    shortcuts: shared.shortcuts.map((shortcut) => ({
      ...shortcut,
      icon: localIcons.get(shortcut.id) ?? shortcut.icon
    })),
    search: { ...shared.search, engines: shared.search.engines.map((engine) => ({ ...engine })) },
    wallpaper: {
      ...local.wallpaper,
      ...shared.wallpaper,
      selected,
      portableSelected: shared.wallpaper.selected,
      selectedIds: Array.from(new Set([...localSelectedIds, ...shared.wallpaper.selectedIds])),
      rotationHistory: local.wallpaper.rotationHistory,
      activeSourceTab: local.wallpaper.activeSourceTab,
      activeCategory: local.wallpaper.activeCategory
    },
    theme: { ...shared.theme },
    deviceId: local.deviceId,
    sync: local.sync,
    schemaVersion: 1
  });
}
