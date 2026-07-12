import { describe, expect, test } from "vitest";
import { createDefaultProfile } from "../profile/defaults";
import { attachBackendSession, normalizeBackendWallpaperCatalog } from "./backendClient";

describe("normalizeBackendWallpaperCatalog", () => {
  test("accepts the v1 cursor-paginated catalog envelope", () => {
    const page = normalizeBackendWallpaperCatalog({
      items: [{
        id: "wallpaper:one",
        title: "One",
        provider: "official",
        category: "nature",
        variants: [{ id: "4k", label: "3840x2160", url: "https://cdn.example.test/one.jpg" }]
      }],
      nextCursor: "next-1"
    });

    expect(page.nextCursor).toBe("next-1");
    expect(page.wallpapers).toMatchObject([{ id: "wallpaper:one", variants: [{ width: 3840, height: 2160 }] }]);
  });
});

describe("attachBackendSession", () => {
  test("adds fixed-backend sync metadata to the explicitly supplied profile", () => {
    const remote = createDefaultProfile();
    remote.theme.columns = 8;
    const result = attachBackendSession(remote, true, "2026-07-12T02:00:00.000Z");

    expect(result.theme.columns).toBe(8);
    expect(result.sync).toMatchObject({
      provider: "backend",
      status: "ready",
      backendUrl: "https://tab.kekeio.com",
      lastSyncedAt: "2026-07-12T02:00:00.000Z"
    });
  });
});
