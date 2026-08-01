import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchUhdpaperImageDataUrl, getUhdpaperDevProxyUrl, loadUhdpaperWallpaperPage } from "./uhdpaperClient";

afterEach(() => vi.unstubAllGlobals());

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

  it("uses authenticated catalog messages in an extension runtime", async () => {
    const sendMessage = vi.fn(async (message: { kind?: string }) => message.kind === "uhdpaper-page"
      ? { ok: true, data: { html: "<html></html>" } }
      : { ok: true, data: { mimeType: "image/jpeg", dataUrl: "data:image/jpeg;base64,AA==" } });
    vi.stubGlobal("chrome", { runtime: { id: "extension-id", sendMessage } });

    await expect(loadUhdpaperWallpaperPage("https://www.uhdpaper.com/?page=2"))
      .resolves.toEqual({ wallpapers: [], nextPageUrl: undefined });
    await expect(fetchUhdpaperImageDataUrl("https://img.uhdpaper.com/wallpaper/space.jpg"))
      .resolves.toBe("data:image/jpeg;base64,AA==");

    expect(sendMessage.mock.calls.map(([message]) => message)).toEqual([
      {
        type: "catalog:get",
        kind: "uhdpaper-page",
        query: "https://www.uhdpaper.com/?page=2"
      },
      {
        type: "catalog:get",
        kind: "uhdpaper-image",
        query: "https://img.uhdpaper.com/wallpaper/space.jpg"
      }
    ]);
  });

  it("does not fall back to a direct UHDpaper request outside the extension and dev proxy", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    await expect(loadUhdpaperWallpaperPage("https://www.uhdpaper.com/?page=2"))
      .rejects.toThrow("当前环境不能通过扩展后台加载 UHDpaper 页面");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
