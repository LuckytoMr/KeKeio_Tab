import type { Profile } from "../profile/types";
import type { RemoteWallpaper, WallpaperVariant } from "../wallpaper/repository";
import { fixedBackendUrl } from "./backendEndpoint";

export function attachBackendSession(
  profile: Profile,
  serverConfirmed: boolean,
  syncedAt: string
): Profile {
  return {
    ...profile,
    sync: {
      ...profile.sync,
      provider: "backend",
      status: serverConfirmed ? "ready" : "syncing",
      backendUrl: fixedBackendUrl,
      lastSyncedAt: serverConfirmed ? syncedAt : profile.sync.lastSyncedAt,
      errorMessage: undefined
    }
  };
}

type BackendWallpaperVariant = {
  id: WallpaperVariant["id"];
  label: string;
  url: string;
};

type BackendWallpaper = {
  id: string;
  title: string;
  provider: string;
  sourcePageUrl?: string;
  previewUrl?: string;
  category: RemoteWallpaper["category"];
  tags?: string[];
  variants?: BackendWallpaperVariant[];
};

function getVariantDimensions(label: string) {
  const match = label.match(/(\d+)\s*x\s*(\d+)/i);
  return {
    width: match ? Number(match[1]) : 3840,
    height: match ? Number(match[2]) : 2160
  };
}

export function normalizeBackendWallpaper(item: BackendWallpaper): RemoteWallpaper {
  const variants = (item.variants || [])
    .filter((variant) => variant.url)
    .map((variant) => {
      const dimensions = getVariantDimensions(variant.label);
      return {
        id: variant.id,
        label: variant.label,
        width: dimensions.width,
        height: dimensions.height,
        url: variant.url
      };
    });

  return {
    id: item.id,
    title: item.title,
    provider: item.provider,
    sourcePageUrl: item.sourcePageUrl || `backend:${item.id}`,
    previewUrl: item.previewUrl,
    previewCss:
      "linear-gradient(135deg, rgba(23, 37, 84, .9), rgba(15, 23, 42, .74)), radial-gradient(circle at 66% 38%, rgba(14,165,233,.45), transparent 30%)",
    category: item.category,
    orientation: "landscape",
    tags: item.tags || [],
    variants
  };
}

export function normalizeBackendWallpaperCatalog(value: unknown) {
  const record = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const items = Array.isArray(record.items) ? record.items as BackendWallpaper[] : [];
  const wallpapers = items.map(normalizeBackendWallpaper).filter((wallpaper) => wallpaper.variants.length > 0);
  return {
    wallpapers,
    nextCursor: typeof record.nextCursor === "string" ? record.nextCursor : undefined,
    page: typeof record.page === "number" ? record.page : 1,
    pageSize: typeof record.pageSize === "number" ? record.pageSize : wallpapers.length,
    total: typeof record.total === "number" ? record.total : wallpapers.length
  };
}
