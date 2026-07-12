import { describe, expect, it } from "vitest";
import { createDefaultProfile } from "../profile/defaults";
import {
  addWallpaperToSelection,
  getWallpaperKey,
  removeWallpaperFromSelection,
  setProfileWallpaper
} from "../profile/mutations";

describe("wallpaper selection pool", () => {
  it("selects official, web, and local wallpaper keys", () => {
    let profile = createDefaultProfile();
    profile = setProfileWallpaper(profile, "web:uhd-bunny-katana:2k");
    expect(profile.wallpaper.selected).toEqual({ kind: "remote", id: "uhd-bunny-katana", variantId: "2k" });
    expect(profile.wallpaper.selectedIds).toContain("web:uhd-bunny-katana:2k");

    profile = setProfileWallpaper(profile, "local:asset-1");
    expect(profile.wallpaper.selected).toEqual({ kind: "local", assetId: "asset-1", localOnly: true });
    expect(getWallpaperKey(profile)).toBe("local:asset-1");
  });

  it("keeps dynamic web wallpaper ids intact when they contain provider separators", () => {
    const profile = setProfileWallpaper(createDefaultProfile(), "web:uhdpaper:284@5@k:4k");

    expect(profile.wallpaper.selected).toEqual({ kind: "remote", id: "uhdpaper:284@5@k", variantId: "4k" });
    expect(getWallpaperKey(profile)).toBe("web:uhdpaper:284@5@k:4k");
  });

  it("removes selected wallpapers and falls back to another selected item", () => {
    let profile = createDefaultProfile();
    profile = addWallpaperToSelection(profile, "web:uhd-bunny-katana:4k");
    profile = setProfileWallpaper(profile, "web:uhd-bunny-katana:4k");
    profile = removeWallpaperFromSelection(profile, "web:uhd-bunny-katana:4k");

    expect(profile.wallpaper.selectedIds).not.toContain("web:uhd-bunny-katana:4k");
    expect(getWallpaperKey(profile)).toBe("mist");
  });
});
