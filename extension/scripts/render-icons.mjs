import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const sourceUrl = new URL("../public/assets/icons/icon.svg", import.meta.url);
const outputUrl = new URL("../public/assets/icons/", import.meta.url);
const source = await readFile(sourceUrl, "utf8");
const browser = await chromium.launch({ headless: true });

try {
  for (const size of [16, 32, 48, 128]) {
    const page = await browser.newPage({ viewport: { width: size, height: size } });
    await page.setContent(`<style>html,body{margin:0;width:${size}px;height:${size}px;background:transparent}svg{display:block;width:${size}px;height:${size}px}</style>${source}`);
    await page.screenshot({
      path: fileURLToPath(new URL(`icon-${size}.png`, outputUrl)),
      omitBackground: true
    });
    await page.close();
  }
} finally {
  await browser.close();
}
