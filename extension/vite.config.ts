import preact from "@preact/preset-vite";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vite";

const rootDir = fileURLToPath(new URL(".", import.meta.url));
const uhdpaperReferer = "https://www.uhdpaper.com/";

function assertUhdpaperPageUrl(rawUrl: string) {
  const url = new URL(rawUrl);
  if (url.protocol !== "https:" || url.hostname !== "www.uhdpaper.com") {
    throw new Error("Only UHDpaper pages can be proxied");
  }
  return url.href;
}

function assertUhdpaperImageUrl(rawUrl: string) {
  const url = new URL(rawUrl);
  if (
    url.protocol !== "https:" ||
    !url.hostname.endsWith(".uhdpaper.com") ||
    !url.pathname.startsWith("/wallpaper/")
  ) {
    throw new Error("Only UHDpaper wallpaper images can be proxied");
  }
  return url.href;
}

function toDataUrl(buffer: ArrayBuffer, mimeType: string) {
  return `data:${mimeType};base64,${Buffer.from(buffer).toString("base64")}`;
}

function uhdpaperDevProxy(): Plugin {
  return {
    name: "full-pro-uhdpaper-dev-proxy",
    configureServer(server) {
      server.middlewares.use("/__fullpro_proxy/uhdpaper", async (request, response) => {
        try {
          const requestUrl = new URL(request.url ?? "", "http://localhost");
          const rawTarget = requestUrl.searchParams.get("url");
          if (!rawTarget) throw new Error("Missing url");

          const isImage = requestUrl.pathname.endsWith("/image");
          const targetUrl = isImage ? assertUhdpaperImageUrl(rawTarget) : assertUhdpaperPageUrl(rawTarget);
          const upstream = await fetch(targetUrl, {
            headers: {
              referer: uhdpaperReferer,
              "user-agent": "Mozilla/5.0"
            }
          });

          if (!upstream.ok) throw new Error(`Upstream failed: ${upstream.status}`);

          response.setHeader("content-type", "application/json; charset=utf-8");
          response.setHeader("cache-control", "no-store");

          if (isImage) {
            const mimeType = upstream.headers.get("content-type") ?? "application/octet-stream";
            if (!mimeType.startsWith("image/")) throw new Error("Upstream did not return an image");
            response.end(JSON.stringify({ mimeType, dataUrl: toDataUrl(await upstream.arrayBuffer(), mimeType) }));
            return;
          }

          response.end(JSON.stringify({ html: await upstream.text() }));
        } catch (error) {
          response.statusCode = 502;
          response.setHeader("content-type", "application/json; charset=utf-8");
          response.end(JSON.stringify({ error: error instanceof Error ? error.message : "Proxy failed" }));
        }
      });
    }
  };
}

export default defineConfig({
  plugins: [preact(), uhdpaperDevProxy()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: {
      input: {
        newtab: resolve(rootDir, "newtab.html"),
        options: resolve(rootDir, "options.html"),
        "service-worker": resolve(rootDir, "src/background/service-worker.ts")
      },
      output: {
        entryFileNames: (chunk) =>
          chunk.name === "service-worker" ? "service-worker.js" : "assets/[name]-[hash].js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash][extname]"
      }
    }
  }
});
