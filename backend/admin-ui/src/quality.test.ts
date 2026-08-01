import { readFileSync, readdirSync, statSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const root = resolve(import.meta.dirname, "..");

function sourceFiles(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const path = resolve(directory, name);
    return statSync(path).isDirectory() ? sourceFiles(path) : /\.(ts|tsx)$/.test(name) && !name.includes(".test.") ? [path] : [];
  });
}

describe("production UI quality gates", () => {
  it("ships a semantic Chinese SPA shell", () => {
    const html = readFileSync(resolve(root, "index.html"), "utf8");
    expect(html).toContain('<html lang="zh-CN">');
    expect(html).toContain('name="viewport"');
    expect(html).toContain('<div id="app"></div>');
    expect(html).toContain("<title>kekeio 运维工作台</title>");
  });

  it("defines structural breakpoints, focus visibility and reduced motion", () => {
    const css = readFileSync(resolve(root, "src/styles.css"), "utf8");
    expect(css).toContain("@media (max-width: 1199px)");
    expect(css).toContain("@media (max-width: 767px)");
    expect(css).toContain("@media (max-width: 390px)");
    expect(css).toContain(":focus-visible");
    expect(css).toContain("prefers-reduced-motion: reduce");
    expect(css).toContain("overflow-wrap: anywhere");
    expect(css).not.toContain("background-clip: text");
    expect(css).not.toMatch(/border-radius:\s*(?:2[4-9]|[3-9]\d)px/);
  });

  it("keeps the open mobile drawer above its header and scrim but below dialogs and toasts", () => {
    const css = readFileSync(resolve(root, "src/styles.css"), "utf8");
    const layer = (name: string) => Number(css.match(new RegExp(`--${name}:\\s*(\\d+)`))?.[1]);
    const header = layer("z-mobile-header");
    const scrim = layer("z-scrim");
    const sidebar = layer("z-sidebar");
    const dialog = layer("z-dialog");
    const toast = layer("z-toast");

    expect(header).toBeLessThan(scrim);
    expect(scrim).toBeLessThan(sidebar);
    expect(sidebar).toBeLessThan(dialog);
    expect(dialog).toBeLessThan(toast);
    expect(css).toMatch(/\.mobile-header\s*\{[^}]*z-index:\s*var\(--z-mobile-header\)/s);
    expect(css).toMatch(/\.admin-sidebar\s*\{[^}]*z-index:\s*var\(--z-sidebar\)/s);
  });

  it("lets an odd final sync metric span the full summary row", () => {
    const css = readFileSync(resolve(root, "src/styles.css"), "utf8");
    expect(css).toMatch(/\.sync-summary dl > div:last-child:nth-child\(odd\)\s*\{[^}]*grid-column:\s*1 \/ -1/s);
  });

  it("never inserts API content through innerHTML", () => {
    const source = sourceFiles(resolve(root, "src")).map((path) => readFileSync(path, "utf8")).join("\n");
    expect(source).not.toContain(".innerHTML");
    expect(source).not.toContain("dangerouslySetInnerHTML");
  });
});
