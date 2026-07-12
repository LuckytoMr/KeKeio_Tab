import { describe, expect, it } from "vitest";
import {
  canUsePackagedWallpaperVariant,
  getRemoteWallpaperKey,
  parseRemoteWallpaperKey,
  getWallpaperPreviewBackground,
  getWallpaperPreviewUrl,
  remoteWallpapers
} from "./repository";

describe("wallpaper repository", () => {
  it("uses packaged assets for remote wallpaper previews", () => {
    expect(getWallpaperPreviewUrl("web:uhd-bunny-katana:4k")).toBe("/assets/wallpapers/uhd-bunny-katana-4k.jpg");
    expect(getWallpaperPreviewBackground("web:uhd-bunny-katana:2k")).toContain(
      'url("/assets/wallpapers/uhd-bunny-katana-2k.jpg")'
    );
  });

  it("can address packaged web wallpapers without downloading them again", () => {
    const wallpaper = remoteWallpapers[0];
    expect(getRemoteWallpaperKey(wallpaper)).toBe("web:uhd-bunny-katana:4k");
    expect(canUsePackagedWallpaperVariant(wallpaper.variants[0])).toBe(true);
  });

  it("parses web wallpaper keys whose provider ids contain colons", () => {
    expect(parseRemoteWallpaperKey("web:uhdpaper:284@5@k:2k")).toEqual({
      id: "uhdpaper:284@5@k",
      variantId: "2k"
    });
  });
});
