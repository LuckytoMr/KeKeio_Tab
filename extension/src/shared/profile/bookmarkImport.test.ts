import { describe, expect, it } from "vitest";
import { createDefaultProfile } from "./defaults";
import { toSharedProfileV2 } from "./sharedProfile";
import type { ParsedBookmarkFile } from "./bookmarkImport";
import {
  assertBookmarkImportWriteAllowed,
  bookmarkImportLimits,
  bookmarkImportWriteLockedCode,
  BookmarkImportWriteLockedError,
  buildBookmarkImportPlan,
  normalizeImportedBookmarkUrl,
  parseNetscapeBookmarkHtml
} from "./bookmarkImport";

const netscapeDocument = (body: string) => `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks</H1>
${body}`;

describe("浏览器书签导入", () => {
  it("去掉浏览器包装根并用完整相对路径映射一层分组", () => {
    const parsed = parseNetscapeBookmarkHtml(netscapeDocument(`<DL><p>
      <DT><H3 PERSONAL_TOOLBAR_FOLDER="true">收藏夹栏</H3>
      <DL><p>
        <DT><A HREF="https://direct.example/path?a=1&amp;b=2" ICON="data:image/png;base64,ignored">直属 &amp; 常用</A>
        <DT><H3>VPN</H3>
        <DL><p>
          <DT><H3>开源项目</H3>
          <DL><p><DT><A HREF="https://nested.example/#route"><B>嵌套</B> 🔐</A></DL><p>
        </DL><p>
        <DT><H3>VPN / 开源项目</H3>
        <DL><p><DT><A HREF="https://literal.example/">同名文本文件夹</A></DL><p>
      </DL><p>
      <DT><A HREF="https://root.example/">根级</A>
    </DL><p>`), "favorites.html");

    expect(parsed).toMatchObject({
      fileName: "favorites.html",
      discoveredCount: 4,
      bookmarks: [
        {
          groupTitle: "书签栏",
          title: "直属 & 常用",
          url: "https://direct.example/path?a=1&b=2"
        },
        {
          groupTitle: "VPN / 开源项目",
          title: "嵌套 🔐",
          url: "https://nested.example/#route"
        },
        {
          groupTitle: "VPN \\/ 开源项目",
          title: "同名文本文件夹",
          url: "https://literal.example/"
        },
        {
          groupTitle: "未分类",
          title: "根级",
          url: "https://root.example/"
        }
      ]
    });
  });

  it("拒绝普通网页、过深结构和超长标题的伪造导出文件", () => {
    expect(() => parseNetscapeBookmarkHtml("<html><a href='https://example.com'>普通网页</a></html>"))
      .toThrow("不是浏览器导出的 Netscape 书签 HTML");

    const openFolders = Array.from({ length: bookmarkImportLimits.maxFolderDepth + 1 }, (_, index) =>
      `<DT><H3>第 ${index + 1} 层</H3><DL><p>`
    ).join("");
    expect(() => parseNetscapeBookmarkHtml(netscapeDocument(`<DL><p>${openFolders}`), "过深.html"))
      .toThrow("文件夹层级超过");

    expect(() => parseNetscapeBookmarkHtml(
      netscapeDocument("<DL>".repeat(bookmarkImportLimits.maxDlStackDepth + 1)),
      "结构过深.html"
    )).toThrow("HTML 嵌套层级超过");

    expect(() => parseNetscapeBookmarkHtml(netscapeDocument(
      `<DL><p><DT><A HREF="https://example.com/">${"长".repeat(bookmarkImportLimits.maxCaptureCharacters + 1)}</A></DL>`
    ), "超长标题.html")).toThrow("包含超过 4096 个字符的标题");

    const unterminated = netscapeDocument(`<DL><p>${"<a".repeat(32 * 1024)}`);
    const startedAt = performance.now();
    expect(() => parseNetscapeBookmarkHtml(unterminated, "未闭合.html")).toThrow("包含未闭合的 HTML 标签");
    expect(performance.now() - startedAt).toBeLessThan(250);
  });

  it("只接受绝对且无凭据的 HTTP(S) URL", () => {
    expect(normalizeImportedBookmarkUrl("https://EXAMPLE.com:443/path?q=1#hash"))
      .toBe("https://example.com/path?q=1#hash");
    expect(normalizeImportedBookmarkUrl("http://192.168.50.1/"))
      .toBe("http://192.168.50.1/");

    for (const value of [
      "example.com",
      "/relative",
      "https://user:secret@example.com/",
      "javascript:alert(1)",
      "data:text/html,hello",
      "file:///tmp/a",
      "chrome://extensions",
      "edge://extensions",
      "about:blank",
      "blob:https://example.com/id"
    ]) {
      expect(normalizeImportedBookmarkUrl(value), value).toBeUndefined();
    }
  });

  it("兼容引号内大于号和大型 Firefox 图标，并拒绝伪装标签名与孤立 surrogate", () => {
    const largeIcon = `data:image/png;base64,${"a".repeat(128 * 1024)}`;
    const parsed = parseNetscapeBookmarkHtml(netscapeDocument(`<DL><p>
      <DT><A HREF="https://gt.example/?q=a>b" ICON="${largeIcon}">含大于号</A>
      <DT><a-evil HREF="https://custom.example/">伪装标签</a-evil>
      <DT><A HREF="https://surrogate.example/">前&#xD800;后</A>
    </DL><p>`), "quoted.html");

    expect(parsed.bookmarks).toEqual([
      { groupTitle: "未分类", title: "含大于号", url: "https://gt.example/?q=a>b" },
      { groupTitle: "未分类", title: "前�后", url: "https://surrogate.example/" }
    ]);
    const plan = buildBookmarkImportPlan(createDefaultProfile(), [parsed]);
    expect(plan.importedCount).toBe(2);
    expect(plan.profile.shortcuts.find((shortcut) => shortcut.url.startsWith("https://gt.example/"))?.title)
      .toBe("含大于号");
    expect(JSON.stringify(plan.profile)).not.toContain("\\ud800");
    expect(JSON.stringify(plan.profile)).not.toContain("data:image");
  });

  it("多文件合并同名分组、组内去重，并保留跨分组重复 URL", () => {
    const profile = createDefaultProfile();
    const timestamp = new Date().toISOString();
    profile.groups.push({
      id: "group:vpn-open-source",
      title: "VPN / 开源项目",
      sortIndex: 1,
      createdAt: timestamp,
      updatedAt: timestamp
    });
    profile.shortcuts.push({
      id: "shortcut:existing",
      groupId: "group:vpn-open-source",
      title: "已有",
      url: "https://example.com/",
      icon: { kind: "text", text: "已" },
      sortIndex: 0,
      createdAt: timestamp,
      updatedAt: timestamp
    });

    const files: ParsedBookmarkFile[] = [
      {
        fileName: "chrome.html",
        discoveredCount: 2,
        bookmarks: [
          { groupTitle: "VPN / 开源项目", title: "重复已有", url: "https://EXAMPLE.com" },
          { groupTitle: "VPN / 开源项目", title: "新增", url: "https://new.example/path#keep" }
        ]
      },
      {
        fileName: "edge.html",
        discoveredCount: 2,
        bookmarks: [
          { groupTitle: "VPN / 开源项目", title: "批内重复", url: "https://new.example/path#keep" },
          { groupTitle: "个人", title: "跨分组保留", url: "https://example.com/" }
        ]
      }
    ];

    const plan = buildBookmarkImportPlan(profile, files);

    expect(plan).toMatchObject({
      detectedCount: 4,
      importedCount: 2,
      affectedGroupCount: 2,
      createdGroupCount: 1,
      reusedGroupCount: 1,
      skippedDuplicateCount: 2,
      skippedInvalidCount: 0,
      skippedCapacityCount: 0
    });
    expect(plan.profile.shortcuts.filter((shortcut) => shortcut.url === "https://example.com/" && !shortcut.deletedAt))
      .toHaveLength(2);
    expect(plan.profile.shortcuts.find((shortcut) => shortcut.url === "https://new.example/path#keep"))
      .toMatchObject({ groupId: "group:vpn-open-source", sortIndex: 1 });
    expect(() => toSharedProfileV2(plan.profile)).not.toThrow();
  });

  it("HTML 实体只解码一次，避免双编码斜杠与多级路径碰撞", () => {
    const parsed = parseNetscapeBookmarkHtml(netscapeDocument(`<DL><p>
      <DT><H3 PERSONAL_TOOLBAR_FOLDER="true">书签栏</H3><DL><p>
        <DT><H3>A</H3><DL><p>
          <DT><H3>B</H3><DL><p><DT><A HREF="https://same.example/">多级</A></DL><p>
        </DL><p>
        <DT><H3>A &amp;#47; B</H3><DL><p>
          <DT><A HREF="https://same.example/">字面实体 &amp;lt;</A>
        </DL><p>
      </DL><p>
    </DL><p>`), "entities.html");

    expect(parsed.bookmarks.map((bookmark) => bookmark.groupTitle)).toEqual(["A / B", "A &#47; B"]);
    const plan = buildBookmarkImportPlan(createDefaultProfile(), [parsed]);
    expect(plan.importedCount).toBe(2);
    expect(plan.skippedDuplicateCount).toBe(0);
    expect(plan.profile.shortcuts.find((shortcut) => shortcut.groupId === plan.profile.groups.find(
      (group) => group.title === "A &#47; B"
    )?.id)?.title).toBe("字面实体 &lt;");
  });

  it("跳过危险链接、忽略内嵌图标，并让 HTTP 书签保持可同步", () => {
    const parsed = parseNetscapeBookmarkHtml(netscapeDocument(`<DL><p>
      <DT><A HREF="jav&#x61;script:alert(1)" ICON="data:image/png;base64,evil">脚本</A>
      <DT><A HREF="https://user:secret@example.com/">凭据</A>
      <DT><A HREF="chrome://extensions">内部页</A>
      <DT><A ICON="fake href='https://attribute.example/'">无 HREF</A>
      <DT><A HREF="http://192.168.50.1/" ICON="data:image/png;base64,ignored">路由器</A>
    </DL><p>`));
    const plan = buildBookmarkImportPlan(createDefaultProfile(), [parsed]);

    expect(plan.importedCount).toBe(1);
    expect(plan.skippedInvalidCount).toBe(4);
    const imported = plan.profile.shortcuts.find((shortcut) => shortcut.url === "http://192.168.50.1/");
    expect(imported?.icon).toEqual({ kind: "text", text: "路由" });
    expect(JSON.stringify(imported)).not.toContain("data:image");
    expect(() => toSharedProfileV2(plan.profile)).not.toThrow();
  });

  it("不复活同名墓碑分组，并按 Unicode code point 安全缩短过长链接标题", () => {
    const profile = createDefaultProfile();
    const timestamp = new Date().toISOString();
    profile.groups.push({
      id: "group:deleted",
      title: "未分类",
      sortIndex: 1,
      createdAt: timestamp,
      updatedAt: timestamp,
      deletedAt: timestamp
    });
    const longTitle = "😀".repeat(250);
    const plan = buildBookmarkImportPlan(profile, [{
      fileName: "long.html",
      discoveredCount: 2,
      bookmarks: [
        { groupTitle: "未分类", title: "新链接", url: "https://root.example/" },
        { groupTitle: "长标题", title: longTitle, url: "https://long.example/" }
      ]
    }]);

    const activeUnclassified = plan.profile.groups.filter((group) => group.title === "未分类" && !group.deletedAt);
    const importedLong = plan.profile.shortcuts.find((shortcut) => shortcut.url === "https://long.example/");
    expect(activeUnclassified).toHaveLength(1);
    expect(activeUnclassified[0].id).not.toBe("group:deleted");
    expect(importedLong?.title.length).toBeLessThanOrEqual(240);
    expect(importedLong?.title.endsWith("…")).toBe(true);
    expect(plan.truncatedTitleCount).toBe(1);
    expect(() => toSharedProfileV2(plan.profile)).not.toThrow();
  });

  it("拒绝会被静默截短并产生碰撞的超长文件夹路径", () => {
    const longGroup = "分".repeat(161);
    expect(() => buildBookmarkImportPlan(createDefaultProfile(), [{
      fileName: "long-group.html",
      discoveredCount: 1,
      bookmarks: [{ groupTitle: longGroup, title: "链接", url: "https://long-group.example/" }]
    }])).toThrow("超过 160 个字符的文件夹路径");
  });

  it("把墓碑也计入分组容量且不创建空分组", () => {
    const profile = createDefaultProfile();
    const timestamp = new Date().toISOString();
    for (let index = 1; index < bookmarkImportLimits.maxGroups; index += 1) {
      profile.groups.push({
        id: `group:deleted:${index}`,
        title: `已删除 ${index}`,
        sortIndex: index,
        createdAt: timestamp,
        updatedAt: timestamp,
        deletedAt: timestamp
      });
    }

    const plan = buildBookmarkImportPlan(profile, [{
      fileName: "full.html",
      discoveredCount: 1,
      bookmarks: [{ groupTitle: "新分组", title: "无法加入", url: "https://capacity.example/" }]
    }]);

    expect(plan.importedCount).toBe(0);
    expect(plan.skippedCapacityCount).toBe(1);
    expect(plan.profile).toBe(profile);
    expect(plan.profile.groups.some((group) => group.title === "新分组")).toBe(false);
  });

  it("达到快捷方式容量时仍优先把已有链接计为重复", () => {
    const profile = createDefaultProfile();
    const timestamp = new Date().toISOString();
    const groupId = profile.groups[0].id;
    for (let index = profile.shortcuts.length; index < bookmarkImportLimits.maxShortcuts; index += 1) {
      profile.shortcuts.push({
        id: `shortcut:capacity:${index}`,
        groupId,
        title: `容量 ${index}`,
        url: `https://capacity-${index}.example/`,
        icon: { kind: "text", text: "容" },
        sortIndex: index,
        createdAt: timestamp,
        updatedAt: timestamp
      });
    }

    const duplicateUrl = profile.shortcuts[0].url;
    const plan = buildBookmarkImportPlan(profile, [{
      fileName: "capacity.html",
      discoveredCount: 2,
      bookmarks: [
        { groupTitle: profile.groups[0].title, title: "已有", url: duplicateUrl },
        { groupTitle: profile.groups[0].title, title: "超出容量", url: "https://new-at-capacity.example/" }
      ]
    }]);

    expect(plan.importedCount).toBe(0);
    expect(plan.skippedDuplicateCount).toBe(1);
    expect(plan.skippedCapacityCount).toBe(1);
  });

  it("按 UTF-8 字节拒绝超过默认同步配置上限的整批结果", () => {
    const bookmarks = Array.from({ length: 900 }, (_, index) => ({
      groupTitle: "批量",
      title: `书签 ${index}`,
      url: `https://large-${index}.example/${"a".repeat(900)}`
    }));

    expect(() => buildBookmarkImportPlan(createDefaultProfile(), [{
      fileName: "large.html",
      discoveredCount: bookmarks.length,
      bookmarks
    }])).toThrow("超过 512 KiB 的默认同步配置上限");
  });

  it("导入写门只允许当前书签导入拥有者保存", () => {
    expect(() => assertBookmarkImportWriteAllowed(false, false)).not.toThrow();
    expect(() => assertBookmarkImportWriteAllowed(true, true)).not.toThrow();

    try {
      assertBookmarkImportWriteAllowed(true, false);
      throw new Error("预期写门拒绝普通保存");
    } catch (error) {
      expect(error).toBeInstanceOf(BookmarkImportWriteLockedError);
      expect((error as BookmarkImportWriteLockedError).code).toBe(bookmarkImportWriteLockedCode);
    }

    expect(() => assertBookmarkImportWriteAllowed(false, false)).not.toThrow();
  });
});
