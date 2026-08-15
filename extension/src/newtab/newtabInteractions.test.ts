import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");
const cssSource = readFileSync(new URL("../styles/app.css", import.meta.url), "utf8");

function sourceBetween(source: string, startMarker: string, endMarker: string) {
  const start = source.indexOf(startMarker);
  const end = source.indexOf(endMarker, start + startMarker.length);
  if (start < 0 || end < 0) throw new Error(`找不到源码片段：${startMarker} -> ${endMarker}`);
  return source.slice(start, end);
}

function cssRule(selector: string) {
  const marker = `${selector} {`;
  const start = cssSource.indexOf(marker);
  if (start < 0) throw new Error(`找不到 CSS 规则：${selector}`);
  const end = cssSource.indexOf("\n}", start + marker.length);
  if (end < 0) throw new Error(`CSS 规则未闭合：${selector}`);
  return cssSource.slice(start, end + 2);
}

const groupWheelSource = sourceBetween(
  appSource,
  "function handleGroupWheel(",
  "useEffect(() => {\n    if (!shortcutEditMode) return;"
);
const nativeWheelSurfaceSource = sourceBetween(
  appSource,
  "const nativeWheelSurfaceSelector = [",
  '].join(", ");'
);

describe("新标签页视口与滚轮交互基线", () => {
  it("快捷方式网格占满搜索框下方到视口底部", () => {
    expect(cssRule(".app-shell")).toContain("height: 100dvh");
    expect(cssRule(".home-stage")).toContain("height: 100dvh");
    expect(cssRule(".home-stage")).toContain("padding-bottom: 0");

    const gridRule = cssRule(".shortcut-grid");
    expect(gridRule).toContain("flex: 1 1 0");
    expect(gridRule).toContain("max-height: none");
    expect(gridRule).toContain("overflow-y: auto");
    expect(gridRule).not.toContain("--shortcut-grid-max-height");
    expect(appSource).not.toContain('"--shortcut-grid-max-height"');
    expect(appSource).not.toContain("<span>行数</span>");
  });

  it("普通纵向滚轮只切换分组且锁定期不会恢复默认滚动", () => {
    const preventDefaultIndex = groupWheelSource.indexOf("event.preventDefault()");
    const lockCheckIndex = groupWheelSource.indexOf("groupWheelLockedRef.current");

    expect(groupWheelSource).toContain("event.ctrlKey");
    expect(groupWheelSource).toContain("settingsOpen || event.ctrlKey");
    expect(groupWheelSource).toContain("Math.abs(event.deltaX) > Math.abs(event.deltaY)");
    expect(preventDefaultIndex).toBeGreaterThanOrEqual(0);
    expect(lockCheckIndex).toBeGreaterThanOrEqual(0);
    expect(preventDefaultIndex).toBeLessThan(lockCheckIndex);
    expect(groupWheelSource).toContain("switchGroupByOffset(event.deltaY > 0 ? 1 : -1)");
    expect(groupWheelSource).not.toContain("Math.abs(event.deltaY) < 10");
    expect(groupWheelSource).not.toMatch(/scrollHeight|scrollTop|canScrollDown|canScrollUp/);
    expect(appSource.match(/onWheel=\{handleGroupWheel\}/g)).toHaveLength(1);
  });

  it("真实滚动浮层不触发分组切换", () => {
    for (const selector of [
      ".settings-panel",
      ".modal-backdrop",
      ".enterprise-dialog-backdrop",
      ".search-engine-menu",
      ".shortcut-context-menu",
      ".wallpaper-context-menu"
    ]) {
      expect(nativeWheelSurfaceSource).toContain(selector);
    }

    expect(nativeWheelSurfaceSource).not.toContain(".shortcut-grid");
    expect(groupWheelSource).toContain("event.target.closest(nativeWheelSurfaceSelector)");
    expect(cssSource).toContain("bottom: calc(18px + env(safe-area-inset-bottom))");
    expect(cssSource).toContain("bottom: calc(82px + env(safe-area-inset-bottom))");
    expect(cssSource).toContain("padding-bottom: calc(88px + env(safe-area-inset-bottom))");
    expect(cssRule(".settings-content")).toContain("overflow-y: auto");
    expect(cssRule(".enterprise-dialog-body")).toContain("overflow-y: auto");
    expect(cssRule(".shortcut-form-body")).toContain("overflow-y: auto");
  });
});
