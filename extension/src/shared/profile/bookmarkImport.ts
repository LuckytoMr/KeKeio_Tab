import { getShortcutFallbackText } from "../shortcut/icons";
import { toSharedProfileV2 } from "./sharedProfile";
import type { Profile, ShortcutGroup } from "./types";

export const bookmarkImportLimits = {
  maxFiles: 10,
  maxFileBytes: 5 * 1024 * 1024,
  maxTotalBytes: 10 * 1024 * 1024,
  maxSourceCharacters: 5 * 1024 * 1024,
  maxTokensPerFile: 100_000,
  maxTokensPerBatch: 250_000,
  maxTagCharacters: 5 * 1024 * 1024,
  maxDlStackDepth: 64,
  maxCaptureCharacters: 4096,
  maxParsedCharactersPerFile: 8 * 1024 * 1024,
  maxParsedCharactersPerBatch: 16 * 1024 * 1024,
  maxFolderDepth: 32,
  maxBookmarksPerFile: 10_000,
  maxBookmarksPerBatch: 20_000,
  maxGroups: 256,
  maxShortcuts: 4096,
  maxSharedProfileBytes: 512 * 1024
} as const;

const groupTitleLimit = 160;
const shortcutTitleLimit = 240;
const shortcutUrlLimit = 2048;
const fallbackToolbarTitle = "书签栏";
const unclassifiedTitle = "未分类";

type FolderContext = {
  title: string;
  personalToolbar: boolean;
};

export type ParsedBrowserBookmark = {
  groupTitle: string;
  title: string;
  url: string;
};

export type ParsedBookmarkFile = {
  fileName: string;
  bookmarks: ParsedBrowserBookmark[];
  discoveredCount: number;
  parsedTokenCount?: number;
  parsedCharacterCount?: number;
};

export type BookmarkImportPlan = {
  profile: Profile;
  firstGroupId?: string;
  detectedCount: number;
  importedCount: number;
  affectedGroupCount: number;
  createdGroupCount: number;
  reusedGroupCount: number;
  skippedDuplicateCount: number;
  skippedInvalidCount: number;
  skippedCapacityCount: number;
  truncatedTitleCount: number;
};

const namedHtmlEntities: Record<string, string> = {
  amp: "&",
  apos: "'",
  gt: ">",
  lt: "<",
  nbsp: " ",
  quot: '"'
};

export const bookmarkImportWriteLockedCode = "BOOKMARK_IMPORT_WRITE_LOCKED";

export class BookmarkImportWriteLockedError extends Error {
  readonly code = bookmarkImportWriteLockedCode;

  constructor() {
    super("正在导入浏览器书签，请等待导入完成");
    this.name = "BookmarkImportWriteLockedError";
  }
}

export function assertBookmarkImportWriteAllowed(active: boolean, owned: boolean) {
  if (active && !owned) throw new BookmarkImportWriteLockedError();
}

function decodeHtmlEntities(value: string) {
  return value.replace(/&(#(?:x[\da-f]+|\d+)|[a-z]+);/gi, (entity, body: string) => {
    if (!body.startsWith("#")) return namedHtmlEntities[body.toLowerCase()] ?? entity;

    const hexadecimal = body[1]?.toLowerCase() === "x";
    const codePoint = Number.parseInt(body.slice(hexadecimal ? 2 : 1), hexadecimal ? 16 : 10);
    if (
      !Number.isInteger(codePoint) ||
      codePoint < 0 ||
      codePoint > 0x10ffff ||
      (codePoint >= 0xd800 && codePoint <= 0xdfff)
    ) {
      return "\ufffd";
    }

    try {
      return String.fromCodePoint(codePoint);
    } catch {
      return entity;
    }
  });
}

function normalizeWhitespace(value: string) {
  return value.replace(/[\u0000-\u001f\u007f\s]+/g, " ").trim();
}

function normalizeHtmlText(value: string) {
  return normalizeWhitespace(decodeHtmlEntities(value));
}

