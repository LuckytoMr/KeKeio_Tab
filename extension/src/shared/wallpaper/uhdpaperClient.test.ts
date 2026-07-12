import { describe, expect, it } from "vitest";
import { getUhdpaperDevProxyUrl } from "./uhdpaperClient";

describe("UHDpaper dev proxy urls", () => {
  it("encodes page and image requests through the local dev proxy", () => {
    const pageUrl = getUhdpaperDevProxyUrl("page", "https://www.uhdpaper.com/?m=0");
    const imageUrl = getUhdpaperDevProxyUrl(
      "image",
      "https://img.uhdpaper.com/wallpaper/anime-girl-bunny-ears-katana-white-hair-354@5@d-thumb.jpg?dl"
    );

    expect(pageUrl).toBe("/__fullpro_proxy/uhdpaper/page?url=https%3A%2F%2Fwww.uhdpaper.com%2F%3Fm%3D0");
    expect(imageUrl).toContain("/__fullpro_proxy/uhdpaper/image?url=");
    expect(imageUrl).toContain("anime-girl-bunny-ears-katana-white-hair");
  });
});
