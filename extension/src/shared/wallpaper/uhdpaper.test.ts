import { describe, expect, it } from "vitest";
import { parseUhdpaperListPage } from "./uhdpaper";

const fixture = `
<article class="post-outer-container">
  <div class="wp_box">
    <a href="https://www.uhdpaper.com/2025/03/3545d-anime-girl-bunny-ears-katana-4k.html?m=0" target="_blank"><span></span></a>
    <div class="snippet-title _ds">
      <h2 style="font-size: 11px;">Anime Girl Bunny Ears Katana</h2>
      <img alt="" src="https://img.uhdpaper.com/wallpaper/anime-girl-bunny-ears-katana-white-hair-354@5@d-thumb.jpg?dl" />
    </div>
    <b>4K</b>
  </div>
</article>
<article class="post-outer-container">
  <div class="wp_box">
    <a href="/2026/07/cyberpunk-ninja-night-wallpaper-2285q.html" target="_blank"><span></span></a>
    <div class="snippet-title _ds">
      <h2 style="font-size: 11px;">Cyberpunk Ninja Night</h2>
      <img alt="" src="https://img.uhdpaper.com/wallpaper/cyberpunk-ninja-night-city-sci-fi-228@5@q">
    </div>
    <b>4K</b>
  </div>
</article>
<a href="/search?updated-max=2026-07-05T19:08:00%2B08:00&max-results=20">Next &#187;</a>
`;

describe("UHDpaper parser", () => {
  it("parses list cards into remote wallpaper records with next-page cursor", () => {
    const result = parseUhdpaperListPage(fixture, "https://www.uhdpaper.com/");

    expect(result.nextPageUrl).toBe("https://www.uhdpaper.com/search?updated-max=2026-07-05T19:08:00%2B08:00&max-results=20");
    expect(result.wallpapers).toHaveLength(2);
    expect(result.wallpapers[0]).toMatchObject({
      id: "uhdpaper:354@5@d",
      title: "Anime Girl Bunny Ears Katana",
      provider: "UHDpaper",
      sourcePageUrl: "https://www.uhdpaper.com/2025/03/3545d-anime-girl-bunny-ears-katana-4k.html?m=0",
      category: "anime",
      orientation: "landscape",
      previewUrl: "https://img.uhdpaper.com/wallpaper/anime-girl-bunny-ears-katana-white-hair-354@5@d-thumb.jpg?dl"
    });
    expect(result.wallpapers[0].variants.map((variant) => [variant.id, variant.label, variant.url])).toEqual([
      [
        "4k",
        "3840x2160",
        "https://img.uhdpaper.com/wallpaper/anime-girl-bunny-ears-katana-white-hair-354@5@d-pc-4k.jpg"
      ],
      [
        "2k",
        "2560x1440",
        "https://img.uhdpaper.com/wallpaper/anime-girl-bunny-ears-katana-white-hair-354@5@d-pc-2k.jpg"
      ],
      [
        "hd",
        "1920x1080",
        "https://img.uhdpaper.com/wallpaper/anime-girl-bunny-ears-katana-white-hair-354@5@d-pc-hd.jpg"
      ]
    ]);
    expect(result.wallpapers[1].category).toBe("digital");
    expect(result.wallpapers[1].previewUrl).toBe(
      "https://img.uhdpaper.com/wallpaper/cyberpunk-ninja-night-city-sci-fi-228@5@q-thumb.jpg?dl"
    );
  });
});