function parseAttributes(source: string, tag: "h3" | "a") {
  const attributes: Record<string, string> = {};
  let cursor = 0;

  while (cursor < source.length) {
    while (cursor < source.length && /\s/.test(source[cursor])) cursor += 1;
    if (source[cursor] === "/") {
      cursor += 1;
      continue;
    }

    const nameStart = cursor;
    while (cursor < source.length && !/[\s=/>]/.test(source[cursor])) cursor += 1;
    if (cursor === nameStart) {
      cursor += 1;
      continue;
    }
    const name = source.slice(nameStart, cursor).toLowerCase();
    while (cursor < source.length && /\s/.test(source[cursor])) cursor += 1;
    if (source[cursor] !== "=") continue;
    cursor += 1;
    while (cursor < source.length && /\s/.test(source[cursor])) cursor += 1;

    const quote = source[cursor] === '"' || source[cursor] === "'" ? source[cursor] : undefined;
    if (quote) cursor += 1;
    const valueStart = cursor;
    if (quote) {
      while (cursor < source.length && source[cursor] !== quote) cursor += 1;
    } else {
      while (cursor < source.length && !/[\s>]/.test(source[cursor])) cursor += 1;
    }
    const valueEnd = cursor;
    if (quote && source[cursor] === quote) cursor += 1;

    const allowed = (tag === "a" && name === "href") || (tag === "h3" && name === "personal_toolbar_folder");
    if (!allowed) continue;
    const rawLimit = name === "href" ? shortcutUrlLimit * 8 : 32;
    if (valueEnd - valueStart > rawLimit) {
      attributes[name] = name === "href" ? "x".repeat(shortcutUrlLimit + 1) : "";
      continue;
    }
    const decoded = decodeHtmlEntities(source.slice(valueStart, valueEnd)).trim();
    attributes[name] = name === "href" ? decoded.slice(0, shortcutUrlLimit + 1) : decoded.slice(0, 32);
  }

  return attributes;
}

function isTruthyAttribute(value: string | undefined) {
  return value === "1" || value?.toLowerCase() === "true";
}

