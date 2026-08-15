import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const appSource = readFileSync(new URL("../newtab/App.tsx", import.meta.url), "utf8");
const optionsSource = readFileSync(new URL("../options/main.tsx", import.meta.url), "utf8");
const dialogSource = readFileSync(new URL("./Dialog.tsx", import.meta.url), "utf8");

function sourceBetween(source: string, startMarker: string, endMarker: string) {
  const start = source.indexOf(startMarker);
  const end = source.indexOf(endMarker, start + startMarker.length);
  if (start < 0 || end < 0) throw new Error(`找不到源码片段：${startMarker} -> ${endMarker}`);
  return source.slice(start, end);
}

const persistSource = sourceBetween(appSource, "async function persist(", "async function exportConfig(");
const loadLatestProfileSource = sourceBetween(
  appSource,
  "async function loadLatestPersistedProfile(",
  "async function persist("
);
const bookmarkImportSource = sourceBetween(
  appSource,
  "async function importBrowserBookmarks(",
  "async function resetLocalConfig("
);
const rotateWallpaperSource = sourceBetween(appSource, "async function rotateWallpaper(", "async function updateProfile(");
const settingsKeyboardSource = sourceBetween(appSource, "if (!settingsOpen) return;", "const clearPendingPress = () => {");
const settingsPanelSource = sourceBetween(appSource, "{settingsOpen ? (", "{shortcutForm ? (");
const bookmarkSettingsSource = sourceBetween(
  settingsPanelSource,
  '<section className="backup-task bookmark-import-task"',
  '<section className="backup-task config-backup-task"'
);

describe("设置交互静态接线基线", () => {
  it("扩展设置不再使用浏览器原生确认框", () => {
    expect(`${appSource}\n${optionsSource}`).not.toContain("window.confirm");
  });

  it("首次连接页面接入显式双向选择和安全取消编排器", () => {
    expect(appSource).toContain("resolveFirstConnectionStrategy");
    expect(appSource).toContain("dialogs.decide");
    expect(appSource).toContain('value: "use-local"');
    expect(appSource).toContain('value: "use-remote"');
    expect(appSource).toContain('type: "auth:logout"');
  });

  it("刷新后端资源具备手动忙碌入口与同页反馈", () => {
    expect(appSource).toContain('refreshBackendResources(undefined, "manual")');
    expect(appSource).toContain("resourceRefreshing");
    expect(appSource).toContain("<ActionStatus feedback={resourceFeedback}");
  });

  it("浏览器书签导入支持多文件、追加确认和导入后定位首个分组", () => {
    expect(bookmarkSettingsSource).toContain("导入浏览器书签");
    expect(bookmarkSettingsSource).toContain('accept=".html,.htm,text/html"');
    expect(bookmarkSettingsSource).toContain("multiple");
    expect(bookmarkSettingsSource).toContain("importBrowserBookmarks(event.currentTarget.files)");
    expect(bookmarkSettingsSource).toContain("直属链接归入“书签栏”");
    expect(bookmarkSettingsSource).toContain("根级链接归入“未分类”");
    expect(bookmarkImportSource).toContain("buildBookmarkImportPlan");
    expect(bookmarkImportSource).toContain("parsedBookmarkCount > bookmarkImportLimits.maxBookmarksPerBatch");
    expect(bookmarkImportSource).toContain("parsedTokenCount > bookmarkImportLimits.maxTokensPerBatch");
    expect(bookmarkImportSource).toContain("parsedCharacterCount > bookmarkImportLimits.maxParsedCharactersPerBatch");
    expect(bookmarkImportSource).toContain("await dialogs.confirm");
    expect(bookmarkImportSource).toContain("setActiveGroupId(plan.firstGroupId");
    expect(bookmarkImportSource).toContain("setSettingsOpen(false)");
  });

  it("浏览器书签导入占用写门并在失败后回载持久化基线", () => {
    const queuedSaveWait = loadLatestProfileSource.indexOf("await profileSaveTailRef.current.catch");
    const persistedLoad = loadLatestProfileSource.indexOf("const latest = await loadProfile()");
    const guard = persistSource.indexOf("assertBookmarkImportWriteAllowed");
    const optimisticWrite = persistSource.indexOf("profileRef.current = stored");
    expect(queuedSaveWait).toBeGreaterThanOrEqual(0);
    expect(persistedLoad).toBeGreaterThanOrEqual(0);
    expect(queuedSaveWait).toBeLessThan(persistedLoad);
    expect(guard).toBeGreaterThanOrEqual(0);
    expect(optimisticWrite).toBeGreaterThanOrEqual(0);
    expect(guard).toBeLessThan(optimisticWrite);
    expect(bookmarkImportSource).toContain("syncBusyRef.current || githubBusyRef.current || wallpaperActionRef.current");
    expect(bookmarkImportSource).toContain("const baselineProfile = await loadLatestPersistedProfile()");
    expect(bookmarkImportSource).toContain("const confirmedProfile = await loadLatestPersistedProfile()");
    expect(bookmarkImportSource).toContain("canonicalJson(confirmedProfile) !== canonicalJson(baselineProfile)");
    expect(bookmarkImportSource).toContain('operation: "bookmark-import"');
    expect(bookmarkImportSource).toContain("已重新载入当前本机配置");
    expect(rotateWallpaperSource).toContain('backupActionRef.current === "bookmarks"');
    expect(settingsPanelSource).toContain('className="settings-content-lock"');
    expect(settingsPanelSource).toContain('disabled={backupAction === "bookmarks"}');
    expect(settingsPanelSource).toMatch(
      /aria-label="关闭设置"[\s\S]{0,120}disabled=\{backupAction === "bookmarks"\}/
    );
    expect(settingsKeyboardSource).toContain('backupActionRef.current === "bookmarks"');
  });

  it("自定义弹窗具备模态语义、说明关联、背景隔离与完整焦点集合", () => {
    expect(dialogSource).toContain('aria-modal="true"');
    expect(dialogSource).toContain("aria-describedby={descriptionId}");
    expect(dialogSource).toContain('sibling.setAttribute("inert", "")');
    expect(dialogSource).toContain("select:not(:disabled)");
    expect(dialogSource).toContain("textarea:not(:disabled)");
    expect(dialogSource).toContain("previousFocusAvailable");
    expect(dialogSource).toContain("previousFocus?.focus({ preventScroll: true })");
    expect(dialogSource).toContain("fallback?.focus({ preventScroll: true })");
  });
});
