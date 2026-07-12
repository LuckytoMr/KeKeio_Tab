import { fileURLToPath, URL } from "node:url";
import preact from "@preact/preset-vite";
import { defineConfig } from "vite";

export default defineConfig({
  base: "/admin/assets/",
  plugins: [preact()],
  build: {
    outDir: fileURLToPath(new URL("../internal/server/web/admin", import.meta.url)),
    emptyOutDir: true,
    assetsDir: "",
    rollupOptions: {
      output: {
        entryFileNames: "admin-[hash].js",
        chunkFileNames: "admin-[hash].js",
        assetFileNames: "admin-[hash][extname]"
      }
    }
  }
});
