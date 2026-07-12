import { db, type LocalAsset } from "../storage/db";

export type BuiltinWallpaper = {
  id: string;
  title: string;
  category: "soft" | "nature" | "dark" | "color" | "solid";
  tone: string;
  css: string;
};

export type WallpaperVariant = {
  id: "4k" | "2k" | "hd";
  label: string;
  width: number;
  height: number;
  assetUrl?: string;
  url: string;
};

export type RemoteWallpaper = {
  id: string;
  title: string;
  provider: string;
  sourcePageUrl: string;
  previewUrl?: string;
  previewCss: string;
  category: "anime" | "digital" | "nature" | "city";
  orientation: "landscape" | "portrait";
  tags: string[];
  variants: WallpaperVariant[];
};

export const builtinWallpapers: BuiltinWallpaper[] = [
  {
    id: "mist",
    title: "浅雾",
    category: "soft",
    tone: "#dff3ff",
    css:
      "radial-gradient(circle at 18% 78%, rgba(89, 186, 255, .24), transparent 32%), linear-gradient(145deg, #f8fbff 0%, #edf8ff 48%, #ffffff 100%)"
  },
  {
    id: "mineral",
    title: "矿石",
    category: "nature",
    tone: "#d7f0e8",
    css:
      "linear-gradient(135deg, rgba(14, 91, 113, .16), transparent 38%), linear-gradient(160deg, #f7fbfa 0%, #e9f5f1 50%, #ffffff 100%)"
  },
  {
    id: "dawn",
    title: "晨光",
    category: "soft",
    tone: "#f6d7ca",
    css:
      "linear-gradient(120deg, rgba(230, 115, 81, .18), transparent 36%), linear-gradient(145deg, #fffaf7 0%, #f3f5ff 52%, #ffffff 100%)"
  },
  {
    id: "aurora",
    title: "极光",
    category: "color",
    tone: "#b7f5e7",
    css:
      "radial-gradient(circle at 22% 28%, rgba(35, 214, 181, .38), transparent 30%), radial-gradient(circle at 76% 36%, rgba(87, 120, 255, .28), transparent 34%), linear-gradient(140deg, #f7fffd 0%, #eef2ff 56%, #ffffff 100%)"
  },
  {
    id: "ink",
    title: "墨蓝",
    category: "dark",
    tone: "#1d2a3d",
    css:
      "radial-gradient(circle at 22% 20%, rgba(45, 119, 255, .42), transparent 32%), radial-gradient(circle at 80% 70%, rgba(22, 184, 142, .24), transparent 28%), linear-gradient(145deg, #151b25 0%, #23334a 56%, #11161d 100%)"
  },
  {
    id: "forest",
    title: "林间",
    category: "nature",
    tone: "#cdebd6",
    css:
      "radial-gradient(circle at 18% 72%, rgba(73, 170, 113, .28), transparent 34%), linear-gradient(160deg, #f6fbf5 0%, #deefe2 48%, #f9fbf7 100%)"
  },
  {
    id: "lake",
    title: "湖面",
    category: "nature",
    tone: "#c7ebf4",
    css:
      "linear-gradient(180deg, rgba(255,255,255,.6), transparent 40%), radial-gradient(circle at 50% 86%, rgba(47, 161, 205, .28), transparent 46%), linear-gradient(130deg, #f8feff 0%, #dff4fb 55%, #ffffff 100%)"
  },
  {
    id: "pearl",
    title: "珍珠",
    category: "soft",
    tone: "#f1eef8",
    css:
      "radial-gradient(circle at 68% 28%, rgba(175, 137, 255, .2), transparent 30%), radial-gradient(circle at 24% 74%, rgba(255, 177, 201, .18), transparent 32%), linear-gradient(145deg, #ffffff 0%, #f4f1fb 100%)"
  },
  {
    id: "ember",
    title: "余烬",
    category: "dark",
    tone: "#3a2321",
    css:
      "radial-gradient(circle at 70% 74%, rgba(255, 116, 70, .35), transparent 32%), radial-gradient(circle at 28% 24%, rgba(255, 198, 109, .16), transparent 28%), linear-gradient(150deg, #1d1717 0%, #3b2422 54%, #171516 100%)"
  },
  {
    id: "plum",
    title: "梅影",
    category: "color",
    tone: "#ead7ec",
    css:
      "radial-gradient(circle at 72% 28%, rgba(194, 92, 172, .25), transparent 31%), linear-gradient(135deg, #fff8ff 0%, #f0e4f2 52%, #ffffff 100%)"
  },
  {
    id: "graphite",
    title: "石墨",
    category: "solid",
    tone: "#edf0f2",
    css: "linear-gradient(145deg, #f7f8f9 0%, #e8edf0 48%, #ffffff 100%)"
  },
  {
    id: "paper",
    title: "白纸",
    category: "solid",
    tone: "#ffffff",
    css: "linear-gradient(145deg, #ffffff 0%, #f7f9fb 100%)"
  }
];

export const wallpaperCategories = [
  { id: "all", title: "全部" },
  { id: "soft", title: "浅色" },
  { id: "nature", title: "自然" },
  { id: "dark", title: "深色" },
  { id: "color", title: "彩色" },
  { id: "solid", title: "纯色" },
  { id: "local", title: "本地" }
] as const;

export const webWallpaperCategories = [
  { id: "all", title: "全部" },
  { id: "anime", title: "动漫" },
  { id: "digital", title: "数字艺术" },
  { id: "nature", title: "自然" },
  { id: "city", title: "城市" }
] as const;