function escapeGroupTitleSegment(value: string) {
  return value.replace(/\\/g, "\\\\").replace(/\//g, "\\/");
}

function resolveGroupTitle(path: FolderContext[], fileName: string) {
  if (!path.length) return unclassifiedTitle;

  const [first, ...rest] = path;
  const logicalPath = first.personalToolbar ? rest : path;
  const titles = logicalPath.map((folder) => folder.title).filter(Boolean);
  if (titles.length) {
    let resolved = "";
    for (const title of titles) {
      const segment = escapeGroupTitleSegment(title);
      const next = resolved ? `${resolved} / ${segment}` : segment;
      if (next.length > groupTitleLimit) {
        throw new Error(`${fileName} 包含超过 ${groupTitleLimit} 个字符的文件夹路径`);
      }
      resolved = next;
    }
    return resolved;
  }

  return first.personalToolbar ? fallbackToolbarTitle : unclassifiedTitle;
}

export function parseNetscapeBookmarkHtml(text: string, fileName = "书签文件"): ParsedBookmarkFile {
  if (text.length > bookmarkImportLimits.maxSourceCharacters) {
    throw new Error(`${fileName} 解码后的文本超过 ${bookmarkImportLimits.maxSourceCharacters} 个字符上限`);
  }
  if (!/NETSCAPE-Bookmark-file-1/i.test(text) || !/<\s*DL(?=[\s/>])/i.test(text)) {
    throw new Error(`${fileName} 不是浏览器导出的 Netscape 书签 HTML`);
  }

  const bookmarks: ParsedBrowserBookmark[] = [];
  const folderPath: FolderContext[] = [];
  const dlStack: Array<FolderContext | null> = [];
  let pendingFolder: FolderContext | null = null;
  let capture: {
    tag: "h3" | "a";
    attributes: Record<string, string>;
    text: string[];
    textLength: number;
  } | null = null;
  let parsedTokenCount = 0;
  let parsedCharacterCount = 0;

  const finalizeCapture = () => {
    if (!capture) return;

    const current = capture;
    capture = null;
    const title = normalizeHtmlText(current.text.join(""));

    if (current.tag === "h3") {
      pendingFolder = {
        title: title || "未命名文件夹",
        personalToolbar: isTruthyAttribute(current.attributes.personal_toolbar_folder)
      };
      return;
    }

    const rawUrl = current.attributes.href?.trim() ?? "";
    const bookmark = {
      groupTitle: resolveGroupTitle(folderPath, fileName),
      title: title || rawUrl,
      url: rawUrl
    };
    parsedCharacterCount += bookmark.groupTitle.length + bookmark.title.length + bookmark.url.length;
    if (parsedCharacterCount > bookmarkImportLimits.maxParsedCharactersPerFile) {
      throw new Error(`${fileName} 解析后的书签内容超过安全上限`);
    }
    bookmarks.push(bookmark);
    if (bookmarks.length > bookmarkImportLimits.maxBookmarksPerFile) {
      throw new Error(`${fileName} 的书签数量超过 ${bookmarkImportLimits.maxBookmarksPerFile} 条上限`);
    }
  };

  const countToken = () => {
    parsedTokenCount += 1;
    if (parsedTokenCount > bookmarkImportLimits.maxTokensPerFile) {
      throw new Error(`${fileName} 的 HTML 结构超过安全检查上限`);
    }
  };
  const appendCapturedText = (value: string) => {
    if (!capture || !value) return;
    capture.textLength += value.length;
    if (capture.textLength > bookmarkImportLimits.maxCaptureCharacters) {
      throw new Error(`${fileName} 包含超过 ${bookmarkImportLimits.maxCaptureCharacters} 个字符的标题`);
    }
    capture.text.push(value);
  };

  let cursor = 0;
  while (cursor < text.length) {
    const tagStart = text.indexOf("<", cursor);
    if (tagStart < 0) {
      countToken();
      appendCapturedText(text.slice(cursor));
      break;
    }
    if (tagStart > cursor) {
      countToken();
      appendCapturedText(text.slice(cursor, tagStart));
    }

    if (text.startsWith("<!--", tagStart)) {
      const commentEnd = text.indexOf("-->", tagStart + 4);
      if (commentEnd < 0) throw new Error(`${fileName} 包含未闭合的 HTML 注释`);
      if (commentEnd + 3 - tagStart > bookmarkImportLimits.maxTagCharacters) {
        throw new Error(`${fileName} 包含超过 ${bookmarkImportLimits.maxTagCharacters} 个字符的 HTML 标签`);
      }
      countToken();
      cursor = commentEnd + 3;
      continue;
    }

    let tagEnd = -1;
    let quote: '"' | "'" | undefined;
    for (let index = tagStart + 1; index < text.length; index += 1) {
      const character = text[index];
      if (quote) {
        if (character === quote) quote = undefined;
        continue;
      }
      if (character === '"' || character === "'") {
        quote = character;
        continue;
      }
      if (character === ">") {
        tagEnd = index;
        break;
      }
      if (index - tagStart + 1 > bookmarkImportLimits.maxTagCharacters) {
        throw new Error(`${fileName} 包含超过 ${bookmarkImportLimits.maxTagCharacters} 个字符的 HTML 标签`);
      }
    }
    if (tagEnd < 0) throw new Error(`${fileName} 包含未闭合的 HTML 标签`);
    if (tagEnd - tagStart + 1 > bookmarkImportLimits.maxTagCharacters) {
      throw new Error(`${fileName} 包含超过 ${bookmarkImportLimits.maxTagCharacters} 个字符的 HTML 标签`);
    }
    countToken();
    const tagSource = text.slice(tagStart + 1, tagEnd);
    cursor = tagEnd + 1;

    const token = /^\s*(\/?)\s*(dl|h3|a)(?=\s|\/|$)([\s\S]*)$/i.exec(tagSource);
    if (!token) continue;

    const closing = Boolean(token[1]);
    const tag = token[2].toLowerCase() as "dl" | "h3" | "a";

    if (tag === "h3" || tag === "a") {
      if (closing) {
        if (capture?.tag === tag) finalizeCapture();
        continue;
      }

      if (capture) finalizeCapture();
      pendingFolder = null;
      capture = {
        tag,
        attributes: parseAttributes(token[3], tag),
        text: [],
        textLength: 0
      };
      continue;
    }

    if (capture) finalizeCapture();
    if (closing) {
      pendingFolder = null;
      const folder = dlStack.pop();
      if (folder) folderPath.pop();
      continue;
    }

    const folder = pendingFolder;
    pendingFolder = null;
    if (dlStack.length >= bookmarkImportLimits.maxDlStackDepth) {
      throw new Error(`${fileName} 的 HTML 嵌套层级超过 ${bookmarkImportLimits.maxDlStackDepth} 层上限`);
    }
    dlStack.push(folder);
    if (!folder) continue;

    folderPath.push(folder);
    if (folderPath.length > bookmarkImportLimits.maxFolderDepth) {
      throw new Error(`${fileName} 的文件夹层级超过 ${bookmarkImportLimits.maxFolderDepth} 层上限`);
    }
  }

  if (capture) finalizeCapture();

  return {
    fileName,
    bookmarks,
    discoveredCount: bookmarks.length,
    parsedTokenCount,
    parsedCharacterCount
  };
}

function truncateNormalizedText(value: string, limit: number) {
  const normalized = normalizeWhitespace(value);
  if (normalized.length <= limit) return { value: normalized, truncated: false };

  const characters: string[] = [];
  let length = 1;
  for (const character of Array.from(normalized)) {
    if (length + character.length > limit) break;
    characters.push(character);
    length += character.length;
  }
  return { value: `${characters.join("")}…`, truncated: true };
}

export function normalizeImportedBookmarkUrl(input: string) {
  const raw = input.trim();
  if (!raw || raw.length > shortcutUrlLimit) return undefined;

  try {
    const url = new URL(raw);
    if (
      (url.protocol !== "https:" && url.protocol !== "http:") ||
      !url.hostname ||
      url.username ||
      url.password
    ) {
      return undefined;
    }

    const normalized = url.toString();
    return normalized.length <= shortcutUrlLimit ? normalized : undefined;
  } catch {
    return undefined;
  }
}

function maxActiveShortcutSortIndex(profile: Profile, groupId: string) {
  return profile.shortcuts
    .filter((shortcut) => shortcut.groupId === groupId && !shortcut.deletedAt)
    .reduce((maximum, shortcut) => Math.max(maximum, shortcut.sortIndex), -1);
}

export function buildBookmarkImportPlan(profile: Profile, files: ParsedBookmarkFile[]): BookmarkImportPlan {
  const detectedCount = files.reduce((total, file) => total + file.discoveredCount, 0);
  if (detectedCount > bookmarkImportLimits.maxBookmarksPerBatch) {
    throw new Error(`所选文件共包含 ${detectedCount} 条书签，超过 ${bookmarkImportLimits.maxBookmarksPerBatch} 条检查上限`);
  }
  const parsedTokenCount = files.reduce((total, file) => total + (file.parsedTokenCount ?? 0), 0);
  if (parsedTokenCount > bookmarkImportLimits.maxTokensPerBatch) {
    throw new Error("所选书签文件的 HTML 结构合计超过安全检查上限");
  }
  const parsedCharacterCount = files.reduce((total, file) => total + (file.parsedCharacterCount ?? file.bookmarks.reduce(
    (fileTotal, bookmark) => fileTotal + bookmark.groupTitle.length + bookmark.title.length + bookmark.url.length,
    0
  )), 0);
  if (parsedCharacterCount > bookmarkImportLimits.maxParsedCharactersPerBatch) {
    throw new Error("所选书签文件解析后的内容合计超过安全上限");
  }

  const timestamp = new Date().toISOString();
  const groups = [...profile.groups];
  const shortcuts = [...profile.shortcuts];
  const initialActiveGroupIds = new Set(profile.groups.filter((group) => !group.deletedAt).map((group) => group.id));
  const activeGroupByTitle = new Map(
    profile.groups
      .filter((group) => !group.deletedAt)
      .map((group) => [group.title, group] as const)
  );
  const seenUrlsByGroup = new Map<string, Set<string>>();
  const nextSortIndexByGroup = new Map<string, number>();
  const batchKeys = new Set<string>();
  const affectedGroupIds = new Set<string>();
  const reusedGroupIds = new Set<string>();
  let maxGroupSortIndex = groups.reduce((maximum, group) => Math.max(maximum, group.sortIndex), -1);
  let firstGroupId: string | undefined;
  let importedCount = 0;
  let createdGroupCount = 0;
  let skippedDuplicateCount = 0;
  let skippedInvalidCount = 0;
  let skippedCapacityCount = 0;
  let truncatedTitleCount = 0;

  for (const shortcut of profile.shortcuts) {
    if (shortcut.deletedAt) continue;
    const normalized = normalizeImportedBookmarkUrl(shortcut.url);
    if (!normalized) continue;
    const seen = seenUrlsByGroup.get(shortcut.groupId) ?? new Set<string>();
    seen.add(normalized);
    seenUrlsByGroup.set(shortcut.groupId, seen);
  }

  const ensureGroup = (title: string) => {
    const existing = activeGroupByTitle.get(title);
    if (existing) return existing;
    if (groups.length >= bookmarkImportLimits.maxGroups) return undefined;

    const group: ShortcutGroup = {
      id: `group:${crypto.randomUUID()}`,
      title,
      sortIndex: ++maxGroupSortIndex,
      createdAt: timestamp,
      updatedAt: timestamp
    };
    groups.push(group);
    activeGroupByTitle.set(title, group);
    seenUrlsByGroup.set(group.id, new Set());
    nextSortIndexByGroup.set(group.id, 0);
    createdGroupCount += 1;
    return group;
  };

  for (const file of files) {
    for (const bookmark of file.bookmarks) {
      const normalizedUrl = normalizeImportedBookmarkUrl(bookmark.url);
      if (!normalizedUrl) {
        skippedInvalidCount += 1;
        continue;
      }

      const groupTitle = normalizeWhitespace(bookmark.groupTitle || unclassifiedTitle) || unclassifiedTitle;
      if (groupTitle.length > groupTitleLimit) {
        throw new Error(`“${file.fileName}”包含超过 ${groupTitleLimit} 个字符的文件夹路径`);
      }
      const batchKey = `${groupTitle}\u0000${normalizedUrl}`;
      if (batchKeys.has(batchKey)) {
        skippedDuplicateCount += 1;
        continue;
      }
      batchKeys.add(batchKey);

      const existingGroup = activeGroupByTitle.get(groupTitle);
      const existingSeenUrls = existingGroup ? seenUrlsByGroup.get(existingGroup.id) : undefined;
      if (existingSeenUrls?.has(normalizedUrl)) {
        skippedDuplicateCount += 1;
        continue;
      }

      if (shortcuts.length >= bookmarkImportLimits.maxShortcuts) {
        skippedCapacityCount += 1;
        continue;
      }

      const group = ensureGroup(groupTitle);
      if (!group) {
        skippedCapacityCount += 1;
        continue;
      }

      const seenUrls = seenUrlsByGroup.get(group.id) ?? new Set<string>();

      const normalizedTitle = truncateNormalizedText(bookmark.title || normalizedUrl, shortcutTitleLimit);
      if (normalizedTitle.truncated) truncatedTitleCount += 1;
      const title = normalizedTitle.value || new URL(normalizedUrl).hostname;
      const nextSortIndex = nextSortIndexByGroup.get(group.id)
        ?? maxActiveShortcutSortIndex(profile, group.id) + 1;

      shortcuts.push({
        id: `shortcut:${crypto.randomUUID()}`,
        groupId: group.id,
        title,
        url: normalizedUrl,
        icon: { kind: "text", text: getShortcutFallbackText(title) },
        sortIndex: nextSortIndex,
        createdAt: timestamp,
        updatedAt: timestamp
      });
      nextSortIndexByGroup.set(group.id, nextSortIndex + 1);
      seenUrls.add(normalizedUrl);
      seenUrlsByGroup.set(group.id, seenUrls);
      affectedGroupIds.add(group.id);
      if (initialActiveGroupIds.has(group.id)) reusedGroupIds.add(group.id);
      firstGroupId ??= group.id;
      importedCount += 1;
    }
  }

  const nextProfile = importedCount
    ? { ...profile, groups, shortcuts, updatedAt: timestamp }
    : profile;

  if (importedCount) {
    let sharedProfile: ReturnType<typeof toSharedProfileV2>;
    try {
      sharedProfile = toSharedProfileV2(nextProfile);
    } catch {
      throw new Error("导入结果超过可同步配置限制，未写入任何书签");
    }
    const sharedProfileBytes = new TextEncoder().encode(JSON.stringify(sharedProfile)).byteLength;
    if (sharedProfileBytes > bookmarkImportLimits.maxSharedProfileBytes) {
      throw new Error(`导入结果超过 ${bookmarkImportLimits.maxSharedProfileBytes / 1024} KiB 的默认同步配置上限`);
    }
  }

  return {
    profile: nextProfile,
    firstGroupId,
    detectedCount,
    importedCount,
    affectedGroupCount: affectedGroupIds.size,
    createdGroupCount,
    reusedGroupCount: reusedGroupIds.size,
    skippedDuplicateCount,
    skippedInvalidCount,
    skippedCapacityCount,
    truncatedTitleCount
  };
}