export const remoteWallpapers: RemoteWallpaper[] = [
  {
    id: "uhd-bunny-katana",
    title: "Bunny Ears Katana",
    provider: "UHDpaper",
    sourcePageUrl: "https://www.uhdpaper.com/2025/03/3545d-anime-girl-bunny-ears-katana-4k.html?m=0",
    previewCss:
      "radial-gradient(circle at 62% 38%, rgba(255,255,255,.84), transparent 13%), radial-gradient(circle at 66% 43%, rgba(255,87,170,.42), transparent 18%), radial-gradient(circle at 42% 32%, rgba(58,209,231,.58), transparent 28%), linear-gradient(135deg, #102436 0%, #0b1521 42%, #4c1944 78%, #10131d 100%)",
    category: "anime",
    orientation: "landscape",
    tags: ["4K", "Anime", "Katana", "White Hair"],
    variants: [
      {
        id: "4k",
        label: "3840x2160",
        width: 3840,
        height: 2160,
        assetUrl: "/assets/wallpapers/uhd-bunny-katana-4k.jpg",
        url: "https://image-5.uhdpaper.com/wallpaper/anime-girl-bunny-ears-katana-white-hair-4k-wallpaper-uhdpaper.com-354@5@d.jpg"
      },
      {
        id: "2k",
        label: "2560x1440",
        width: 2560,
        height: 1440,
        assetUrl: "/assets/wallpapers/uhd-bunny-katana-2k.jpg",
        url: "https://image-5.uhdpaper.com/wallpaper/anime-girl-bunny-ears-katana-white-hair-2k-wallpaper-uhdpaper.com-354@5@d.jpg"
      },
      {
        id: "hd",
        label: "1920x1080",
        width: 1920,
        height: 1080,
        assetUrl: "/assets/wallpapers/uhd-bunny-katana-hd.jpg",
        url: "https://image-5.uhdpaper.com/wallpaper/anime-girl-bunny-ears-katana-white-hair-hd-wallpaper-uhdpaper.com-354@5@d.jpg"
      }
    ]
  }
];

export function getWallpaperVariantUrl(variant: WallpaperVariant | undefined) {
  return variant?.assetUrl ?? variant?.url;
}

export function getRemoteWallpaperKey(wallpaper: RemoteWallpaper, variantId = wallpaper.variants[0]?.id ?? "4k") {
  return `web:${wallpaper.id}:${variantId}`;
}

export function canUsePackagedWallpaperVariant(variant: WallpaperVariant | undefined) {
  return Boolean(variant?.assetUrl);
}

function isWallpaperVariantId(value: string | undefined): value is WallpaperVariant["id"] {
  return value === "4k" || value === "2k" || value === "hd";
}

export function parseRemoteWallpaperKey(wallpaperId: string) {
  if (!wallpaperId.startsWith("web:")) return undefined;

  const parts = wallpaperId.slice("web:".length).split(":").filter(Boolean);
  if (parts.length === 0) return undefined;

  const last = parts.at(-1);
  const variantId = isWallpaperVariantId(last) ? last : "4k";
  const idParts = isWallpaperVariantId(last) && parts.length > 1 ? parts.slice(0, -1) : parts;
  const id = idParts.join(":");

  return id ? { id, variantId } : undefined;
}

export function getWallpaperPreviewUrl(wallpaperId: string) {
  if (wallpaperId.startsWith("local:")) return undefined;
  if (wallpaperId.startsWith("web:")) {
    const parsed = parseRemoteWallpaperKey(wallpaperId);
    const wallpaper = remoteWallpapers.find((item) => item.id === parsed?.id);
    return getWallpaperVariantUrl(
      wallpaper?.variants.find((variant) => variant.id === parsed?.variantId) ?? wallpaper?.variants[0]
    );
  }

  return builtinWallpapers.find((item) => item.id === wallpaperId)?.css;
}

export function getWallpaperPreviewBackground(wallpaperId: string) {
  if (wallpaperId.startsWith("web:")) {
    const parsed = parseRemoteWallpaperKey(wallpaperId);
    const wallpaper = remoteWallpapers.find((item) => item.id === parsed?.id);
    const variant = wallpaper?.variants.find((item) => item.id === parsed?.variantId) ?? wallpaper?.variants[0];
    const previewUrl = getWallpaperVariantUrl(variant);
    return previewUrl && wallpaper ? `url("${previewUrl}"), ${wallpaper.previewCss}` : wallpaper?.previewCss;
  }

  return getWallpaperPreviewUrl(wallpaperId);
}

export function getWallpaperTitle(wallpaperId: string) {
  if (wallpaperId.startsWith("local:")) return "本地图片";
  if (wallpaperId.startsWith("web:")) {
    const parsed = parseRemoteWallpaperKey(wallpaperId);
    const wallpaper = remoteWallpapers.find((item) => item.id === parsed?.id);
    const variant = wallpaper?.variants.find((item) => item.id === parsed?.variantId);
    return wallpaper ? `${wallpaper.title} ${variant?.label ?? ""}`.trim() : wallpaperId;
  }

  return builtinWallpapers.find((item) => item.id === wallpaperId)?.title ?? wallpaperId;
}

export async function saveLocalWallpaper(file: File) {
  const timestamp = new Date().toISOString();
  const asset: LocalAsset = {
    assetId: crypto.randomUUID(),
    type: "wallpaper",
    name: file.name,
    mimeType: file.type,
    size: file.size,
    blob: file,
    createdAt: timestamp,
    lastUsedAt: timestamp
  };

  await db.assets.put(asset);
  return asset;
}

export async function listLocalWallpapers() {
  return db.assets.where("type").equals("wallpaper").reverse().sortBy("createdAt");
}

export async function getLocalAssetUrl(assetId: string) {
  const asset = await db.assets.get(assetId);
  return asset ? URL.createObjectURL(asset.blob) : undefined;
}
