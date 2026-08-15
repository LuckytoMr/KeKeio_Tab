import {
  Edit3,
  Github,
  Plus,
  Search,
  Settings,
  Shuffle,
  Trash2,
  Upload,
  X
} from "lucide-preact";
import type { JSX } from "preact";
import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import { ActionStatus, BusyLabel, inferFeedbackTone, type ActionFeedback } from "../components/Feedback";
import { useDialogs } from "../components/Dialog";
import { buildProfileBackupFilename, exportProfileBackup, parseProfileBackup } from "../shared/profile/backup";
import {
  assertBookmarkImportWriteAllowed,
  bookmarkImportLimits,
  buildBookmarkImportPlan,
  parseNetscapeBookmarkHtml
} from "../shared/profile/bookmarkImport";
import { createDefaultProfile } from "../shared/profile/defaults";
import { sharedProfileToLocalProfile, type SharedProfileV2 } from "../shared/profile/sharedProfile";
import {
  getShortcutDensityMetrics,
  getShortcutGridColumnGap,
  getShortcutGridColumnCount,
  getShortcutGridJustification,
  getShortcutIconShapeRadius,
  getShortcutIconSizeMetrics,
  getShortcutRowHeight,
  shortcutGridLayout,
  shortcutIconShapeOptions,
  shortcutIconSizeOptions
} from "../shared/profile/theme";
import {
  addWallpaperToSelection,
  addShortcutGroup,
  deleteShortcut,
  deleteShortcutGroup,
  getWallpaperKey,
  moveShortcut,
  renameShortcutGroup,
  removeWallpaperFromSelection,
  setProfileWallpaper,
  swapShortcutGroupOrder,
  upsertShortcut
} from "../shared/profile/mutations";
import type { Profile, SearchEngine, Shortcut, ShortcutInput } from "../shared/profile/types";
import { buildSearchUrl, getSearchEngineIcon } from "../shared/search/engines";
import {
  buildShortcutIcon,
  getShortcutFallbackText,
  getShortcutIconImageUrl,
  type ShortcutIconMode
} from "../shared/shortcut/icons";
import { clearProfile, loadProfile, saveProfile, subscribeProfileInvalidation } from "../shared/storage/chromeStorage";
import {
  clearGitHubSyncAuth,
  loadGitHubSyncAuth,
  saveGitHubSyncAuth,
  type GitHubSyncAuth
} from "../shared/storage/githubSyncAuth";
import {
  attachBackendSession,
  normalizeBackendWallpaperCatalog
} from "../shared/sync/backendClient";
import { fixedBackendUrl } from "../shared/sync/backendEndpoint";
import { minimumPluginPasswordLength, validateBackendAuthForm, type BackendAuthFormError } from "../shared/sync/authForm";
import {
  GistConflictError,
  githubProfileFilename,
  githubTokenCreateUrl,
  loadProfileFromGitHubGist,
  saveProfileToGitHubGist
} from "../shared/sync/client";
import {
  mergeSharedProfiles,
  resolveMergeConflicts,
  type MergeConflictChoice
} from "../shared/sync/merge";
import { canonicalJson } from "../shared/sync/gistBackup";
import {
  canWriteSyncSession,
  isSameWritableSyncSession,
  type PublicWorkerSession
} from "../shared/sync/credentialVault";
import { ScopedRequestGate, type ScopedRequestToken } from "../shared/sync/requestGate";
import {
  requiresExplicitReadOnlyRemoteApproval,
  resolveFirstConnectionStrategy,
  type FirstConnectionKind
} from "../shared/sync/firstConnection";
import { canonicalBackendBaseUrl } from "../shared/sync/syncApi";
import { WritableSessionRequestGate } from "../shared/sync/writableSessionRequestGate";
import { sendWorkerMessage } from "../shared/sync/workerProtocol";
import { normalizeVerifiedRemoteStyles, type RemoteStylePackage } from "../shared/style/remoteStyles";
import {
  builtinWallpapers,
  canUsePackagedWallpaperVariant,
  getRemoteWallpaperKey,
  getWallpaperPreviewBackground,
  getWallpaperTitle,
  getWallpaperVariantUrl,
  getLocalAssetUrl,
  listLocalWallpapers,
  parseRemoteWallpaperKey,
  remoteWallpapers,
  saveLocalWallpaper,
  webWallpaperCategories,
  wallpaperCategories
} from "../shared/wallpaper/repository";
import type { RemoteWallpaper, WallpaperVariant } from "../shared/wallpaper/repository";
import {
  buildWallpaperRotationCandidates,
  getWallpaperRotationDelayMs,
  hasWallpaperRotationAlternative,
  normalizeWallpaperIntervalSeconds,
  pickNextWallpaper
} from "../shared/wallpaper/rotation";
import {
  fetchUhdpaperImageBlob,
  fetchUhdpaperImageDataUrl,
  loadUhdpaperWallpaperPage
} from "../shared/wallpaper/uhdpaperClient";
import { UHD_HOME_URL } from "../shared/wallpaper/uhdpaper";

type ShortcutForm = {
  id?: string;
  groupId: string;
  title: string;
  url: string;
  iconMode: ShortcutIconMode;
  iconText: string;
  iconUrl: string;
};

type LocalWallpaperView = {
  assetId: string;
  name: string;
  url: string;
};

type SyncConflictView = {
  conflictId: string;
  base: SharedProfileV2;
  localAtConflict: SharedProfileV2;
  remoteAtConflict: SharedProfileV2;
  remoteVersion: number;
};

type ShortcutMenuState = {
  shortcut: Shortcut;
  left: number;
  top: number;
};

type WallpaperMenuState = {
  left: number;
  top: number;
};

type ShortcutDragState = {
  shortcut: Shortcut;
  startX: number;
  startY: number;
  x: number;
  y: number;
  hasMoved: boolean;
  editMode: boolean;
  targetId?: string;
  targetGroupId: string;
};

type ShortcutPressState = {
  shortcut: Shortcut;
  startX: number;
  startY: number;
};

type BackupAction = "export" | "import" | "bookmarks" | "reset" | null;
type SyncAction = "sync" | "load" | "logout" | "resend-email" | "forgot-password" | "resolve-conflict" | null;
type GitHubAction = "backup" | "load" | "clear" | null;
type WallpaperAction = "rotate" | "upload" | null;

const emptyForm = (groupId: string): ShortcutForm => ({
  groupId,
  title: "",
  url: "",
  iconMode: "auto",
  iconText: "",
  iconUrl: ""
});

const shortcutIconModeOptions: ReadonlyArray<{ value: ShortcutIconMode; label: string }> = [
  { value: "auto", label: "自动获取" },
  { value: "text", label: "文字" },
  { value: "url", label: "图片链接" }
];

const nativeWheelSurfaceSelector = [
  ".settings-panel",
  ".modal-backdrop",
  ".enterprise-dialog-backdrop",
  ".search-engine-menu",
  ".shortcut-context-menu",
  ".wallpaper-context-menu"
].join(", ");

function getSearchUrl(profile: Profile, query: string) {
  const engine =
    profile.search.engines.find((item) => item.id === profile.search.selectedEngineId) ?? profile.search.engines[0];
  return buildSearchUrl(engine, query);
}

function expectedWorkerSession(session: PublicWorkerSession) {
  return {
    expectedAccountScope: session.accountScope,
    expectedSessionGeneration: session.sessionGeneration
  };
}

function summarizeProfileContent(profile: {
  groups: ReadonlyArray<{ deletedAt?: string }>;
  shortcuts: ReadonlyArray<{ deletedAt?: string }>;
  updatedAt?: string;
}) {
  return {
    groups: profile.groups.filter((group) => !group.deletedAt).length,
    shortcuts: profile.shortcuts.filter((shortcut) => !shortcut.deletedAt).length,
    updatedAt: profile.updatedAt
  };
}

function formatProfileUpdatedAt(value?: string) {
  if (!value) return "时间未知";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "时间未知" : date.toLocaleString();
}

function buildWebPreviewRequestScope(auth: PublicWorkerSession) {
  return JSON.stringify([
    canonicalBackendBaseUrl(auth.baseUrl),
    auth.accountScope,
    auth.sessionGeneration
  ]);
}

function getShortcutInitial(title: string) {
  return getShortcutFallbackText(title);
}

function getShortcutIconMode(shortcut: Shortcut): ShortcutIconMode {
  if (shortcut.icon.kind === "favicon") return "auto";
  if (shortcut.icon.kind === "url") return "url";
  if (shortcut.icon.kind === "local") return "auto";
  return "text";
}

function getShortcutIconText(shortcut: Shortcut) {
  if (shortcut.icon.kind === "text") return shortcut.icon.text;
  if (shortcut.icon.kind === "favicon" || shortcut.icon.kind === "url") return shortcut.icon.fallbackText;
  return getShortcutInitial(shortcut.title);
}

function getShortcutIconUrl(shortcut: Shortcut) {
  if (shortcut.icon.kind === "favicon" || shortcut.icon.kind === "url") return shortcut.icon.url;
  return "";
}

function preloadIconImage(url: string) {
  return new Promise<void>((resolve, reject) => {
    const image = new Image();
    image.decoding = "async";
    image.referrerPolicy = "no-referrer";
    image.onload = () => {
      const decode = image.decode?.();
      if (!decode) {
        resolve();
        return;
      }
      decode.then(resolve).catch(resolve);
    };
    image.onerror = () => reject(new Error("ICON_IMAGE_LOAD_FAILED"));
    image.src = url;
  });
}

function ShortcutIconView({ shortcut }: { shortcut: Shortcut }) {
  const [failed, setFailed] = useState(false);
  const [localUrl, setLocalUrl] = useState("");
  const [displayUrl, setDisplayUrl] = useState("");
  const [imageReady, setImageReady] = useState(false);
  const displayUrlRef = useRef("");
  const icon = shortcut.icon;
  const targetImageUrl = getShortcutIconImageUrl(icon, localUrl);
  const imageUrl = displayUrl || targetImageUrl;
  const fallbackText =
    icon.kind === "text"
      ? icon.text
      : icon.kind === "favicon" || icon.kind === "url"
        ? icon.fallbackText
        : getShortcutInitial(shortcut.title);

  useEffect(() => {
    displayUrlRef.current = "";
    setDisplayUrl("");
    setFailed(false);
    setImageReady(false);
  }, [shortcut.id]);

  useEffect(() => {
    if (icon.kind !== "local") {
      setLocalUrl("");
      return;
    }

    let revokedUrl = "";
    let mounted = true;
    void getLocalAssetUrl(icon.assetId).then((url) => {
      if (!mounted || !url) return;
      revokedUrl = url;
      setLocalUrl(url);
    });

    return () => {
      mounted = false;
      if (revokedUrl) URL.revokeObjectURL(revokedUrl);
    };
  }, [icon]);

  useEffect(() => {
    let cancelled = false;

    if (!targetImageUrl) {
      displayUrlRef.current = "";
      setDisplayUrl("");
      setFailed(false);
      setImageReady(false);
      return;
    }

    if (targetImageUrl === displayUrlRef.current) {
      setFailed(false);
      setImageReady(true);
      return;
    }

    setFailed(false);
    if (!displayUrlRef.current) setImageReady(false);

    void preloadIconImage(targetImageUrl)
      .then(() => {
        if (cancelled) return;
        displayUrlRef.current = targetImageUrl;
        setDisplayUrl(targetImageUrl);
        setImageReady(true);
      })
      .catch(() => {
        if (cancelled) return;
        displayUrlRef.current = "";
        setDisplayUrl("");
        setFailed(true);
        setImageReady(false);
      });

    return () => {
      cancelled = true;
    };
  }, [targetImageUrl]);

  if (imageUrl && !failed) {
    return (
      <span className={imageReady ? "shortcut-icon has-image image-loaded" : "shortcut-icon has-image"}>
        <span className="shortcut-icon-fallback" aria-hidden="true">
          {fallbackText}
        </span>
        <img
          src={imageUrl}
          alt=""
          draggable={false}
          decoding="async"
          referrerPolicy="no-referrer"
          onLoad={() => {
            if (imageUrl === displayUrlRef.current) setImageReady(true);
          }}
          onError={() => setFailed(true)}
        />
      </span>
    );
  }

  return <span className="shortcut-icon">{fallbackText}</span>;
}

const compactEngineLabels: Record<string, string> = {
  bing: "B",
  sogou: "搜",
  so360: "360",
  duckduckgo: "D",
  yandex: "Y",
  yahoo: "Y!",
  naver: "N",
  bilibili: "B",
  zhihu: "知",
  wechat: "微",
  wikipedia: "W",
  youtube: "▶",
  npm: "npm",
  mdn: "MDN",
  stackoverflow: "SO"
};

function SearchEngineLogo({ engine }: { engine: SearchEngine }) {
  const icon = getSearchEngineIcon(engine);

  if (icon.kind === "baidu") {
    return (
      <svg className="engine-logo engine-logo-svg engine-logo-baidu" viewBox="0 0 24 24" aria-hidden="true">
        <circle cx="6.4" cy="7.2" r="2.5" />
        <circle cx="11.8" cy="5.1" r="2.7" />
        <circle cx="17.5" cy="7.3" r="2.4" />
        <circle cx="8.4" cy="12.2" r="2.4" />
        <circle cx="15.7" cy="12.2" r="2.4" />
        <path d="M6.6 17.5c.4-3 2.7-4.6 5.4-4.6s5 1.6 5.4 4.6c.3 2-1 3.6-3 3.6H9.6c-2 0-3.3-1.6-3-3.6Z" />
      </svg>
    );
  }

  if (icon.kind === "google") {
    return (
      <svg className="engine-logo engine-logo-svg engine-logo-google" viewBox="0 0 24 24" aria-hidden="true">
        <path
          fill="#4285f4"
          d="M23.5 12.3c0-.8-.1-1.5-.2-2.2H12v4.3h6.5c-.3 1.4-1.1 2.7-2.4 3.5v2.9h3.9c2.2-2.1 3.5-5.1 3.5-8.5Z"
        />
        <path
          fill="#34a853"
          d="M12 24c3.2 0 5.9-1.1 7.9-3.1L16 18c-1.1.7-2.4 1.1-4 1.1-3.1 0-5.7-2.1-6.7-4.9h-4v3C3.3 21.2 7.3 24 12 24Z"
        />
        <path
          fill="#fbbc05"
          d="M5.3 14.2A7.3 7.3 0 0 1 5 12c0-.8.1-1.5.3-2.2v-3h-4A12 12 0 0 0 0 12c0 1.9.5 3.6 1.3 5.2l4-3Z"
        />
        <path
          fill="#ea4335"
          d="M12 4.8c1.7 0 3.3.6 4.5 1.8L20 3.1A11.8 11.8 0 0 0 12 0C7.3 0 3.3 2.8 1.3 6.8l4 3C6.3 6.9 8.9 4.8 12 4.8Z"
        />
      </svg>
    );
  }

  if (icon.kind === "github") {
    return (
      <span className="engine-logo engine-logo-github" aria-hidden="true">
        <Github size={17} />
      </span>
    );
  }

  return (
    <span className={`engine-logo engine-logo-${icon.kind}`} aria-hidden="true">
      {compactEngineLabels[icon.kind] ?? icon.label}
    </span>
  );
}

export function App() {
  const dialogs = useDialogs();
  const [profile, setProfile] = useState<Profile>(() => createDefaultProfile());
  const [loaded, setLoaded] = useState(false);
  const [activeGroupId, setActiveGroupId] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsTab, setSettingsTab] = useState<"general" | "groups" | "wallpaper" | "search" | "backup" | "sync">(
    "general"
  );
  const [shortcutForm, setShortcutForm] = useState<ShortcutForm | null>(null);
  const [query, setQuery] = useState("");
  const [formError, setFormError] = useState("");
  const [shortcutMenu, setShortcutMenu] = useState<ShortcutMenuState | null>(null);
  const [wallpaperMenu, setWallpaperMenu] = useState<WallpaperMenuState | null>(null);
  const [shortcutDrag, setShortcutDrag] = useState<ShortcutDragState | null>(null);
  const [shortcutEditMode, setShortcutEditMode] = useState(false);
  const [groupSwitcherVisible, setGroupSwitcherVisible] = useState(false);
  const [searchEngineMenuOpen, setSearchEngineMenuOpen] = useState(false);
  const [localWallpapers, setLocalWallpapers] = useState<LocalWallpaperView[]>([]);
  const [webVariantById, setWebVariantById] = useState<Record<string, string>>({});
  const [webWallpapers, setWebWallpapers] = useState<RemoteWallpaper[]>(() => remoteWallpapers);
  const [officialRemoteWallpapers, setOfficialRemoteWallpapers] = useState<RemoteWallpaper[]>([]);
  const [webNextPageUrl, setWebNextPageUrl] = useState<string | undefined>(UHD_HOME_URL);
  const [webLoading, setWebLoading] = useState(false);
  const [webError, setWebError] = useState("");
  const [wallpaperAction, setWallpaperAction] = useState<WallpaperAction>(null);
  const [wallpaperFeedback, setWallpaperFeedback] = useState<ActionFeedback>({ tone: "info", message: "" });
  const [backupStatus, setBackupStatus] = useState("");
  const [bookmarkImportFeedback, setBookmarkImportFeedback] = useState<ActionFeedback>({ tone: "info", message: "" });
  const [backupAction, setBackupAction] = useState<BackupAction>(null);
  const [backendAuth, setBackendAuth] = useState<PublicWorkerSession | null>(null);
  const [backendEmail, setBackendEmail] = useState("");
  const [backendPassword, setBackendPassword] = useState("");
  const [syncFormError, setSyncFormError] = useState<BackendAuthFormError | null>(null);
  const [syncAuthError, setSyncAuthError] = useState("");
  const [syncAuthAction, setSyncAuthAction] = useState<"login" | "register" | null>(null);
  const [githubAuth, setGithubAuth] = useState<GitHubSyncAuth | null>(null);
  const [githubToken, setGithubToken] = useState("");
  const [githubGistId, setGithubGistId] = useState("");
  const [githubStatus, setGithubStatus] = useState("");
  const [githubBusy, setGithubBusy] = useState(false);
  const [githubAction, setGithubAction] = useState<GitHubAction>(null);
  const [syncStatus, setSyncStatus] = useState("");
  const [syncConflict, setSyncConflict] = useState<SyncConflictView | null>(null);
  const [syncConflictChoices, setSyncConflictChoices] = useState<Record<string, MergeConflictChoice>>({});
  const [syncBusy, setSyncBusy] = useState(false);
  const [syncAction, setSyncAction] = useState<SyncAction>(null);
  const [webPreviewUrls, setWebPreviewUrls] = useState<Record<string, string>>({});
  const [webSavingKeys, setWebSavingKeys] = useState<Set<string>>(() => new Set());
  const [webBackendNextPage, setWebBackendNextPage] = useState(1);
  const [webBackendCursor, setWebBackendCursor] = useState<string | undefined>();
  const [releaseNotice, setReleaseNotice] = useState("");
  const [remoteStyles, setRemoteStyles] = useState<RemoteStylePackage[]>([]);
  const [remoteStyleStatus, setRemoteStyleStatus] = useState("");
  const [resourceRefreshing, setResourceRefreshing] = useState(false);
  const [resourceFeedback, setResourceFeedback] = useState<ActionFeedback>({ tone: "info", message: "" });
  const [localSaveFeedback, setLocalSaveFeedback] = useState<ActionFeedback>({ tone: "info", message: "" });
  const syncMergeResult = useMemo(() => {
    if (!syncConflict) return null;
    try {
      return mergeSharedProfiles(syncConflict.base, syncConflict.localAtConflict, syncConflict.remoteAtConflict);
    } catch {
      return null;
    }
  }, [syncConflict]);
  const webLoadedPagesRef = useRef(new Set<string>());
  const webLoadingRef = useRef(false);
  const webSavingKeysRef = useRef(new Set<string>());
  const webLastScrollLoadRef = useRef(0);
  const backendAuthRef = useRef<PublicWorkerSession | null>(null);
  const startupSyncRequestGateRef = useRef(new WritableSessionRequestGate());
  const releaseRequestGateRef = useRef(new ScopedRequestGate());
  const webPreviewRequestGateRef = useRef(new ScopedRequestGate());
  const webPreviewRequestTokenRef = useRef<ScopedRequestToken | null>(null);
  const syncBusyRef = useRef(false);
  const resourceRefreshBusyRef = useRef(false);
  const resourceRefreshManualRequestedRef = useRef(false);
  const resourceRefreshGenerationRef = useRef(0);
  const profileSaveTailRef = useRef<Promise<void>>(Promise.resolve());
  const profileSaveRunRef = useRef(0);
  const backupActionRef = useRef<BackupAction>(null);
  const githubBusyRef = useRef(false);
  const wallpaperActionRef = useRef<WallpaperAction>(null);
  const groupWheelLockedRef = useRef(false);
  const groupSwitcherTimerRef = useRef<number | null>(null);
  const searchBoxRef = useRef<HTMLFormElement>(null);
  const settingsPanelRef = useRef<HTMLElement>(null);
  const settingsCloseRef = useRef<HTMLButtonElement>(null);
  const profileRef = useRef(profile);
  const shortcutPressRef = useRef<ShortcutPressState | null>(null);
  const shortcutPressTimerRef = useRef<number | null>(null);
  const shortcutDragRef = useRef<ShortcutDragState | null>(null);
  const suppressShortcutClickRef = useRef(false);
  const prefetchedIconUrlsRef = useRef(new Set<string>());
  const rotationLastRunAtRef = useRef<number | null>(null);

  useEffect(() => {
    profileRef.current = profile;
  }, [profile]);

  useEffect(() => {
    backendAuthRef.current = backendAuth;
  }, [backendAuth]);

  useEffect(() => {
    if (canWriteSyncSession(backendAuth)) return;
    setSyncConflict(null);
    setSyncConflictChoices({});
  }, [backendAuth?.accountScope, backendAuth?.readOnly, backendAuth?.firstConnectionPending]);

  useEffect(() => {
    syncBusyRef.current = syncBusy;
  }, [syncBusy]);

  useEffect(() => {
    if (!loaded) return;

    const urls = profile.shortcuts
      .filter((shortcut) => !shortcut.deletedAt)
      .map((shortcut) => getShortcutIconImageUrl(shortcut.icon, ""))
      .filter((url): url is string => Boolean(url));

    for (const url of urls) {
      if (prefetchedIconUrlsRef.current.has(url)) continue;
      prefetchedIconUrlsRef.current.add(url);
      void preloadIconImage(url).catch(() => {
        prefetchedIconUrlsRef.current.delete(url);
      });
    }
  }, [loaded, profile.shortcuts]);

  useEffect(() => {
    void loadProfile().then((stored) => {
      setProfile(stored);
      setActiveGroupId(stored.groups[0]?.id ?? "");
      if (stored.sync.errorMessage) setSyncStatus(stored.sync.errorMessage);
      setLoaded(true);
    });
  }, []);

  useEffect(() => {
    if (!loaded) return;
    void refreshPublicBootstrap();
  }, [loaded]);

  useEffect(() => subscribeProfileInvalidation((message) => {
    if (message.profileId !== profileRef.current.profileId) return;
    void loadProfile().then((stored) => {
      setProfile(stored);
      setActiveGroupId((current) => stored.groups.some((group) => group.id === current && !group.deletedAt)
        ? current
        : stored.groups.find((group) => !group.deletedAt)?.id ?? "");
    });
  }), []);

  useEffect(() => {
    void sendWorkerMessage<PublicWorkerSession | undefined>({ type: "auth:session" }).then((auth) => {
      if (!auth) return;
      backendAuthRef.current = auth;
      setBackendAuth(auth);
      setBackendEmail(auth.email);
      if (auth.readOnly) {
        setSyncStatus("当前是迁移只读会话：可读取云端配置和重发验证邮件，验证邮箱后重新登录即可恢复同步写入。");
      }
      void refreshBackendResources(auth);
      if (!canWriteSyncSession(auth)) {
        startupSyncRequestGateRef.current.invalidate();
        return;
      }
      const requestToken = startupSyncRequestGateRef.current.begin(auth);
      const isCurrentRequest = () => startupSyncRequestGateRef.current.isCurrent(
        requestToken,
        auth,
        backendAuthRef.current
      );
      void sendWorkerMessage<{ status: string; conflictId?: string }>({ type: "sync:flush" }).then(async (outcome) => {
        if (!isCurrentRequest()) return;
        if (outcome.status === "server-empty-conflict") {
          if (!isCurrentRequest()) return;
          setSyncStatus("云端配置为空且本机基线已过期；已冻结自动同步，请人工确认后再覆盖云端。");
          return;
        }
        if (outcome.status !== "conflict" || !outcome.conflictId) return;
        const conflict = await sendWorkerMessage<SyncConflictView>({
          type: "sync:get-conflict",
          conflictId: outcome.conflictId
        });
        if (!isCurrentRequest()) return;
        setSyncConflict(conflict);
        if (!isCurrentRequest()) return;
        setSyncConflictChoices({});
        if (!isCurrentRequest()) return;
        setSyncStatus(`检测到待处理配置冲突（${outcome.conflictId}）。`);
      }).catch(() => undefined);
    }).catch(() => undefined);
  }, []);

  useEffect(() => {
    void loadGitHubSyncAuth().then((auth) => {
      if (!auth) return;
      setGithubAuth(auth);
      setGithubToken(auth.token);
      setGithubGistId(auth.gistId);
    });
  }, []);

  useEffect(() => {
    void refreshLocalWallpapers();
  }, []);

  useEffect(
    () => () => {
      if (groupSwitcherTimerRef.current !== null) {
        window.clearTimeout(groupSwitcherTimerRef.current);
      }
    },
    []
  );

  useEffect(() => {
    if (!shortcutMenu && !wallpaperMenu) return;

    const close = () => {
      setShortcutMenu(null);
      setWallpaperMenu(null);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") close();
    };

    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    window.addEventListener("scroll", close, true);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("blur", close);
      window.removeEventListener("scroll", close, true);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [shortcutMenu, wallpaperMenu]);

  useEffect(() => {
    if (!searchEngineMenuOpen) return;

    const closeOnPointerDown = (event: MouseEvent) => {
      if (!searchBoxRef.current?.contains(event.target as Node)) {
        setSearchEngineMenuOpen(false);
      }
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setSearchEngineMenuOpen(false);
    };

    window.addEventListener("pointerdown", closeOnPointerDown);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("pointerdown", closeOnPointerDown);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [searchEngineMenuOpen]);

  useEffect(() => {
    if (!settingsOpen) return;

    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const isolated: Array<{ element: HTMLElement; hadInert: boolean; ariaHidden: string | null }> = [];
    settingsCloseRef.current?.focus({ preventScroll: true });
    let branch: HTMLElement | null = settingsPanelRef.current;
    while (branch && branch !== document.body) {
      const parent = branch.parentElement;
      if (!parent) break;
      for (const sibling of Array.from(parent.children)) {
        if (sibling === branch || !(sibling instanceof HTMLElement)) continue;
        isolated.push({
          element: sibling,
          hadInert: sibling.hasAttribute("inert"),
          ariaHidden: sibling.getAttribute("aria-hidden")
        });
        sibling.setAttribute("inert", "");
        sibling.setAttribute("aria-hidden", "true");
      }
      branch = parent;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (document.querySelector(".enterprise-dialog")) return;
      if (event.key === "Escape") {
        event.preventDefault();
        if (backupActionRef.current === "bookmarks") return;
        setSettingsOpen(false);
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = Array.from(settingsPanelRef.current?.querySelectorAll<HTMLElement>(
        "button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex='-1'])"
      ) ?? []).filter((element) => element.offsetParent !== null || element === document.activeElement);
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (!settingsPanelRef.current?.contains(document.activeElement)) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
        return;
      }
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      for (const { element, hadInert, ariaHidden } of isolated) {
        if (!hadInert) element.removeAttribute("inert");
        if (ariaHidden === null) element.removeAttribute("aria-hidden");
        else element.setAttribute("aria-hidden", ariaHidden);
      }
      previousFocus?.focus();
    };
  }, [settingsOpen]);

  useEffect(() => {
    const clearPendingPress = () => {
      if (shortcutPressTimerRef.current !== null) {
        window.clearTimeout(shortcutPressTimerRef.current);
        shortcutPressTimerRef.current = null;
      }
      shortcutPressRef.current = null;
    };

    const handleMouseMove = (event: MouseEvent) => {
      const drag = shortcutDragRef.current;
      if (drag) {
        const target = document.elementFromPoint(event.clientX, event.clientY) as HTMLElement | null;
        const targetTile = target?.closest<HTMLElement>("[data-shortcut-id]");
        const targetId = targetTile?.dataset.shortcutId;
        const targetShortcut = targetId
          ? profileRef.current.shortcuts.find((shortcut) => shortcut.id === targetId && !shortcut.deletedAt)
          : undefined;
        const nextDrag = {
          ...drag,
          x: event.clientX,
          y: event.clientY,
          hasMoved: drag.hasMoved || Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) > 6,
          targetId: targetId && targetId !== drag.shortcut.id ? targetId : undefined,
          targetGroupId: targetShortcut?.groupId ?? drag.targetGroupId
        };

        event.preventDefault();
        shortcutDragRef.current = nextDrag;
        setShortcutDrag(nextDrag);
        return;
      }

      const press = shortcutPressRef.current;
      if (!press) return;

      const moved = Math.hypot(event.clientX - press.startX, event.clientY - press.startY);
      if (moved > 8) clearPendingPress();
    };

    const handleMouseUp = (event: MouseEvent) => {
      clearPendingPress();
      const drag = shortcutDragRef.current;
      if (!drag) return;

      const pointerX = event.clientX || drag.x;
      const pointerY = event.clientY || drag.y;
      const target = document.elementFromPoint(pointerX, pointerY) as HTMLElement | null;
      const targetTile = target?.closest<HTMLElement>("[data-shortcut-id]");
      const targetId = drag.targetId ?? targetTile?.dataset.shortcutId;
      const targetShortcut = targetId
        ? profileRef.current.shortcuts.find((shortcut) => shortcut.id === targetId && !shortcut.deletedAt)
        : undefined;
      const destinationGroupId = targetShortcut?.groupId ?? drag.targetGroupId;

      shortcutDragRef.current = null;
      setShortcutDrag(null);
      if (
        (targetId && targetId !== drag.shortcut.id) ||
        destinationGroupId !== drag.shortcut.groupId ||
        (drag.editMode && drag.hasMoved && !targetId)
      ) {
        const next = moveShortcut(
          profileRef.current,
          drag.shortcut.id,
          destinationGroupId,
          targetId && targetId !== drag.shortcut.id ? targetId : undefined
        );
        if (next !== profileRef.current) void persist(next).catch(() => undefined);
      }

      suppressShortcutClickRef.current = true;
      window.setTimeout(() => {
        suppressShortcutClickRef.current = false;
      }, 250);
    };

    window.addEventListener("mousemove", handleMouseMove, { passive: false });
    window.addEventListener("mouseup", handleMouseUp);
    return () => {
      window.removeEventListener("mousemove", handleMouseMove);
      window.removeEventListener("mouseup", handleMouseUp);
    };
  }, []);

  async function loadLatestPersistedProfile() {
    await profileSaveTailRef.current.catch(() => undefined);
    const latest = await loadProfile();
    profileRef.current = latest;
    setProfile(latest);
    setActiveGroupId((current) => latest.groups.some((group) => group.id === current && !group.deletedAt)
      ? current
      : latest.groups.find((group) => !group.deletedAt)?.id ?? "");
    return latest;
  }

  async function persist(next: Profile, options: {
    touch?: boolean;
    notifySync?: boolean;
    successMessage?: string;
    operation?: "bookmark-import";
  } = {}) {
    assertBookmarkImportWriteAllowed(
      backupActionRef.current === "bookmarks",
      options.operation === "bookmark-import"
    );
    const shouldTouch = options.touch !== false && next.updatedAt === profileRef.current.updatedAt;
    const stored = shouldTouch ? { ...next, updatedAt: new Date().toISOString() } : next;
    const runId = ++profileSaveRunRef.current;
    profileRef.current = stored;
    setProfile(stored);
    setLocalSaveFeedback({ tone: "pending", message: "正在保存到本机…" });

    const write = profileSaveTailRef.current
      .catch(() => undefined)
      .then(() => saveProfile(stored));
    profileSaveTailRef.current = write;

    try {
      await write;
      if (runId === profileSaveRunRef.current) {
        setLocalSaveFeedback({
          tone: "success",
          message: options.successMessage
            ?? (canWriteSyncSession(backendAuthRef.current) ? "已保存到本机，等待云端同步" : "已保存到本机")
        });
      }
      if (options.notifySync !== false && typeof chrome !== "undefined" && chrome.runtime?.sendMessage) {
        void chrome.runtime.sendMessage({ type: "sync:notify-change" }).catch(() => undefined);
      }
    } catch (error) {
      if (runId === profileSaveRunRef.current) {
        setLocalSaveFeedback({
          tone: "error",
          message: error instanceof Error ? `保存失败：${error.message}` : "保存失败，刚才的更改尚未写入"
        });
      }
      throw error;
    }
  }

  async function exportConfig() {
    if (backupActionRef.current) return;
    backupActionRef.current = "export";
    setBackupAction("export");
    setBackupStatus("正在生成配置文件…");
    try {
      const blob = new Blob([exportProfileBackup(profileRef.current)], {
        type: "application/json;charset=utf-8"
      });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = buildProfileBackupFilename(profileRef.current);
      anchor.click();
      URL.revokeObjectURL(url);
      setBackupStatus("已生成配置文件并发起下载。本地上传图片和缓存图标不会包含在配置文件中。");
    } catch (error) {
      setBackupStatus(error instanceof Error ? `导出失败：${error.message}` : "导出失败，请重试");
    } finally {
      backupActionRef.current = null;
      setBackupAction(null);
    }
  }

  async function importConfig(files: FileList | null) {
    const file = files?.[0];
    if (!file || backupActionRef.current) return;

    try {
      backupActionRef.current = "import";
      setBackupAction("import");
      setBackupStatus("正在检查配置文件…");
      const imported = parseProfileBackup(await file.text(), profileRef.current);
      setBackupAction(null);
      const groups = imported.groups.filter((group) => !group.deletedAt).length;
      const shortcuts = imported.shortcuts.filter((shortcut) => !shortcut.deletedAt).length;
      const accepted = await dialogs.confirm({
        dedupeKey: "profile-import",
        title: "导入并替换当前配置？",
        tone: "warning",
        confirmLabel: "导入并替换",
        description: (
          <div className="dialog-impact">
            <p>已验证 <strong>{file.name}</strong>，继续后会替换当前浏览器的可同步配置。</p>
            <dl className="dialog-summary">
              <div><dt>分组</dt><dd>{groups}</dd></div>
              <div><dt>快捷方式</dt><dd>{shortcuts}</dd></div>
            </dl>
            <p>本地上传图片、缓存图标与登录状态不会从文件中恢复。</p>
          </div>
        )
      });
      if (!accepted) {
        setBackupStatus("已取消导入，当前配置未更改。");
        return;
      }
      setBackupAction("import");
      setBackupStatus("正在导入并替换当前配置…");
      await persist(imported);
      setActiveGroupId(imported.groups[0]?.id ?? "");
      setBackupStatus("已导入配置，当前新标签页已更新。");
    } catch (error) {
      setBackupStatus(error instanceof Error ? error.message : "导入失败");
    } finally {
      backupActionRef.current = null;
      setBackupAction(null);
    }
  }

  async function importBrowserBookmarks(selectedFiles: FileList | null) {
    const files = Array.from(selectedFiles ?? []);
    if (!files.length || backupActionRef.current) return;

    if (syncBusyRef.current || githubBusyRef.current || wallpaperActionRef.current) {
      setBookmarkImportFeedback({
        tone: "warning",
        message: "当前仍有同步、GitHub 或壁纸操作正在进行，请等待完成后再导入书签。"
      });
      return;
    }

    if (files.length > bookmarkImportLimits.maxFiles) {
      setBookmarkImportFeedback({
        tone: "error",
        message: `一次最多选择 ${bookmarkImportLimits.maxFiles} 个书签 HTML 文件。`
      });
      return;
    }

    const oversizedFile = files.find((file) => file.size > bookmarkImportLimits.maxFileBytes);
    if (oversizedFile) {
      setBookmarkImportFeedback({
        tone: "error",
        message: `“${oversizedFile.name}”超过 5 MiB，未读取任何文件。`
      });
      return;
    }

    const totalBytes = files.reduce((total, file) => total + file.size, 0);
    if (totalBytes > bookmarkImportLimits.maxTotalBytes) {
      setBookmarkImportFeedback({
        tone: "error",
        message: "所选书签文件合计超过 10 MiB，未读取任何文件。"
      });
      return;
    }

    backupActionRef.current = "bookmarks";
    setBackupAction("bookmarks");
    setBookmarkImportFeedback({
      tone: "pending",
      message: `正在读取并检查 ${files.length} 个书签文件…`
    });

    try {
      const baselineProfile = await loadLatestPersistedProfile();
      const parsedFiles: ReturnType<typeof parseNetscapeBookmarkHtml>[] = [];
      let parsedBookmarkCount = 0;
      let parsedTokenCount = 0;
      let parsedCharacterCount = 0;
      for (const file of files) {
        const buffer = await file.arrayBuffer();
        let text: string;
        try {
          text = new TextDecoder("utf-8", { fatal: true }).decode(buffer);
        } catch {
          throw new Error(`“${file.name}”不是有效的 UTF-8 书签 HTML`);
        }
        const parsed = parseNetscapeBookmarkHtml(text, file.name);
        parsedBookmarkCount += parsed.discoveredCount;
        parsedTokenCount += parsed.parsedTokenCount ?? 0;
        parsedCharacterCount += parsed.parsedCharacterCount ?? 0;
        if (parsedBookmarkCount > bookmarkImportLimits.maxBookmarksPerBatch) {
          throw new Error(`所选文件的书签数量合计超过 ${bookmarkImportLimits.maxBookmarksPerBatch} 条上限`);
        }
        if (parsedTokenCount > bookmarkImportLimits.maxTokensPerBatch) {
          throw new Error("所选书签文件的 HTML 结构合计超过安全检查上限");
        }
        if (parsedCharacterCount > bookmarkImportLimits.maxParsedCharactersPerBatch) {
          throw new Error("所选书签文件解析后的内容合计超过安全上限");
        }
        parsedFiles.push(parsed);
      }

      const plan = buildBookmarkImportPlan(baselineProfile, parsedFiles);
      const skippedCount = plan.skippedDuplicateCount + plan.skippedInvalidCount + plan.skippedCapacityCount;

      if (!plan.importedCount) {
        setBookmarkImportFeedback({
          tone: "warning",
          message: `识别到 ${plan.detectedCount} 条书签，但没有可新增内容；已跳过 ${skippedCount} 条重复、无效或超出容量的记录。`
        });
        return;
      }

      setBookmarkImportFeedback({ tone: "pending", message: "书签已检查，等待确认…" });
      const accepted = await dialogs.confirm({
        dedupeKey: "bookmark-html-import",
        title: `追加 ${plan.importedCount} 个浏览器书签？`,
        tone: "neutral",
        confirmLabel: "追加书签",
        description: (
          <div className="dialog-impact">
            <p>已读取 <strong>{files.length} 个文件</strong>，只会向当前链接追加内容，不会替换已有配置。</p>
            <dl className="dialog-summary">
              <div><dt>识别书签</dt><dd>{plan.detectedCount}</dd></div>
              <div><dt>新增书签</dt><dd>{plan.importedCount}</dd></div>
              <div><dt>涉及分组</dt><dd>{plan.affectedGroupCount}</dd></div>
              <div><dt>跳过重复</dt><dd>{plan.skippedDuplicateCount}</dd></div>
            </dl>
            <ul>
              <li>浏览器自带的“书签栏”外层会被忽略，其中直属链接统一归入“书签栏”；外层之外的根级链接归入“未分类”。</li>
              <li>多级文件夹按完整路径区分，例如“VPN / 开源项目”。</li>
              {plan.skippedInvalidCount ? <li>另有 {plan.skippedInvalidCount} 条无效或不安全链接会被跳过。</li> : null}
              {plan.skippedCapacityCount ? <li>另有 {plan.skippedCapacityCount} 条因配置容量限制无法导入。</li> : null}
              {plan.truncatedTitleCount ? <li>有 {plan.truncatedTitleCount} 个过长标题会安全截短。</li> : null}
            </ul>
          </div>
        )
      });

      if (!accepted) {
        setBookmarkImportFeedback({ tone: "info", message: "已取消导入，当前链接未发生变化。" });
        return;
      }

      const confirmedProfile = await loadLatestPersistedProfile();
      if (canonicalJson(confirmedProfile) !== canonicalJson(baselineProfile)) {
        throw new Error("确认期间本地配置已更新，请重新选择书签文件后再试");
      }

      setBookmarkImportFeedback({ tone: "pending", message: "正在追加书签并保存到本机…" });
      await persist(plan.profile, {
        touch: false,
        operation: "bookmark-import",
        successMessage: canWriteSyncSession(backendAuthRef.current)
          ? "书签已保存到本机，等待云端同步"
          : "书签已保存到本机"
      });

      setActiveGroupId(plan.firstGroupId ?? activeGroupId);
      setBookmarkImportFeedback({
        tone: "success",
        message: `已导入 ${plan.importedCount} 个书签到 ${plan.affectedGroupCount} 个分组，跳过 ${skippedCount} 条。`
      });
      setSettingsOpen(false);
    } catch (error) {
      let reloadError: unknown;
      try {
        await loadLatestPersistedProfile();
      } catch (cause) {
        reloadError = cause;
      }
      const reason = error instanceof Error ? error.message : "无法读取书签文件";
      setBookmarkImportFeedback({
        tone: "error",
        message: reloadError
          ? `导入失败：${reason}；重新载入本机配置也失败：${reloadError instanceof Error ? reloadError.message : "未知错误"}。`
          : `导入失败：${reason}。已重新载入当前本机配置。`
      });
    } finally {
      backupActionRef.current = null;
      setBackupAction(null);
    }
  }

  async function resetLocalConfig() {
    if (backupActionRef.current) return;
    backupActionRef.current = "reset";
    const accepted = await dialogs.confirm({
      dedupeKey: "profile-reset",
      title: "重置当前浏览器配置？",
      tone: "danger",
      confirmLabel: "重置本机配置",
      description: (
        <div className="dialog-impact danger-impact">
          <p>这会清空当前快捷方式、分组、搜索、壁纸选择和外观设置。</p>
          <ul>
            <li>本机配置会立即恢复为默认值。</li>
            <li>如果已连接后端，重置结果可能进入同步队列并影响其他浏览器。</li>
          </ul>
        </div>
      )
    });
    if (!accepted) {
      backupActionRef.current = null;
      setBackupStatus("已取消重置，当前配置未更改。");
      return;
    }

    setBackupAction("reset");
    setBackupStatus("正在重置本机配置…");
    try {
      const next = await clearProfile();
      profileRef.current = next;
      setProfile(next);
      setActiveGroupId(next.groups.find((group) => !group.deletedAt)?.id ?? "");
      setBackupStatus("已重置本地配置。");
      setLocalSaveFeedback({ tone: "success", message: "已保存到本机" });
    } catch (error) {
      setBackupStatus(error instanceof Error ? `重置失败：${error.message}` : "重置失败，请重试");
    } finally {
      backupActionRef.current = null;
      setBackupAction(null);
    }
  }

  async function rememberBackendAuth(
    auth: PublicWorkerSession,
    serverConfirmed: boolean,
    selectedProfile: Profile = profileRef.current
  ) {
    startupSyncRequestGateRef.current.invalidate();
    releaseRequestGateRef.current.invalidate();
    webPreviewRequestGateRef.current.invalidate();
    webPreviewRequestTokenRef.current = null;
    setReleaseNotice("");
    setWebPreviewUrls({});
    setSyncConflict(null);
    setSyncConflictChoices({});
    backendAuthRef.current = auth;
    setBackendAuth(auth);
    setBackendEmail(auth.email);
    const syncedAt = new Date(auth.updatedAt).toISOString();
    await persist(attachBackendSession(selectedProfile, serverConfirmed, syncedAt), {
      touch: false,
      notifySync: false,
      successMessage: serverConfirmed ? "已保存到本机，云端配置已应用" : "已保存到本机，等待云端同步"
    });
  }

  async function connectBackend(mode: "login" | "register") {
    if (syncBusyRef.current) return;

    const validation = validateBackendAuthForm({ mode, email: backendEmail, password: backendPassword });
    if (validation) {
      setSyncFormError(validation);
      setSyncAuthError("");
      setSyncStatus("");
      return;
    }

    setSyncFormError(null);
    setSyncAuthError("");
    syncBusyRef.current = true;
    setSyncBusy(true);
    setSyncAuthAction(mode);
    setSyncStatus(mode === "register" ? "正在注册..." : "正在登录...");
    try {
      const email = backendEmail.trim();

      if (mode === "register") {
        await sendWorkerMessage({ type: "auth:register", email, password: backendPassword });
        setBackendPassword("");
        setSyncStatus("注册申请已提交，请先完成邮箱验证后再登录。");
        return;
      }

      const result = await sendWorkerMessage<{
        session: PublicWorkerSession;
        firstConnection: FirstConnectionKind;
        remote: { profile: SharedProfileV2 | null; version: number; updatedAt?: string };
      }>({ type: "auth:login", email, password: backendPassword });
      startupSyncRequestGateRef.current.invalidate();
      setSyncConflict(null);
      setSyncConflictChoices({});
      if (result.session.readOnly) {
        let selectedProfile = profileRef.current;
        let activeSession = result.session;
        if (result.remote.profile) {
          if (requiresExplicitReadOnlyRemoteApproval(result.firstConnection)) {
            const localSummary = summarizeProfileContent(profileRef.current);
            const remoteSummary = summarizeProfileContent(result.remote.profile);
            const accepted = await dialogs.confirm({
              dedupeKey: "read-only-first-connection",
              title: "使用云端只读配置替换本机？",
              tone: "warning",
              confirmLabel: "使用云端只读配置",
              cancelLabel: "取消连接",
              description: (
                <div className="dialog-impact">
                  <p>本机和云端都有配置，但当前迁移会话只能读取云端，不能把本机配置上传。</p>
                  <dl className="dialog-summary content-summary">
                    <div><dt>当前本机</dt><dd>{localSummary.groups} 个分组 · {localSummary.shortcuts} 个快捷方式</dd></div>
                    <div><dt>云端版本 {result.remote.version}</dt><dd>{remoteSummary.groups} 个分组 · {remoteSummary.shortcuts} 个快捷方式</dd></div>
                  </dl>
                  <p>云端更新时间：{formatProfileUpdatedAt(result.remote.updatedAt ?? remoteSummary.updatedAt)}</p>
                  <button className="command" type="button" onClick={() => void exportConfig()}>先导出当前本机配置</button>
                </div>
              )
            });
            if (!accepted) {
              startupSyncRequestGateRef.current.invalidate();
              await sendWorkerMessage({
                type: "auth:logout",
                ...expectedWorkerSession(result.session)
              }).catch(() => undefined);
              setBackendPassword("");
              setSyncStatus("已取消只读连接，本机和云端配置均未更改。");
              return;
            }
          }
          const completed = await sendWorkerMessage<{ session?: PublicWorkerSession }>({
            type: "sync:complete-first-connection",
            strategy: "use-remote",
            ...expectedWorkerSession(result.session)
          });
          activeSession = completed.session ?? activeSession;
          selectedProfile = await loadProfile();
          setActiveGroupId(selectedProfile.groups.find((group) => !group.deletedAt)?.id ?? "");
        }
        await rememberBackendAuth(activeSession, true, selectedProfile);
        await refreshBackendResources(activeSession);
        setBackendPassword("");
        setSyncStatus(result.remote.profile
          ? "已读取并采用云端迁移配置。当前会话只读；完成邮箱验证后请退出并重新登录以启用同步。"
          : "已建立迁移只读会话，但云端暂无配置。完成邮箱验证后请退出并重新登录以启用同步。"
        );
        return;
      }
      const localSummary = summarizeProfileContent(profileRef.current);
      const remoteSummary = result.remote.profile ? summarizeProfileContent(result.remote.profile) : null;
      const strategy = await resolveFirstConnectionStrategy(
        result.firstConnection,
        () => dialogs.decide<"use-local" | "use-remote">({
          dedupeKey: "first-connection",
          title: "这台浏览器要使用哪份配置？",
          tone: "warning",
          confirmLabel: "应用所选配置",
          cancelLabel: "取消连接",
          description: (
            <div className="dialog-impact">
              <p>本机和云端都有配置。请选择一份作为这次连接的起点；关闭或取消不会覆盖任何一方。</p>
              <dl className="dialog-summary content-summary">
                <div><dt>当前本机</dt><dd>{localSummary.groups} 个分组 · {localSummary.shortcuts} 个快捷方式</dd></div>
                <div><dt>云端版本 {result.remote.version}</dt><dd>{remoteSummary?.groups ?? 0} 个分组 · {remoteSummary?.shortcuts ?? 0} 个快捷方式</dd></div>
              </dl>
              <p>云端更新时间：{formatProfileUpdatedAt(result.remote.updatedAt ?? remoteSummary?.updatedAt)}</p>
              <button className="command" type="button" onClick={() => void exportConfig()}>先导出当前本机配置</button>
            </div>
          ),
          options: [
            {
              value: "use-local",
              label: "保留本机配置",
              description: "继续使用当前浏览器里的快捷方式、分组、搜索、壁纸选择和外观设置。",
              consequence: "本机配置会作为新版本进入云端同步队列。",
              icon: "device"
            },
            {
              value: "use-remote",
              label: "使用云端配置",
              description: "采用账号中已有的可同步配置替换当前浏览器配置。",
              consequence: "本地上传图片、缓存和登录状态仍只保留在当前浏览器。",
              icon: "cloud"
            }
          ]
        }),
        async () => {
          startupSyncRequestGateRef.current.invalidate();
          await sendWorkerMessage({
            type: "auth:logout",
            ...expectedWorkerSession(result.session)
          }).catch(() => undefined);
        }
      );
      if (!strategy) {
        setBackendPassword("");
        setSyncStatus("已取消连接，本机和云端配置均未更改。");
        return;
      }
      const completed = await sendWorkerMessage<{ session?: PublicWorkerSession }>({
        type: "sync:complete-first-connection",
        strategy,
        ...expectedWorkerSession(result.session)
      });
      const activeSession = completed.session ?? result.session;
      const selectedProfile = strategy === "use-remote" ? await loadProfile() : profileRef.current;
      await rememberBackendAuth(activeSession, strategy === "use-remote", selectedProfile);
      if (strategy === "use-remote") {
        setActiveGroupId(selectedProfile.groups.find((group) => !group.deletedAt)?.id ?? "");
      }
      await refreshBackendResources(activeSession);
      setBackendPassword("");
      setSyncStatus(strategy === "use-local" ? "已连接，当前本机配置已进入持久同步队列。" : "已连接并采用云端配置。");
    } catch (error) {
      const message = error instanceof Error ? error.message : "连接失败";
      setSyncAuthError(`${mode === "register" ? "注册失败" : "登录失败"}：${message}`);
      setSyncStatus("");
      await persist({
        ...profileRef.current,
        sync: {
          ...profileRef.current.sync,
          provider: "backend",
          status: "error",
          backendUrl: fixedBackendUrl,
          errorMessage: message
        }
      }, { notifySync: false, successMessage: "已保存本机同步状态" });
    } finally {
      syncBusyRef.current = false;
      setSyncBusy(false);
      setSyncAuthAction(null);
    }
  }

  async function requestAccountEmail(kind: "resend-verification" | "forgot-password") {
    if (syncBusyRef.current) return;

    syncBusyRef.current = true;
    setSyncBusy(true);
    setSyncAction(kind === "resend-verification" ? "resend-email" : "forgot-password");
    setSyncStatus(kind === "resend-verification" ? "正在重新发送验证邮件…" : "正在申请密码重置邮件…");
    try {
      const email = backendEmail.trim();
      if (!email) throw new Error("请先填写邮箱");

      await sendWorkerMessage({
        type: kind === "resend-verification" ? "auth:resend-verification" : "auth:forgot-password",
        email
      });
      setSyncStatus(kind === "resend-verification"
        ? "请求已受理。如果该邮箱有待验证账号，新的验证链接会发送到邮箱。"
        : "请求已受理。如果该邮箱存在账号，密码重置链接会发送到邮箱。"
      );
    } catch (error) {
      setSyncStatus(error instanceof Error ? error.message : "账号邮件发送失败，请稍后重试");
    } finally {
      syncBusyRef.current = false;
      setSyncBusy(false);
      setSyncAction(null);
    }
  }

  async function saveProfileToBackend() {
    const auth = backendAuthRef.current;
    if (!auth || !canWriteSyncSession(auth) || syncBusyRef.current) return;

    setSyncBusy(true);
    syncBusyRef.current = true;
    setSyncAction("sync");
    setSyncStatus("正在处理同步队列...");
    try {
      const outcome = await sendWorkerMessage<{ status: string; version?: number; conflictId?: string; code?: string }>({ type: "sync:flush" });
      const syncedAt = new Date().toISOString();
      if (outcome.status === "synced") {
        await persist({
          ...profileRef.current,
          sync: { ...profileRef.current.sync, status: "ready", lastSyncedAt: syncedAt, errorMessage: undefined }
        }, {
          touch: false,
          notifySync: false,
          successMessage: "已保存到本机并同步到云端"
        });
        setSyncStatus(`已同步服务器版本 ${outcome.version ?? "-"}：${new Date(syncedAt).toLocaleString()}`);
      } else if (outcome.status === "conflict") {
        if (outcome.conflictId) {
          setSyncConflict(await sendWorkerMessage<SyncConflictView>({ type: "sync:get-conflict", conflictId: outcome.conflictId }));
          setSyncConflictChoices({});
        }
        setSyncStatus(`检测到配置冲突（${outcome.conflictId ?? "待处理"}），已停止自动覆盖。`);
      } else if (outcome.status === "server-empty-conflict") {
        setSyncStatus("云端配置为空且本机基线已过期；已冻结自动同步，请人工确认后再覆盖云端。");
      } else if (outcome.status === "retrying") {
        setSyncStatus(`同步暂时失败，后台将自动重试：${outcome.code ?? "NETWORK_ERROR"}`);
      } else {
        setSyncStatus("当前没有到期的同步任务；后台会按计划继续处理。");
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "保存失败";
      setSyncStatus(message);
      await persist({
        ...profileRef.current,
        sync: {
          ...profileRef.current.sync,
          provider: "backend",
          status: "error",
          backendUrl: auth.baseUrl,
          lastSyncAttemptAt: new Date().toISOString(),
          errorMessage: message
        }
      }, { touch: false, notifySync: false, successMessage: "已保存本机同步状态" });
    } finally {
      setSyncBusy(false);
      syncBusyRef.current = false;
      setSyncAction(null);
    }
  }

  async function resolveSyncConflict() {
    if (!syncConflict || !syncMergeResult || syncBusyRef.current || !canWriteSyncSession(backendAuthRef.current)) return;
    syncBusyRef.current = true;
    setSyncBusy(true);
    setSyncAction("resolve-conflict");
    setSyncStatus("正在提交冲突解决结果...");
    try {
      const resolved = resolveMergeConflicts(
        syncConflict.base,
        syncConflict.localAtConflict,
        syncConflict.remoteAtConflict,
        syncConflictChoices
      );
      await sendWorkerMessage({
        type: "sync:resolve-conflict",
        conflictId: syncConflict.conflictId,
        profile: resolved
      });
      const next = await loadProfile();
      setProfile(next);
      setActiveGroupId(next.groups.find((group) => !group.deletedAt)?.id ?? "");
      setSyncConflict(null);
      setSyncConflictChoices({});
      setSyncStatus("已按逐项选择生成新 mutation；冲突期间的后续本地编辑已重新应用。");
    } catch (error) {
      setSyncStatus(error instanceof Error ? error.message : "冲突解决失败");
    } finally {
      syncBusyRef.current = false;
      setSyncBusy(false);
      setSyncAction(null);
    }
  }

  function exportSyncConflict() {
    if (!syncConflict) return;
    const blob = new Blob([`${JSON.stringify({
      format: "full-pro-sync-conflict",
      version: 1,
      exportedAt: new Date().toISOString(),
      base: syncConflict.base,
      local: syncConflict.localAtConflict,
      remote: syncConflict.remoteAtConflict
    }, null, 2)}\n`], { type: "application/json;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `kekeio-tab-conflict-${syncConflict.conflictId}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  async function loadProfileFromBackend() {
    const auth = backendAuthRef.current;
    if (!auth || syncBusyRef.current) return;
    const localSummary = summarizeProfileContent(profileRef.current);
    syncBusyRef.current = true;
    const accepted = await dialogs.confirm({
      dedupeKey: "backend-profile-load",
      title: "使用云端配置替换当前浏览器？",
      tone: "warning",
      confirmLabel: "加载云端并替换",
      description: (
        <div className="dialog-impact">
          <p>将替换当前浏览器的快捷方式、分组、搜索、壁纸选择和外观设置。</p>
          <p>当前本机：{localSummary.groups} 个分组 · {localSummary.shortcuts} 个快捷方式 · {formatProfileUpdatedAt(localSummary.updatedAt)}</p>
          <ul>
            <li>本地上传图片、缓存与登录状态不会从云端恢复。</li>
            <li>加载失败时，当前本机配置不会更改。</li>
          </ul>
          <button className="command" type="button" onClick={() => void exportConfig()}>先导出当前本机配置</button>
        </div>
      )
    });
    if (!accepted) {
      syncBusyRef.current = false;
      setSyncStatus("已取消从后端加载，当前本机配置未更改。");
      return;
    }

    setSyncBusy(true);
    setSyncAction("load");
    setSyncStatus("正在从后端加载...");
    try {
      await sendWorkerMessage({
        type: "sync:complete-first-connection",
        strategy: "use-remote",
        ...expectedWorkerSession(auth)
      });
      const next = await loadProfile();
      setProfile(next);
      setActiveGroupId(next.groups.find((group) => !group.deletedAt)?.id ?? "");
      setSyncStatus(`已从后端加载：${new Date().toLocaleString()}`);
    } catch (error) {
      setSyncStatus(error instanceof Error ? error.message : "加载失败");
    } finally {
      syncBusyRef.current = false;
      setSyncBusy(false);
      setSyncAction(null);
    }
  }

  async function disconnectBackend() {
    const auth = backendAuthRef.current;
    if (!auth || syncBusyRef.current) return;

    syncBusyRef.current = true;
    setSyncBusy(true);
    setSyncAction("logout");
    setSyncStatus("正在退出...");
    try {
      startupSyncRequestGateRef.current.invalidate();
      resourceRefreshGenerationRef.current += 1;
      resourceRefreshBusyRef.current = false;
      resourceRefreshManualRequestedRef.current = false;
      setResourceRefreshing(false);
      releaseRequestGateRef.current.invalidate();
      webPreviewRequestGateRef.current.invalidate();
      webPreviewRequestTokenRef.current = null;
      backendAuthRef.current = null;
      setBackendAuth(null);
      setReleaseNotice("");
      setWebPreviewUrls({});
      setSyncConflict(null);
      setSyncConflictChoices({});
      setRemoteStyles([]);
      setRemoteStyleStatus("已退出，后端风格和全网资源已停止更新。");
      setWebWallpapers(remoteWallpapers);
      setWebBackendNextPage(1);
      await sendWorkerMessage({
        type: "auth:logout",
        ...expectedWorkerSession(auth)
      }).catch(() => undefined);
      await persist({
        ...profileRef.current,
        sync: {
          ...profileRef.current.sync,
          provider: "none",
          status: "disabled",
          backendUrl: undefined,
          errorMessage: undefined
        }
      });
      setSyncStatus("已退出后端账号，本地配置仍保留。");
      setResourceFeedback({ tone: "info", message: "已停止刷新后端资源。" });
    } catch (error) {
      setSyncStatus(error instanceof Error ? `退出后的本机状态保存失败：${error.message}` : "退出后的本机状态保存失败");
    } finally {
      syncBusyRef.current = false;
      setSyncBusy(false);
      setSyncAction(null);
    }
  }

  async function saveProfileToGitHub() {
    if (githubBusyRef.current) return;
    const token = (githubAuth?.token || githubToken).trim();
    const gistId = (githubAuth?.gistId || githubGistId).trim();
    if (!token) {
      setGithubStatus("请填写 GitHub token。");
      return;
    }

    githubBusyRef.current = true;
    setGithubBusy(true);
    setGithubAction("backup");
    setGithubStatus("正在保存到 GitHub Gist...");
    try {
      const save = (allowOverwrite = false) => saveProfileToGitHubGist({
        token,
        gistId: gistId || undefined,
        profile: profileRef.current,
        expectedRemoteSha256: githubAuth?.canonicalProfileSha256,
        allowOverwrite
      });
      let result;
      try {
        result = await save();
      } catch (error) {
        if (!(error instanceof GistConflictError)) throw error;
        const overwrite = await dialogs.confirm({
          dedupeKey: "github-overwrite",
          title: "远端备份已发生变化",
          tone: "danger",
          confirmLabel: "仍然覆盖远端",
          cancelLabel: "返回检查",
          description: (
            <div className="dialog-impact danger-impact">
              <p>{error.message}</p>
              <p>继续会覆盖其他位置写入的新版本；取消不会修改远端 Gist。</p>
            </div>
          )
        });
        if (!overwrite) {
          setGithubStatus("已取消覆盖，远端备份未更改。");
          return;
        }
        result = await save(true);
      }
      const auth = {
        token,
        gistId: result.gistId,
        updatedAt: result.updatedAt,
        canonicalProfileSha256: result.canonicalProfileSha256
      };
      await saveGitHubSyncAuth(auth);
      setGithubAuth(auth);
      setGithubGistId(result.gistId);
      setGithubStatus(`已备份到 GitHub：${result.gistId}`);
    } catch (error) {
      setGithubStatus(error instanceof Error ? error.message : "GitHub 保存失败");
    } finally {
      githubBusyRef.current = false;
      setGithubBusy(false);
      setGithubAction(null);
    }
  }

  async function loadProfileFromGitHub() {
    if (githubBusyRef.current) return;
    const token = (githubAuth?.token || githubToken).trim();
    const gistId = (githubAuth?.gistId || githubGistId).trim();
    if (!token || !gistId) {
      setGithubStatus("请填写 GitHub token 和 Gist ID。");
      return;
    }
    const localSummary = summarizeProfileContent(profileRef.current);
    githubBusyRef.current = true;
    const accepted = await dialogs.confirm({
      dedupeKey: "github-profile-load",
      title: "使用 GitHub 备份替换当前浏览器？",
      tone: "warning",
      confirmLabel: "从 GitHub 加载",
      description: (
        <div className="dialog-impact">
          <p>将从指定 Gist 读取 {githubProfileFilename}，并替换当前浏览器的可同步配置。</p>
          <p>当前本机：{localSummary.groups} 个分组 · {localSummary.shortcuts} 个快捷方式 · {formatProfileUpdatedAt(localSummary.updatedAt)}</p>
          <p>本地上传图片、缓存与凭据不会从备份中恢复。</p>
          <button className="command" type="button" onClick={() => void exportConfig()}>先导出当前本机配置</button>
        </div>
      )
    });
    if (!accepted) {
      githubBusyRef.current = false;
      setGithubStatus("已取消从 GitHub 加载，当前配置未更改。");
      return;
    }

    setGithubBusy(true);
    setGithubAction("load");
    setGithubStatus("正在从 GitHub 加载...");
    try {
      const loadedBackup = await loadProfileFromGitHubGist({ token, gistId });
      const timestamp = new Date().toISOString();
      const next = sharedProfileToLocalProfile(loadedBackup.profile, profileRef.current);
      const auth = { token, gistId, updatedAt: timestamp, canonicalProfileSha256: loadedBackup.canonicalProfileSha256 };
      await saveGitHubSyncAuth(auth);
      setGithubAuth(auth);
      await persist(next, { touch: false });
      setActiveGroupId(next.groups.find((group) => !group.deletedAt)?.id ?? "");
      setGithubStatus(`已从 GitHub 加载 ${githubProfileFilename}`);
    } catch (error) {
      setGithubStatus(error instanceof Error ? error.message : "GitHub 加载失败");
    } finally {
      githubBusyRef.current = false;
      setGithubBusy(false);
      setGithubAction(null);
    }
  }

  async function disconnectGitHub() {
    if (githubBusyRef.current) return;
    githubBusyRef.current = true;
    setGithubBusy(true);
    setGithubAction("clear");
    setGithubStatus("正在清除本地 GitHub 凭据…");
    try {
      await clearGitHubSyncAuth();
      setGithubAuth(null);
      setGithubToken("");
      setGithubGistId("");
      setGithubStatus("已清除本地 GitHub 凭据，Gist 本身不会被删除。");
    } catch (error) {
      setGithubStatus(error instanceof Error ? `清除失败：${error.message}` : "清除 GitHub 凭据失败");
    } finally {
      githubBusyRef.current = false;
      setGithubBusy(false);
      setGithubAction(null);
    }
  }

  async function refreshBackendResources(
    auth = backendAuthRef.current,
    source: "background" | "manual" = "background"
  ) {
    const manual = source === "manual";
    if (resourceRefreshBusyRef.current) {
      if (manual) {
        resourceRefreshManualRequestedRef.current = true;
        setResourceFeedback({ tone: "pending", message: "资源刷新正在进行，请稍候…" });
      }
      return;
    }

    resourceRefreshBusyRef.current = true;
    setResourceRefreshing(true);
    const generation = ++resourceRefreshGenerationRef.current;
    if (manual) setResourceFeedback({ tone: "pending", message: "正在刷新后端资源…" });

    let previewToken: ScopedRequestToken | null = null;
    try {
      if (!auth) {
        releaseRequestGateRef.current.invalidate();
        webPreviewRequestGateRef.current.invalidate();
        webPreviewRequestTokenRef.current = null;
        setReleaseNotice("");
        setWebPreviewUrls({});
        setRemoteStyles([]);
        setRemoteStyleStatus("登录后可从后端拉取风格。");
        if (manual || resourceRefreshManualRequestedRef.current) {
          setResourceFeedback({ tone: "warning", message: "请先登录后端账号再刷新资源。" });
        }
        return;
      }

      if (!canWriteSyncSession(auth)) {
        webPreviewRequestGateRef.current.invalidate();
        webPreviewRequestTokenRef.current = null;
        setWebPreviewUrls({});
        setRemoteStyles([]);
        const message = auth.readOnly
          ? "迁移只读会话不会访问受保护的风格和壁纸目录。"
          : "完成首次连接选择后才能访问受保护的风格和壁纸目录。";
        setRemoteStyleStatus(message);
        setWebWallpapers(remoteWallpapers);
        setOfficialRemoteWallpapers([]);
        setWebBackendCursor(undefined);
        setWebBackendNextPage(0);
        await refreshPublicBootstrap();
        if (manual || resourceRefreshManualRequestedRef.current) {
          setResourceFeedback({ tone: "warning", message });
        }
        return;
      }

      const baseUrl = canonicalBackendBaseUrl(auth.baseUrl);
      const releaseToken = releaseRequestGateRef.current.begin(baseUrl);
      previewToken = webPreviewRequestGateRef.current.begin(buildWebPreviewRequestScope(auth));
      webPreviewRequestTokenRef.current = previewToken;
      setReleaseNotice("");
      setWebPreviewUrls({});

      const [stylesData, wallpapersData, officialData, bootstrapData] = await Promise.all([
        sendWorkerMessage<unknown>({ type: "catalog:get", kind: "styles" }),
        sendWorkerMessage<unknown>({ type: "catalog:get", kind: "web-wallpapers", query: "?pageSize=24" }),
        sendWorkerMessage<unknown>({ type: "catalog:get", kind: "official-wallpapers" }),
        sendWorkerMessage<unknown>({ type: "catalog:get", kind: "bootstrap" })
      ]);
      if (generation !== resourceRefreshGenerationRef.current || !isSameWritableSyncSession(auth, backendAuthRef.current)) return;

      const stylesRecord = stylesData && typeof stylesData === "object" ? stylesData as { items?: unknown } : {};
      const extensionVersion = typeof chrome !== "undefined" && chrome.runtime?.getManifest
        ? chrome.runtime.getManifest().version
        : "0.1.0";
      const styles = await normalizeVerifiedRemoteStyles(stylesRecord.items ?? stylesData, extensionVersion);
      if (generation !== resourceRefreshGenerationRef.current || !isSameWritableSyncSession(auth, backendAuthRef.current)) return;

      const wallpapers = normalizeBackendWallpaperCatalog(wallpapersData);
      const official = normalizeBackendWallpaperCatalog(officialData);
      setRemoteStyles(styles);
      setRemoteStyleStatus(styles.length ? `已加载 ${styles.length} 个后端风格。` : "后端暂无可用风格。");
      setWebWallpapers(mergeWebWallpapers(remoteWallpapers, wallpapers.wallpapers));
      setOfficialRemoteWallpapers(official.wallpapers);
      setWebBackendCursor(wallpapers.nextCursor);
      setWebBackendNextPage(wallpapers.nextCursor || wallpapers.page * wallpapers.pageSize < wallpapers.total ? wallpapers.page + 1 : 0);
      const bootstrap = bootstrapData && typeof bootstrapData === "object" ? bootstrapData as Record<string, unknown> : {};
      const release = (bootstrap.latestRelease ?? bootstrap.release) as Record<string, unknown> | undefined;
      commitReleaseNotice(
        releaseToken,
        typeof release?.version === "string" ? `可用版本 ${release.version}${typeof release.notes === "string" ? `：${release.notes}` : ""}` : ""
      );
      void hydrateWebPreviews(wallpapers.wallpapers, auth, previewToken);
      void hydrateWebPreviews(official.wallpapers, auth, previewToken);
      if (manual || resourceRefreshManualRequestedRef.current) {
        setResourceFeedback({
          tone: "success",
          message: `资源已刷新：${styles.length} 个风格、${official.wallpapers.length} 张官方壁纸、${wallpapers.wallpapers.length} 张全网壁纸 · ${new Date().toLocaleTimeString()}`
        });
      }
    } catch (error) {
      if (generation !== resourceRefreshGenerationRef.current) return;
      if (auth && !isSameWritableSyncSession(auth, backendAuthRef.current)) return;
      if (previewToken && webPreviewRequestGateRef.current.isCurrent(previewToken)) {
        webPreviewRequestGateRef.current.invalidate();
        webPreviewRequestTokenRef.current = null;
        setWebPreviewUrls({});
      }
      const message = error instanceof Error ? error.message : "后端资源加载失败";
      setRemoteStyleStatus(message);
      if (manual || resourceRefreshManualRequestedRef.current) {
        setResourceFeedback({ tone: "error", message: `刷新失败，已有资源仍可继续使用。${message}` });
      }
    } finally {
      if (generation === resourceRefreshGenerationRef.current) {
        resourceRefreshBusyRef.current = false;
        resourceRefreshManualRequestedRef.current = false;
        setResourceRefreshing(false);
      }
    }
  }

  async function refreshPublicBootstrap() {
    let releaseToken: ScopedRequestToken | null = null;
    releaseRequestGateRef.current.invalidate();
    setReleaseNotice("");
    try {
      const baseUrl = canonicalBackendBaseUrl(fixedBackendUrl);
      // Release metadata is public and may be shared by accounts on this exact backend URL,
      // while a URL change always starts a distinct generation.
      releaseToken = releaseRequestGateRef.current.begin(baseUrl);
      if (!releaseRequestGateRef.current.isCurrent(releaseToken)) return;
      const bootstrapData = await sendWorkerMessage<unknown>({ type: "catalog:get", kind: "bootstrap" });
      if (!releaseRequestGateRef.current.isCurrent(releaseToken)) return;
      const bootstrap = bootstrapData && typeof bootstrapData === "object" ? bootstrapData as Record<string, unknown> : {};
      const release = (bootstrap.latestRelease ?? bootstrap.release) as Record<string, unknown> | undefined;
      commitReleaseNotice(releaseToken, typeof release?.version === "string"
        ? `可用版本 ${release.version}${typeof release.notes === "string" ? `：${release.notes}` : ""}`
        : "");
    } catch {
      // Public bootstrap is advisory; authentication and local startup must remain usable.
    }
  }

  function commitReleaseNotice(token: ScopedRequestToken, notice: string) {
    if (!releaseRequestGateRef.current.isCurrent(token)) return;
    setReleaseNotice((current) => releaseRequestGateRef.current.isCurrent(token) ? notice : current);
  }

  async function refreshLocalWallpapers() {
    const assets = await listLocalWallpapers();
    const views = await Promise.all(
      assets.map(async (asset) => ({
        assetId: asset.assetId,
        name: asset.name,
        url: (await getLocalAssetUrl(asset.assetId)) ?? ""
      }))
    );
    setLocalWallpapers(views.filter((item) => item.url));
  }

  function mergeWebWallpapers(current: RemoteWallpaper[], incoming: RemoteWallpaper[]) {
    const seen = new Set(current.map((wallpaper) => wallpaper.sourcePageUrl));
    const merged = [...current];

    for (const wallpaper of incoming) {
      if (seen.has(wallpaper.sourcePageUrl)) continue;
      seen.add(wallpaper.sourcePageUrl);
      merged.push(wallpaper);
    }

    return merged;
  }

  const hydrateWebPreviews = useCallback(async (
    wallpapers: RemoteWallpaper[],
    auth: PublicWorkerSession,
    requestToken: ScopedRequestToken
  ) => {
    const pending = wallpapers.filter((wallpaper) => wallpaper.previewUrl);
    await Promise.all(
      pending.map(async (wallpaper) => {
        if (!wallpaper.previewUrl) return;

        try {
          const dataUrl = await fetchUhdpaperImageDataUrl(wallpaper.previewUrl);
          if (
            !webPreviewRequestGateRef.current.isCurrent(requestToken)
            || !isSameWritableSyncSession(auth, backendAuthRef.current)
          ) return;
          setWebPreviewUrls((current) => {
            if (
              !webPreviewRequestGateRef.current.isCurrent(requestToken)
              || !isSameWritableSyncSession(auth, backendAuthRef.current)
              || current[wallpaper.id]
            ) return current;
            return { ...current, [wallpaper.id]: dataUrl };
          });
        } catch {
          // Keep the CSS fallback for individual thumbnail failures.
        }
      })
    );
  }, []);

  const loadMoreWebWallpapers = useCallback(async () => {
    const auth = backendAuthRef.current;
    if (!auth || !canWriteSyncSession(auth)) {
      setWebError("登录后可使用全网资源壁纸。");
      return webWallpapers;
    }
    if (webLoadingRef.current) {
      return webWallpapers;
    }

    webLoadingRef.current = true;
    setWebLoading(true);

    try {
      if (webBackendNextPage > 0 || webBackendCursor) {
        const params = new URLSearchParams({ pageSize: "24" });
        if (profileRef.current.wallpaper.activeCategory !== "all") params.set("category", profileRef.current.wallpaper.activeCategory);
        if (webBackendCursor) params.set("cursor", webBackendCursor);
        else params.set("page", String(webBackendNextPage));
        const result = normalizeBackendWallpaperCatalog(
          await sendWorkerMessage<unknown>({ type: "catalog:get", kind: "web-wallpapers", query: `?${params}` })
        );
        if (!isSameWritableSyncSession(auth, backendAuthRef.current)) return webWallpapers;
        const previewToken = getCurrentWebPreviewRequestToken(auth);
        const mergedWallpapers = mergeWebWallpapers(webWallpapers, result.wallpapers);
        setWebWallpapers(mergedWallpapers);
        setWebBackendCursor(result.nextCursor);
        setWebBackendNextPage(result.nextCursor || result.page * result.pageSize < result.total ? result.page + 1 : 0);
        setWebError("");
        void hydrateWebPreviews(result.wallpapers, auth, previewToken);
        return mergedWallpapers;
      }

      if (!webNextPageUrl || webLoadedPagesRef.current.has(webNextPageUrl)) {
        return webWallpapers;
      }

      const result = await loadUhdpaperWallpaperPage(webNextPageUrl);
      if (!isSameWritableSyncSession(auth, backendAuthRef.current)) return webWallpapers;
      const previewToken = getCurrentWebPreviewRequestToken(auth);
      webLoadedPagesRef.current.add(webNextPageUrl);
      const mergedWallpapers = mergeWebWallpapers(webWallpapers, result.wallpapers);
      setWebWallpapers(mergedWallpapers);
      setWebNextPageUrl(result.nextPageUrl);
      setWebError("");
      void hydrateWebPreviews(result.wallpapers, auth, previewToken);
      return mergedWallpapers;
    } catch (error) {
      if (!isSameWritableSyncSession(auth, backendAuthRef.current)) return webWallpapers;
      setWebError(error instanceof Error ? error.message : "全网资源加载失败");
      return webWallpapers;
    } finally {
      webLoadingRef.current = false;
      setWebLoading(false);
    }
  }, [hydrateWebPreviews, webBackendCursor, webBackendNextPage, webNextPageUrl, webWallpapers]);

  function getCurrentWebPreviewRequestToken(auth: PublicWorkerSession) {
    const scope = buildWebPreviewRequestScope(auth);
    const current = webPreviewRequestTokenRef.current;
    if (current?.scope === scope && webPreviewRequestGateRef.current.isCurrent(current)) return current;
    const next = webPreviewRequestGateRef.current.begin(scope);
    webPreviewRequestTokenRef.current = next;
    setWebPreviewUrls({});
    return next;
  }

  async function saveWebWallpaperToLocal(wallpaper: RemoteWallpaper, variant: WallpaperVariant, shouldSelect: boolean) {
    const key = getRemoteWallpaperKey(wallpaper, variant.id);
    if (webSavingKeysRef.current.has(key)) return;
    webSavingKeysRef.current.add(key);
    setWebSavingKeys(new Set(webSavingKeysRef.current));

    try {
      if (canUsePackagedWallpaperVariant(variant)) {
        const currentProfile = profileRef.current;
        await persist(
          shouldSelect
            ? setProfileWallpaper(currentProfile, key)
            : addWallpaperToSelection(currentProfile, key)
        );
        setWebError("");
        return;
      }

      const blob = await fetchUhdpaperImageBlob(variant.url);
      const extension = blob.type.includes("png") ? "png" : "jpg";
      const file = new File([blob], `${wallpaper.title}-${variant.label}.${extension}`, {
        type: blob.type || "image/jpeg"
      });
      const asset = await saveLocalWallpaper(file);
      const localKey = `local:${asset.assetId}`;
      await refreshLocalWallpapers();
      const currentProfile = profileRef.current;
      await persist(
        shouldSelect
          ? setProfileWallpaper(currentProfile, localKey)
          : addWallpaperToSelection(currentProfile, localKey)
      );
      setWebError("");
    } catch (error) {
      setWebError(error instanceof Error ? error.message : "图片保存失败");
    } finally {
      webSavingKeysRef.current.delete(key);
      setWebSavingKeys(new Set(webSavingKeysRef.current));
    }
  }

  useEffect(() => {
    if (
      settingsOpen &&
      settingsTab === "wallpaper" &&
      profile.wallpaper.activeSourceTab === "web" &&
      webLoadedPagesRef.current.size === 0
    ) {
      void loadMoreWebWallpapers();
    }
  }, [loadMoreWebWallpapers, profile.wallpaper.activeSourceTab, settingsOpen, settingsTab]);

  function handleSettingsContentScroll(event: JSX.TargetedEvent<HTMLDivElement>) {
    if (settingsTab !== "wallpaper" || profile.wallpaper.activeSourceTab !== "web") return;

    const target = event.currentTarget;
    const nearBottom = target.scrollTop + target.clientHeight >= target.scrollHeight - 360;
    if (!nearBottom) return;

    const now = Date.now();
    if (now - webLastScrollLoadRef.current < 900) return;
    webLastScrollLoadRef.current = now;
    void loadMoreWebWallpapers();
  }

  function handleSettingsTabKeyDown(event: JSX.TargetedKeyboardEvent<HTMLElement>) {
    if (!["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    const tabs = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>("[role='tab']"));
    const currentIndex = tabs.indexOf(document.activeElement as HTMLButtonElement);
    if (currentIndex < 0) return;
    event.preventDefault();
    const previous = event.key === "ArrowUp" || event.key === "ArrowLeft";
    const nextIndex = event.key === "Home"
      ? 0
      : event.key === "End"
        ? tabs.length - 1
        : (currentIndex + (previous ? -1 : 1) + tabs.length) % tabs.length;
    tabs[nextIndex]?.focus();
    tabs[nextIndex]?.click();
  }

  const sortedGroups = useMemo(
    () => profile.groups.filter((group) => !group.deletedAt).sort((a, b) => a.sortIndex - b.sortIndex),
    [profile.groups]
  );
  const activeGroup = sortedGroups.find((group) => group.id === activeGroupId) ?? sortedGroups[0];
  const selectedSearchEngine =
    profile.search.engines.find((engine) => engine.id === profile.search.selectedEngineId) ?? profile.search.engines[0];
  const visibleShortcuts = useMemo(
    () =>
      profile.shortcuts
        .filter((shortcut) => shortcut.groupId === activeGroup?.id && !shortcut.deletedAt)
        .sort((a, b) => a.sortIndex - b.sortIndex),
    [activeGroup?.id, profile.shortcuts]
  );

  const wallpaperStyle = useMemo(() => {
    const selected = profile.wallpaper.selected;
    if (selected.kind === "local") {
      const local = localWallpapers.find((item) => item.assetId === selected.assetId);
      return local ? { backgroundImage: `url("${local.url}")` } : {};
    }

    if (selected.kind === "remote") {
      const remote = officialRemoteWallpapers.find((item) => item.id === selected.id) ?? webWallpapers.find((item) => item.id === selected.id) ?? remoteWallpapers.find((item) => item.id === selected.id);
      const variant = remote?.variants.find((item) => item.id === selected.variantId) ?? remote?.variants[0];
      const previewUrl = getWallpaperVariantUrl(variant);
      return previewUrl ? { backgroundImage: `url("${previewUrl}")` } : {};
    }

    const builtin = builtinWallpapers.find((item) => item.id === selected.id) ?? builtinWallpapers[0];
    return { backgroundImage: builtin.css };
  }, [localWallpapers, officialRemoteWallpapers, profile.wallpaper.selected, webWallpapers]);

  const appStyle = useMemo(
    () => {
      const iconMetrics = getShortcutIconSizeMetrics(profile.theme.iconSize);
      const densityMetrics = getShortcutDensityMetrics(profile.theme.density, profile.theme.iconSize);
      const visibleItemCount = visibleShortcuts.length + 1;
      const gridColumnCount = getShortcutGridColumnCount(profile.theme.columns, visibleItemCount);
      const tabletGridColumnCount = Math.min(4, gridColumnCount);
      const mobileGridColumnCount = Math.min(3, gridColumnCount);
      const narrowGridColumnCount = Math.min(2, gridColumnCount);
      const gridJustification = getShortcutGridJustification(profile.theme.density, gridColumnCount);
      const spreadsFullRow = gridJustification === "space-between";
      const getColumnGap = (columnCount: number) =>
        spreadsFullRow
          ? 0
          : getShortcutGridColumnGap(columnCount, iconMetrics, densityMetrics.preferredColumnGap);

      return {
        ...wallpaperStyle,
        "--columns": String(gridColumnCount),
        "--tablet-columns": String(tabletGridColumnCount),
        "--mobile-columns": String(mobileGridColumnCount),
        "--narrow-columns": String(narrowGridColumnCount),
        "--tile-size": `${iconMetrics.tileSize}px`,
        "--shortcut-row-gap": `${densityMetrics.rowGap}px`,
        "--shortcut-column-gap": `${getColumnGap(gridColumnCount)}px`,
        "--tablet-shortcut-column-gap": `${getColumnGap(tabletGridColumnCount)}px`,
        "--mobile-shortcut-column-gap": `${getColumnGap(mobileGridColumnCount)}px`,
        "--narrow-shortcut-column-gap": `${getColumnGap(narrowGridColumnCount)}px`,
        "--shortcut-grid-justify": gridJustification,
        "--shortcut-content-width": `${shortcutGridLayout.contentWidth}px`,
        "--shortcut-grid-padding-top": `${densityMetrics.paddingTop}px`,
        "--shortcut-grid-padding-bottom": `${densityMetrics.paddingBottom}px`,
        "--shortcut-grid-padding-inline": `${shortcutGridLayout.paddingInline}px`,
        "--shortcut-content-gap": `${densityMetrics.contentGap}px`,
        "--shortcut-title-height": `${shortcutGridLayout.titleHeight}px`,
        "--shortcut-tile-min-height": `${iconMetrics.tileMinHeight}px`,
        "--shortcut-row-height": `${getShortcutRowHeight(iconMetrics, densityMetrics)}px`,
        "--shortcut-icon-size": `${iconMetrics.iconSize}px`,
        "--shortcut-icon-image-size": `${iconMetrics.imageSize}px`,
        "--shortcut-icon-font-size": `${iconMetrics.fallbackFontSize}px`,
        "--shortcut-title-font-size": `${iconMetrics.titleFontSize}px`,
        "--shortcut-icon-radius": getShortcutIconShapeRadius(profile.theme.iconShape, iconMetrics.iconSize),
        "--wallpaper-overlay-opacity": String(profile.wallpaper.overlayOpacity),
        "--wallpaper-blur": `${profile.wallpaper.blur}px`
      } as JSX.CSSProperties;
    },
    [
      profile.theme.columns,
      profile.theme.density,
      profile.theme.iconShape,
      profile.theme.iconSize,
      profile.wallpaper.blur,
      profile.wallpaper.overlayOpacity,
      visibleShortcuts.length,
      wallpaperStyle
    ]
  );

  const visibleBuiltinWallpapers = useMemo(() => {
    if (profile.wallpaper.activeCategory === "all") return builtinWallpapers;
    if (profile.wallpaper.activeCategory === "local") return [];
    return builtinWallpapers.filter((wallpaper) => wallpaper.category === profile.wallpaper.activeCategory);
  }, [profile.wallpaper.activeCategory]);

  const visibleRemoteWallpapers = useMemo(() => {
    if (profile.wallpaper.activeCategory === "all") return webWallpapers;
    return webWallpapers.filter((wallpaper) => wallpaper.category === profile.wallpaper.activeCategory);
  }, [profile.wallpaper.activeCategory, webWallpapers]);

  const activeRemoteStyle = useMemo(
    () => remoteStyles.find((style) => style.id === profile.theme.styleId),
    [profile.theme.styleId, remoteStyles]
  );

  useEffect(() => {
    const elementId = "full-pro-remote-style";
    let styleElement = document.getElementById(elementId) as HTMLStyleElement | null;
    if (!activeRemoteStyle) {
      styleElement?.remove();
      return;
    }

    if (!styleElement) {
      styleElement = document.createElement("style");
      styleElement.id = elementId;
      document.head.appendChild(styleElement);
    }
    styleElement.textContent = activeRemoteStyle.css;
  }, [activeRemoteStyle]);

  function getActiveRemoteWallpaperPool(wallpapers: RemoteWallpaper[]) {
    if (profile.wallpaper.activeCategory === "all") return wallpapers;

    const categoryPool = wallpapers.filter((wallpaper) => wallpaper.category === profile.wallpaper.activeCategory);
    return categoryPool.length ? categoryPool : wallpapers;
  }

  function getLocalWallpaperUrlFromKey(wallpaperId: string) {
    if (!wallpaperId.startsWith("local:")) return undefined;
    const assetId = wallpaperId.slice("local:".length);
    return localWallpapers.find((item) => item.assetId === assetId)?.url;
  }

  function getSelectedWallpaperBackground(wallpaperId: string) {
    const localUrl = getLocalWallpaperUrlFromKey(wallpaperId);
    if (localUrl) return `url("${localUrl}")`;
    if (wallpaperId.startsWith("web:")) {
      const parsed = parseRemoteWallpaperKey(wallpaperId);
      const wallpaper =
        officialRemoteWallpapers.find((item) => item.id === parsed?.id) ?? webWallpapers.find((item) => item.id === parsed?.id) ?? remoteWallpapers.find((item) => item.id === parsed?.id);
      const variant = wallpaper?.variants.find((item) => item.id === parsed?.variantId) ?? wallpaper?.variants[0];
      const previewUrl = getWallpaperVariantUrl(variant) ?? (wallpaper ? webPreviewUrls[wallpaper.id] : undefined);
      return previewUrl && wallpaper ? `url("${previewUrl}"), ${wallpaper.previewCss}` : getWallpaperPreviewBackground(wallpaperId);
    }
    const preview = getWallpaperPreviewBackground(wallpaperId);
    if (!preview) return undefined;
    return preview;
  }

  async function submitSearch(event: Event) {
    event.preventDefault();
    const text = query.trim();
    if (!text) return;

    if (profile.search.mode === "browser-default" && typeof chrome !== "undefined" && chrome.search?.query) {
      await chrome.search.query({ text, disposition: profile.search.disposition });
      return;
    }

    const url = getSearchUrl(profile, text);
    if (profile.search.disposition === "NEW_TAB" && typeof chrome !== "undefined" && chrome.tabs?.create) {
      await chrome.tabs.create({ url });
      return;
    }

    if (profile.search.disposition === "NEW_TAB") {
      window.open(url, "_blank", "noopener,noreferrer");
      return;
    }

    window.location.href = url;
  }

  function finishShortcutEditMode() {
    if (shortcutPressTimerRef.current !== null) {
      window.clearTimeout(shortcutPressTimerRef.current);
      shortcutPressTimerRef.current = null;
    }
    shortcutPressRef.current = null;
    shortcutDragRef.current = null;
    setShortcutDrag(null);
    setShortcutEditMode(false);
    setGroupSwitcherVisible(false);
  }

  function beginShortcutEditMode() {
    setShortcutMenu(null);
    setWallpaperMenu(null);
    setSettingsOpen(false);
    setShortcutForm(null);
    setShortcutEditMode(true);
    setGroupSwitcherVisible(true);
  }

  function beginShortcutDrag(shortcut: Shortcut, x: number, y: number) {
    if (shortcutPressTimerRef.current !== null) {
      window.clearTimeout(shortcutPressTimerRef.current);
      shortcutPressTimerRef.current = null;
    }
    shortcutPressRef.current = null;
    setShortcutMenu(null);
    setWallpaperMenu(null);
    const nextDrag = {
      shortcut,
      startX: x,
      startY: y,
      x,
      y,
      hasMoved: false,
      editMode: shortcutEditMode,
      targetGroupId: shortcut.groupId
    };
    shortcutDragRef.current = nextDrag;
    setShortcutDrag(nextDrag);
  }

  function openNewShortcut() {
    finishShortcutEditMode();
    setShortcutForm(emptyForm(activeGroup?.id ?? profile.groups.find((group) => !group.deletedAt)!.id));
    setFormError("");
  }

  function openEditShortcut(shortcut: Shortcut) {
    finishShortcutEditMode();
    setShortcutForm({
      id: shortcut.id,
      groupId: shortcut.groupId,
      title: shortcut.title,
      url: shortcut.url,
      iconMode: getShortcutIconMode(shortcut),
      iconText: getShortcutIconText(shortcut),
      iconUrl: getShortcutIconUrl(shortcut)
    });
    setFormError("");
  }

  function openShortcutMenu(event: JSX.TargetedMouseEvent<HTMLElement>, shortcut: Shortcut) {
    event.preventDefault();
    event.stopPropagation();
    setWallpaperMenu(null);
    setShortcutMenu({
      shortcut,
      left: Math.min(event.clientX, window.innerWidth - 238),
      top: Math.min(event.clientY, window.innerHeight - 258)
    });
  }

  function isControlTarget(target: HTMLElement) {
    return Boolean(
      target.closest(
        ".shortcut-tile, .search-box, .group-scroll-rail, .side-rail, .settings-panel, .modal-backdrop, .shortcut-context-menu, .wallpaper-context-menu"
      )
    );
  }

  function openWallpaperMenu(event: JSX.TargetedMouseEvent<HTMLElement>) {
    const target = event.target as HTMLElement;
    if (isControlTarget(target)) return;

    event.preventDefault();
    setShortcutMenu(null);
    setWallpaperMenu({
      left: Math.max(12, Math.min(event.clientX, window.innerWidth - 182)),
      top: Math.max(12, Math.min(event.clientY, window.innerHeight - 236))
    });
  }

  async function openShortcutWithDisposition(shortcut: Shortcut, disposition: "foreground" | "background" | "incognito") {
    setShortcutMenu(null);

    if (typeof chrome !== "undefined") {
      if (disposition === "incognito" && chrome.windows?.create) {
        await chrome.windows.create({ url: shortcut.url, incognito: true });
        return;
      }

      if (chrome.tabs?.create) {
        await chrome.tabs.create({ url: shortcut.url, active: disposition === "foreground" });
        return;
      }
    }

    window.open(shortcut.url, "_blank", "noopener,noreferrer");
  }

  function startShortcutPress(event: JSX.TargetedMouseEvent<HTMLElement>, shortcut: Shortcut) {
    if (event.button !== 0) return;

    if (shortcutEditMode) {
      event.preventDefault();
      beginShortcutDrag(shortcut, event.clientX, event.clientY);
      return;
    }

    shortcutPressRef.current = {
      shortcut,
      startX: event.clientX,
      startY: event.clientY
    };
    shortcutPressTimerRef.current = window.setTimeout(() => {
      shortcutPressTimerRef.current = null;
      const press = shortcutPressRef.current;
      if (!press) return;

      beginShortcutDrag(press.shortcut, press.startX, press.startY);
    }, 1000);
  }

  function markShortcutDragTarget(shortcut: Shortcut) {
    const drag = shortcutDragRef.current;
    if (!drag || drag.shortcut.id === shortcut.id) return;

    const nextDrag = {
      ...drag,
      targetId: shortcut.id,
      targetGroupId: shortcut.groupId
    };
    shortcutDragRef.current = nextDrag;
    setShortcutDrag(nextDrag);
  }

  function maybeSuppressShortcutClick(event: JSX.TargetedMouseEvent<HTMLAnchorElement>) {
    if (!shortcutEditMode && !suppressShortcutClickRef.current) return;

    event.preventDefault();
    event.stopPropagation();
  }

  async function submitShortcut(event: Event) {
    event.preventDefault();
    if (!shortcutForm) return;

    try {
      const builtIcon = buildShortcutIcon({
        mode: shortcutForm.iconMode,
        title: shortcutForm.title,
        url: shortcutForm.url,
        iconText: shortcutForm.iconText,
        iconUrl: shortcutForm.iconUrl
      });
      const input: ShortcutInput = {
        id: shortcutForm.id,
        groupId: shortcutForm.groupId,
        title: shortcutForm.title,
        url: shortcutForm.url,
        icon: builtIcon
      };
      const next = upsertShortcut(profileRef.current, input);
      await persist(next);
      setShortcutForm(null);
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "保存失败");
    }
  }

  async function removeShortcut(shortcutId: string) {
    const current = profileRef.current;
    await persist(deleteShortcut(current, shortcutId, current.deviceId));
  }

  async function selectWallpaper(id: string) {
    await persist(setProfileWallpaper(profileRef.current, id));
  }

  async function uploadWallpapers(files: FileList | null) {
    if (!files?.length || wallpaperActionRef.current) return;
    const images = Array.from(files).filter((file) => file.type.startsWith("image/"));
    if (!images.length) {
      setWallpaperFeedback({ tone: "error", message: "没有可导入的图片，请选择 JPG、PNG、WebP 等图片文件。" });
      return;
    }

    wallpaperActionRef.current = "upload";
    setWallpaperAction("upload");
    setWallpaperFeedback({ tone: "pending", message: `正在导入 ${images.length} 张图片…` });
    const saved = [];
    let failed = 0;
    try {
      for (const file of images) {
        try {
          saved.push(await saveLocalWallpaper(file));
        } catch {
          failed += 1;
        }
      }
      await refreshLocalWallpapers();
      if (saved[0]) await selectWallpaper(`local:${saved[0].assetId}`);
      if (!saved.length) throw new Error("所选图片均无法读取");
      setWallpaperFeedback({
        tone: failed ? "warning" : "success",
        message: failed ? `已导入 ${saved.length} 张；${failed} 张无法读取。` : `已导入 ${saved.length} 张图片。`
      });
    } catch (error) {
      setWallpaperFeedback({ tone: "error", message: error instanceof Error ? `导入失败：${error.message}` : "图片导入失败" });
    } finally {
      wallpaperActionRef.current = null;
      setWallpaperAction(null);
    }
  }

  async function rotateSelectedWallpaper() {
    const current = profileRef.current;
    const currentId = getWallpaperKey(current);
    const fallbackIds = getAllWallpaperCandidateIds();
    const candidates = buildWallpaperRotationCandidates({
      primaryIds: current.wallpaper.selectedIds,
      fallbackIds,
      currentId
    });
    if (candidates.length === 0) throw new Error("至少选择一张可用壁纸后才能随机切换");
    const nextPick = pickNextWallpaper(candidates, current.wallpaper.rotationHistory);
    const next = setProfileWallpaper(current, nextPick.id);
    await persist({
      ...next,
      wallpaper: {
        ...next.wallpaper,
        rotationMode: "random",
        rotationHistory: nextPick.nextHistory
      }
    });
    return nextPick.id;
  }

  async function rotateWebWallpaper() {
    if (!backendAuthRef.current) {
      throw new Error("登录后可随机切换全网资源壁纸");
    }

    let sourcePool = webWallpapers.length ? webWallpapers : remoteWallpapers;
    let remotePool = getActiveRemoteWallpaperPool(sourcePool);

    if (remotePool.length <= 1 && (webBackendNextPage > 0 || webNextPageUrl)) {
      sourcePool = await loadMoreWebWallpapers();
      remotePool = getActiveRemoteWallpaperPool(sourcePool.length ? sourcePool : remoteWallpapers);
    }

    const fallbackIds = remotePool.map((wallpaper) => getRemoteWallpaperKey(wallpaper));
    const current = profileRef.current;
    if (fallbackIds.length > 0 && !hasWallpaperRotationAlternative(fallbackIds, getWallpaperKey(current))) {
      throw new Error("全网资源当前只有这张壁纸，加载更多后才能继续更换");
    }

    const candidates = buildWallpaperRotationCandidates({
      primaryIds: [],
      fallbackIds,
      currentId: getWallpaperKey(current)
    });
    if (candidates.length === 0) {
      throw new Error("全网资源没有可随机的壁纸");
    }

    const nextPick = pickNextWallpaper(candidates, current.wallpaper.rotationHistory);
    const parsed = parseRemoteWallpaperKey(nextPick.id);
    const wallpaper = remotePool.find((item) => item.id === parsed?.id);
    const variant = wallpaper?.variants.find((item) => item.id === parsed?.variantId) ?? wallpaper?.variants[0];
    if (!wallpaper || !variant) {
      throw new Error("全网资源缺少可用分辨率");
    }

    if (canUsePackagedWallpaperVariant(variant)) {
      const next = setProfileWallpaper(current, nextPick.id);
      await persist({
        ...next,
        wallpaper: {
          ...next.wallpaper,
          rotationMode: "random",
          rotationSource: "web",
          rotationHistory: nextPick.nextHistory
        }
      });
      setWebError("");
      return wallpaper.title;
    }

    try {
      const blob = await fetchUhdpaperImageBlob(variant.url);
      const extension = blob.type.includes("png") ? "png" : "jpg";
      const file = new File([blob], `${wallpaper.title}-${variant.label}.${extension}`, {
        type: blob.type || "image/jpeg"
      });
      const asset = await saveLocalWallpaper(file);
      const localKey = `local:${asset.assetId}`;
      await refreshLocalWallpapers();
      const next = setProfileWallpaper(profileRef.current, localKey);
      await persist({
        ...next,
        wallpaper: {
          ...next.wallpaper,
          rotationMode: "random",
          rotationSource: "web",
          rotationHistory: nextPick.nextHistory
        }
      });
      setWebError("");
      return wallpaper.title;
    } catch (error) {
      const message = error instanceof Error ? error.message : "全网资源图片保存失败";
      setWebError(message);
      throw new Error(message);
    }
  }

  async function rotateWallpaper() {
    if (backupActionRef.current === "bookmarks") return;
    if (wallpaperActionRef.current) return;
    wallpaperActionRef.current = "rotate";
    setWallpaperAction("rotate");
    setWallpaperFeedback({ tone: "pending", message: "正在切换下一张壁纸…" });
    try {
      const current = profileRef.current;
      const result = current.wallpaper.rotationSource === "web"
        ? await rotateWebWallpaper()
        : await rotateSelectedWallpaper();
      setWallpaperFeedback({ tone: "success", message: result ? `已切换壁纸：${result}` : "已切换到下一张壁纸。" });
    } catch (error) {
      const message = error instanceof Error ? error.message : "壁纸切换失败";
      setWebError(message);
      setWallpaperFeedback({ tone: "error", message });
    } finally {
      wallpaperActionRef.current = null;
      setWallpaperAction(null);
    }
  }

  async function updateProfile(mutator: (current: Profile) => Profile) {
    try {
      await persist(mutator(profileRef.current));
    } catch {
      // persist 已将可恢复错误展示在设置标题区，避免事件处理器产生未处理拒绝。
    }
  }

  async function selectSearchEngine(engineId: string) {
    setSearchEngineMenuOpen(false);
    await updateProfile((current) => ({
      ...current,
      search: {
        ...current.search,
        mode: "custom",
        selectedEngineId: engineId
      }
    }));
  }

  function revealGroupSwitcher() {
    setGroupSwitcherVisible(true);
    if (groupSwitcherTimerRef.current !== null) {
      window.clearTimeout(groupSwitcherTimerRef.current);
    }
    groupSwitcherTimerRef.current = window.setTimeout(() => {
      setGroupSwitcherVisible(false);
      groupSwitcherTimerRef.current = null;
    }, 900);
  }

  function switchGroupByOffset(offset: number) {
    if (!sortedGroups.length) return;
    const drag = shortcutDragRef.current;
    const currentGroupId = drag?.targetGroupId ?? activeGroup?.id;
    const currentIndex = Math.max(
      0,
      sortedGroups.findIndex((group) => group.id === currentGroupId)
    );
    const nextIndex = (currentIndex + offset + sortedGroups.length) % sortedGroups.length;
    const nextGroupId = sortedGroups[nextIndex].id;
    setActiveGroupId(nextGroupId);
    if (drag && shortcutEditMode) {
      const nextDrag = {
        ...drag,
        targetId: undefined,
        targetGroupId: nextGroupId
      };
      shortcutDragRef.current = nextDrag;
      setShortcutDrag(nextDrag);
    }
    revealGroupSwitcher();
  }

  function handleGroupWheel(event: JSX.TargetedWheelEvent<HTMLElement>) {
    if (settingsOpen || event.ctrlKey || !(event.target instanceof Element)) return;
    if (event.target.closest(nativeWheelSurfaceSelector)) return;
    if (event.deltaY === 0 || Math.abs(event.deltaX) > Math.abs(event.deltaY)) return;

    event.preventDefault();
    event.stopPropagation();
    if (shortcutDragRef.current && !shortcutEditMode) return;
    if (groupWheelLockedRef.current) return;

    groupWheelLockedRef.current = true;
    switchGroupByOffset(event.deltaY > 0 ? 1 : -1);
    window.setTimeout(() => {
      groupWheelLockedRef.current = false;
    }, 260);
  }

  useEffect(() => {
    if (!shortcutEditMode) return;

    const finishOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      finishShortcutEditMode();
    };
    window.addEventListener("keydown", finishOnEscape);
    return () => window.removeEventListener("keydown", finishOnEscape);
  }, [shortcutEditMode]);

  async function setWallpaperSourceTab(tab: Profile["wallpaper"]["activeSourceTab"]) {
    await updateProfile((current) => ({
      ...current,
      wallpaper: {
        ...current.wallpaper,
        activeSourceTab: tab,
        activeCategory: "all"
      }
    }));
  }

  async function addToSelected(wallpaperId: string) {
    await persist(addWallpaperToSelection(profileRef.current, wallpaperId));
  }

  async function removeFromSelected(wallpaperId: string) {
    await persist(removeWallpaperFromSelection(profileRef.current, wallpaperId));
  }

  function getAllWallpaperCandidateIds() {
    const localIds = localWallpapers.map((wallpaper) => `local:${wallpaper.assetId}`);
    const remotePool = webWallpapers.length ? webWallpapers : remoteWallpapers;
    const remoteIds = [...officialRemoteWallpapers, ...remotePool].map((wallpaper) => getRemoteWallpaperKey(wallpaper));
    return [...builtinWallpapers.map((wallpaper) => wallpaper.id), ...localIds, ...remoteIds];
  }

  async function createGroup() {
    const current = profileRef.current;
    const next = addShortcutGroup(current, `分组 ${current.groups.filter((group) => !group.deletedAt).length + 1}`);
    const created = next.groups.filter((group) => !group.deletedAt).sort((a, b) => b.sortIndex - a.sortIndex)[0];
    await persist(next);
    setActiveGroupId(created.id);
    setSettingsTab("groups");
  }

  async function renameGroup(groupId: string, title: string) {
    await persist(renameShortcutGroup(profileRef.current, groupId, title));
  }

  async function deleteGroup(groupId: string) {
    const current = profileRef.current;
    const groups = current.groups.filter((group) => !group.deletedAt).sort((a, b) => a.sortIndex - b.sortIndex);
    const group = groups.find((item) => item.id === groupId);
    const target = groups.find((item) => item.id !== groupId);
    if (!group || !target) return;
    const shortcutCount = current.shortcuts.filter((shortcut) => !shortcut.deletedAt && shortcut.groupId === groupId).length;
    const accepted = await dialogs.confirm({
      dedupeKey: `group-delete:${groupId}`,
      title: `删除“${group.title}”分组？`,
      tone: "warning",
      confirmLabel: "删除分组",
      description: (
        <div className="dialog-impact">
          <p>分组会被删除，但其中的快捷方式不会丢失。</p>
          <p><strong>{shortcutCount}</strong> 个快捷方式将移动到“<strong>{target.title}</strong>”。</p>
        </div>
      )
    });
    if (!accepted) return;
    const next = deleteShortcutGroup(current, groupId);
    await persist(next);
    if (activeGroupId === groupId) {
      const firstGroup = next.groups.filter((group) => !group.deletedAt).sort((a, b) => a.sortIndex - b.sortIndex)[0];
      setActiveGroupId(firstGroup?.id ?? "");
    }
  }

  async function moveGroup(groupId: string, offset: -1 | 1) {
    const index = sortedGroups.findIndex((group) => group.id === groupId);
    const target = sortedGroups[index + offset];
    if (!target) return;
    await persist(swapShortcutGroupOrder(profileRef.current, groupId, target.id));
  }

  useEffect(() => {
    if (!loaded || profile.wallpaper.rotationMode !== "random") {
      rotationLastRunAtRef.current = null;
      return;
    }

    const intervalSeconds = normalizeWallpaperIntervalSeconds(profile.wallpaper.rotationIntervalSeconds || 60);
    rotationLastRunAtRef.current ??= Date.now();
    let timer = 0;
    let cancelled = false;
    const schedule = () => {
      const delay = getWallpaperRotationDelayMs(intervalSeconds, rotationLastRunAtRef.current!, Date.now());
      timer = window.setTimeout(() => {
        if (cancelled) return;
        rotationLastRunAtRef.current = Date.now();
        void rotateWallpaper().finally(() => {
          if (!cancelled) schedule();
        });
      }, delay);
    };
    schedule();

    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [
    loaded,
    profile.wallpaper.rotationMode,
    profile.wallpaper.rotationSource,
    profile.wallpaper.rotationIntervalSeconds,
    profile.wallpaper.selectedIds,
    profile.wallpaper.rotationHistory,
    visibleRemoteWallpapers,
    webWallpapers
  ]);

  useEffect(() => {
    if (profile.wallpaper.rotationSource === "web" && webLoadedPagesRef.current.size === 0) {
      void loadMoreWebWallpapers();
    }
  }, [loadMoreWebWallpapers, profile.wallpaper.rotationSource]);

  return (
    <main
      className={`newtab-root app-shell density-${profile.theme.density}${shortcutEditMode ? " shortcut-edit-mode" : ""}`}
      data-style-id={activeRemoteStyle?.id ?? "quark-flow"}
      style={appStyle}
      onWheel={handleGroupWheel}
      onContextMenu={openWallpaperMenu}
    >
      <div className="wallpaper-overlay" />
      <aside className={`side-rail ${profile.theme.sidebarSide}`}>
        <button className="rail-button" type="button" title="添加图标" aria-label="添加图标" onClick={openNewShortcut}>
          <Plus size={21} />
        </button>
        <button
          className="rail-button"
          type="button"
          title="随机壁纸"
          aria-label="随机壁纸"
          onClick={() => void rotateWallpaper()}
        >
          <Shuffle size={20} />
        </button>
        <button
          className="rail-button"
          type="button"
          title="设置"
          aria-label="设置"
          onClick={() => {
            finishShortcutEditMode();
            setSettingsOpen(true);
          }}
        >
          <Settings size={20} />
        </button>
      </aside>

      <section className="home-stage" aria-busy={!loaded}>
        {shortcutEditMode ? (
          <div className="shortcut-edit-toolbar" role="toolbar" aria-label="链接编辑工具栏">
            <div>
              <strong>编辑链接</strong>
              <span>拖动排序 · 滚轮切换分组 · Esc 完成</span>
            </div>
            <button type="button" onClick={finishShortcutEditMode}>完成</button>
          </div>
        ) : null}
        <form className="search-box" ref={searchBoxRef} onSubmit={(event) => void submitSearch(event)}>
          <div className="search-engine-picker">
            <button
              className="search-engine-trigger"
              type="button"
              aria-label={`当前搜索引擎：${selectedSearchEngine.title}，点击切换`}
              aria-expanded={searchEngineMenuOpen}
              aria-haspopup="menu"
              title={`切换搜索引擎：${selectedSearchEngine.title}`}
              onClick={() => setSearchEngineMenuOpen((open) => !open)}
            >
              <SearchEngineLogo engine={selectedSearchEngine} />
            </button>
            {searchEngineMenuOpen ? (
              <div className="search-engine-menu" role="menu" aria-label="选择搜索引擎">
                {profile.search.engines.map((engine) => (
                  <button
                    key={engine.id}
                    className={engine.id === selectedSearchEngine.id ? "search-engine-option active" : "search-engine-option"}
                    type="button"
                    role="menuitemradio"
                    aria-checked={engine.id === selectedSearchEngine.id}
                    onClick={() => void selectSearchEngine(engine.id)}
                  >
                    <SearchEngineLogo engine={engine} />
                    <span>{engine.title}</span>
                  </button>
                ))}
              </div>
            ) : null}
          </div>
          <input
            value={query}
            onInput={(event) => setQuery(event.currentTarget.value)}
            placeholder="搜索或输入地址"
            aria-label="搜索或输入地址"
          />
          <button className="search-submit" type="submit" aria-label="搜索" title="搜索">
            <Search size={22} />
          </button>
        </form>

        <nav
          className={(groupSwitcherVisible || shortcutEditMode) && !settingsOpen ? "group-scroll-rail visible" : "group-scroll-rail"}
          aria-label="滚轮切换分组"
        >
          <div className="group-scroll-marks">
            {sortedGroups.map((group) => (
              <button
                key={group.id}
                type="button"
                className={group.id === activeGroup?.id ? "active" : ""}
                tabIndex={groupSwitcherVisible || shortcutEditMode ? 0 : -1}
                title={group.title}
                aria-label={group.title}
                onClick={() => {
                  setActiveGroupId(group.id);
                  revealGroupSwitcher();
                }}
              />
            ))}
          </div>
          <span className="group-scroll-label">{activeGroup?.title}</span>
        </nav>

        <section
          className={shortcutEditMode ? "shortcut-grid is-editing" : "shortcut-grid"}
          aria-label={`${activeGroup?.title ?? "导航"}${shortcutEditMode ? "，编辑中" : ""}`}
        >
          {visibleShortcuts.map((shortcut, index) => (
            <article
              className={`shortcut-tile${shortcutDrag?.targetId === shortcut.id ? " drop-target" : ""}${shortcutDrag?.shortcut.id === shortcut.id ? " is-dragging" : ""}`}
              key={shortcut.id}
              data-shortcut-id={shortcut.id}
              data-shortcut-group-id={shortcut.groupId}
              style={{ "--shortcut-edit-delay": `${index * -37}ms` } as JSX.CSSProperties}
              onContextMenu={(event) => openShortcutMenu(event, shortcut)}
              onMouseDown={(event) => startShortcutPress(event, shortcut)}
              onMouseEnter={() => markShortcutDragTarget(shortcut)}
              onMouseMove={() => markShortcutDragTarget(shortcut)}
              onDragStart={(event) => event.preventDefault()}
            >
              <a
                href={shortcut.url}
                aria-label={shortcutEditMode ? `${shortcut.title}，可拖动排序` : shortcut.title}
                data-tooltip={shortcut.title}
                draggable={false}
                onClick={maybeSuppressShortcutClick}
              >
                <ShortcutIconView shortcut={shortcut} />
                <span className="shortcut-title">{shortcut.title}</span>
              </a>
            </article>
          ))}
          <button
            className="shortcut-tile add-tile"
            type="button"
            aria-label="添加"
            data-tooltip="添加"
            onClick={openNewShortcut}
          >
            <span className="shortcut-icon">
              <Plus size={24} />
            </span>
            <span className="shortcut-title">添加</span>
          </button>
        </section>
      </section>

      {shortcutMenu ? (
        <div
          className="shortcut-context-menu"
          role="menu"
          style={{ left: shortcutMenu.left, top: shortcutMenu.top }}
          onClick={(event) => event.stopPropagation()}
          onContextMenu={(event) => event.preventDefault()}
        >
          <button type="button" role="menuitem" onClick={() => void openShortcutWithDisposition(shortcutMenu.shortcut, "foreground")}>
            在新标签页中打开
          </button>
          <button type="button" role="menuitem" onClick={() => void openShortcutWithDisposition(shortcutMenu.shortcut, "background")}>
            在后台页中打开
          </button>
          <button type="button" role="menuitem" onClick={() => void openShortcutWithDisposition(shortcutMenu.shortcut, "incognito")}>
            在隐身窗口中打开
          </button>
          <span className="menu-separator" aria-hidden="true" />
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              openEditShortcut(shortcutMenu.shortcut);
              setShortcutMenu(null);
            }}
          >
            <Edit3 size={16} />
            编辑链接
          </button>
          <button
            type="button"
            role="menuitem"
            className="danger"
            onClick={() => {
              void removeShortcut(shortcutMenu.shortcut.id);
              setShortcutMenu(null);
            }}
          >
            <Trash2 size={16} />
            删除
          </button>
        </div>
      ) : null}

      {wallpaperMenu ? (
        <div
          className="wallpaper-context-menu"
          role="menu"
          style={{ left: wallpaperMenu.left, top: wallpaperMenu.top }}
          onClick={(event) => event.stopPropagation()}
          onContextMenu={(event) => event.preventDefault()}
        >
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              openNewShortcut();
              setWallpaperMenu(null);
            }}
          >
            添加图标
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              if (shortcutEditMode) finishShortcutEditMode();
              else beginShortcutEditMode();
              setWallpaperMenu(null);
            }}
          >
            <Edit3 size={16} />
            {shortcutEditMode ? "完成编辑" : "编辑布局"}
          </button>
          <span className="menu-separator" aria-hidden="true" />
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              void rotateWallpaper();
              setWallpaperMenu(null);
            }}
          >
            下一张壁纸
          </button>
          <span className="menu-separator" aria-hidden="true" />
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              finishShortcutEditMode();
              setSettingsTab("wallpaper");
              setSettingsOpen(true);
              setWallpaperMenu(null);
            }}
          >
            设置
          </button>
        </div>
      ) : null}

      {shortcutDrag ? (
        <div className="shortcut-drag-ghost" style={{ left: shortcutDrag.x, top: shortcutDrag.y }} aria-hidden="true">
          <ShortcutIconView shortcut={shortcutDrag.shortcut} />
          <span>{shortcutDrag.shortcut.title}</span>
        </div>
      ) : null}

      {settingsOpen ? (
        <section
          ref={settingsPanelRef}
          className="settings-panel"
          role="dialog"
          aria-modal="true"
          aria-labelledby="settings-title"
        >
          <header>
            <div>
              <p className="kicker">kekeio</p>
              <h2 id="settings-title">设置</h2>
            </div>
            <div className="settings-header-actions">
              <ActionStatus feedback={localSaveFeedback} compact />
              <button
                ref={settingsCloseRef}
                type="button"
                className="icon-button"
                aria-label="关闭设置"
                disabled={backupAction === "bookmarks"}
                onClick={() => {
                  if (backupActionRef.current === "bookmarks") return;
                  setSettingsOpen(false);
                }}
              >
                <X size={22} />
              </button>
            </div>
          </header>

          <div className="settings-body">
            <nav className="settings-tabs" aria-label="设置分类" role="tablist" onKeyDown={handleSettingsTabKeyDown}>
              {[
                ["general", "常规"],
                ["groups", "分组"],
                ["wallpaper", "壁纸"],
                ["search", "搜索"],
                ["backup", "备份"],
                ["sync", "同步"]
              ].map(([id, label]) => (
                <button
                  key={id}
                  id={`settings-tab-${id}`}
                  type="button"
                  role="tab"
                  aria-selected={settingsTab === id}
                  aria-controls="settings-tabpanel"
                  tabIndex={settingsTab === id ? 0 : -1}
                  className={settingsTab === id ? "active" : ""}
                  onClick={() => setSettingsTab(id as typeof settingsTab)}
                >
                  {label}
                </button>
              ))}
            </nav>

            <div
              id="settings-tabpanel"
              className="settings-content"
              role="tabpanel"
              aria-labelledby={`settings-tab-${settingsTab}`}
              aria-busy={backupAction === "bookmarks" || undefined}
              onScroll={handleSettingsContentScroll}
            >
              <fieldset className="settings-content-lock" disabled={backupAction === "bookmarks"}>
              {settingsTab === "general" ? (
                <div className="settings-section">
                  <label className="field-row">
                    <span>密度</span>
                    <select
                      value={profile.theme.density}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          theme: { ...current.theme, density: event.currentTarget.value as Profile["theme"]["density"] }
                        }))
                      }
                    >
                      <option value="comfortable">舒展</option>
                      <option value="compact">紧凑</option>
                    </select>
                  </label>
                  <label className="field-row">
                    <span>页面风格</span>
                    <select
                      value={profile.theme.styleId}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          theme: { ...current.theme, styleId: event.currentTarget.value as Profile["theme"]["styleId"] }
                        }))
                      }
                    >
                      <option value="quark-flow">夸克风</option>
                      {remoteStyles.map((style) => (
                        <option key={style.id} value={style.id}>
                          {style.name} · {style.version}
                        </option>
                      ))}
                    </select>
                  </label>
                  <div className="field-row action-row">
                    <span>风格更新</span>
                    <div className="inline-actions">
                      <button
                        className="command"
                        type="button"
                        disabled={resourceRefreshing || !canWriteSyncSession(backendAuth)}
                        aria-busy={resourceRefreshing || undefined}
                        onClick={() => void refreshBackendResources(undefined, "manual")}
                      >
                        <BusyLabel busy={resourceRefreshing} busyText="正在刷新…">刷新后端资源</BusyLabel>
                      </button>
                      {resourceFeedback.message ? (
                        <ActionStatus feedback={resourceFeedback} compact />
                      ) : (
                        <small>{remoteStyleStatus || (backendAuth ? "可从后端拉取新风格。" : "登录后可使用后端风格。")}</small>
                      )}
                    </div>
                  </div>
                  <label className="field-row">
                    <span>图标大小</span>
                    <select
                      value={profile.theme.iconSize}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          theme: {
                            ...current.theme,
                            iconSize: event.currentTarget.value as Profile["theme"]["iconSize"]
                          }
                        }))
                      }
                    >
                      {shortcutIconSizeOptions.map((option) => (
                        <option key={option.id} value={option.id}>
                          {option.label}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="field-row">
                    <span>图标形状</span>
                    <select
                      value={profile.theme.iconShape}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          theme: {
                            ...current.theme,
                            iconShape: event.currentTarget.value as Profile["theme"]["iconShape"]
                          }
                        }))
                      }
                    >
                      {shortcutIconShapeOptions.map((option) => (
                        <option key={option.id} value={option.id}>
                          {option.label}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="field-row">
                    <span>列数</span>
                    <select
                      value={profile.theme.columns}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          theme: {
                            ...current.theme,
                            columns: Number(event.currentTarget.value) as Profile["theme"]["columns"]
                          }
                        }))
                      }
                    >
                      <option value={4}>4 列</option>
                      <option value={5}>5 列</option>
                      <option value={6}>6 列</option>
                      <option value={7}>7 列</option>
                      <option value={8}>8 列</option>
                    </select>
                  </label>
                  <label className="field-row">
                    <span>侧边栏</span>
                    <select
                      value={profile.theme.sidebarSide}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          theme: {
                            ...current.theme,
                            sidebarSide: event.currentTarget.value as Profile["theme"]["sidebarSide"]
                          }
                        }))
                      }
                  >
                    <option value="left">左侧</option>
                    <option value="right">右侧</option>
                  </select>
                </label>
                </div>
              ) : null}

              {settingsTab === "groups" ? (
                <div className="settings-section">
                  <div className="wallpaper-toolbar">
                    <button className="command" type="button" onClick={() => void createGroup()}>
                      <Plus size={18} />
                      添加分组
                    </button>
                  </div>
                  <div className="group-manager" aria-label="分组管理">
                    {sortedGroups.map((group, index) => (
                      <article key={group.id} className={group.id === activeGroup?.id ? "group-row active" : "group-row"}>
                        <button
                          type="button"
                          className="group-row-index"
                          aria-label={`切换到${group.title}`}
                          onClick={() => {
                            setActiveGroupId(group.id);
                            revealGroupSwitcher();
                          }}
                        >
                          {index + 1}
                        </button>
                        <input
                          value={group.title}
                          aria-label="分组名称"
                          onInput={(event) => void renameGroup(group.id, event.currentTarget.value)}
                        />
                        <div className="group-row-actions">
                          <button type="button" disabled={index === 0} onClick={() => void moveGroup(group.id, -1)}>
                            上移
                          </button>
                          <button
                            type="button"
                            disabled={index === sortedGroups.length - 1}
                            onClick={() => void moveGroup(group.id, 1)}
                          >
                            下移
                          </button>
                          <button
                            type="button"
                            className="danger"
                            disabled={sortedGroups.length <= 1}
                            onClick={() => void deleteGroup(group.id)}
                          >
                            删除
                          </button>
                        </div>
                      </article>
                    ))}
                  </div>
                </div>
              ) : null}

              {settingsTab === "wallpaper" ? (
                <div className="settings-section">
                  <div className="wallpaper-toolbar">
                    <button
                      className="command"
                      type="button"
                      disabled={Boolean(wallpaperAction)}
                      aria-busy={wallpaperAction === "rotate" || undefined}
                      onClick={() => void rotateWallpaper()}
                    >
                      {wallpaperAction !== "rotate" ? <Shuffle size={18} /> : null}
                      <BusyLabel busy={wallpaperAction === "rotate"} busyText="正在切换…">从已选择随机</BusyLabel>
                    </button>
                  </div>
                  <ActionStatus feedback={wallpaperFeedback} />

                  <nav className="wallpaper-source-tabs" aria-label="壁纸来源">
                    {[
                      ["official", "官方自选"],
                      ["web", "全网资源"],
                      ["local", "本地上传"],
                      ["selected", `已选择 ${profile.wallpaper.selectedIds.length}`]
                    ].map(([id, label]) => (
                      <button
                        key={id}
                        type="button"
                        className={profile.wallpaper.activeSourceTab === id ? "active" : ""}
                        aria-pressed={profile.wallpaper.activeSourceTab === id}
                        onClick={() => void setWallpaperSourceTab(id as Profile["wallpaper"]["activeSourceTab"])}
                      >
                        {label}
                      </button>
                    ))}
                  </nav>

                  {profile.wallpaper.activeSourceTab === "official" ? (
                    <nav className="wallpaper-categories" aria-label="官方壁纸分类">
                      {wallpaperCategories
                        .filter((category) => category.id !== "local")
                        .map((category) => (
                          <button
                            key={category.id}
                            type="button"
                            className={profile.wallpaper.activeCategory === category.id ? "active" : ""}
                            aria-pressed={profile.wallpaper.activeCategory === category.id}
                            onClick={() =>
                              void updateProfile((current) => ({
                                ...current,
                                wallpaper: { ...current.wallpaper, activeCategory: category.id }
                              }))
                            }
                          >
                            {category.title}
                          </button>
                        ))}
                    </nav>
                  ) : null}

                  {profile.wallpaper.activeSourceTab === "web" ? (
                    <nav className="wallpaper-categories" aria-label="全网资源分类">
                      {webWallpaperCategories.map((category) => (
                        <button
                          key={category.id}
                          type="button"
                          className={profile.wallpaper.activeCategory === category.id ? "active" : ""}
                          aria-pressed={profile.wallpaper.activeCategory === category.id}
                          onClick={() =>
                            void updateProfile((current) => ({
                              ...current,
                              wallpaper: { ...current.wallpaper, activeCategory: category.id }
                            }))
                          }
                        >
                          {category.title}
                        </button>
                      ))}
                    </nav>
                  ) : null}

                  {profile.wallpaper.activeSourceTab === "local" ? (
                    <label className={`upload-button wide-upload${wallpaperAction ? " disabled" : ""}`} aria-disabled={Boolean(wallpaperAction)}>
                      {wallpaperAction !== "upload" ? <Upload size={18} /> : null}
                      <BusyLabel busy={wallpaperAction === "upload"} busyText="正在导入图片…">
                        选择本地图片，可多选，图片只存在浏览器本地
                      </BusyLabel>
                      <input
                        type="file"
                        accept="image/*"
                        multiple
                        disabled={Boolean(wallpaperAction)}
                        onChange={(event) => {
                          void uploadWallpapers(event.currentTarget.files);
                          event.currentTarget.value = "";
                        }}
                      />
                    </label>
                  ) : null}

                  <label className="field-row">
                    <span>随机模式</span>
                    <select
                      value={profile.wallpaper.rotationMode}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          wallpaper: {
                            ...current.wallpaper,
                            rotationMode: event.currentTarget.value as Profile["wallpaper"]["rotationMode"]
                          }
                        }))
                      }
                    >
                      <option value="manual">手动</option>
                      <option value="random">随机不重复</option>
                    </select>
                  </label>

                  <label className="field-row">
                    <span>随机来源</span>
                    <select
                      value={profile.wallpaper.rotationSource}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          wallpaper: {
                            ...current.wallpaper,
                            rotationSource: event.currentTarget.value as Profile["wallpaper"]["rotationSource"]
                          }
                        }))
                      }
                    >
                      <option value="selected">已选择</option>
                      <option value="web">全网资源</option>
                    </select>
                  </label>

                  <label className="field-row">
                    <span>自动间隔</span>
                    <input
                      type="number"
                      min="1"
                      max="86400"
                      step="1"
                      value={profile.wallpaper.rotationIntervalSeconds}
                      onInput={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          wallpaper: {
                            ...current.wallpaper,
                            rotationIntervalSeconds: normalizeWallpaperIntervalSeconds(Number(event.currentTarget.value) || 60)
                          }
                        }))
                      }
                    />
                  </label>

                  <label className="field-row">
                    <span>遮罩</span>
                    <div className="range-control">
                      <input
                        type="range"
                        min="0"
                        max="0.6"
                        step="0.02"
                        value={profile.wallpaper.overlayOpacity}
                        aria-valuetext={`${Math.round(profile.wallpaper.overlayOpacity * 100)}%`}
                        onInput={(event) =>
                          void updateProfile((current) => ({
                            ...current,
                            wallpaper: {
                              ...current.wallpaper,
                              overlayOpacity: Number(event.currentTarget.value)
                            }
                          }))
                        }
                      />
                      <output>{Math.round(profile.wallpaper.overlayOpacity * 100)}%</output>
                    </div>
                  </label>

                  <label className="field-row">
                    <span>模糊</span>
                    <div className="range-control">
                      <input
                        type="range"
                        min="0"
                        max="18"
                        step="1"
                        value={profile.wallpaper.blur}
                        aria-valuetext={`${profile.wallpaper.blur} 像素`}
                        onInput={(event) =>
                          void updateProfile((current) => ({
                            ...current,
                            wallpaper: {
                              ...current.wallpaper,
                              blur: Number(event.currentTarget.value)
                            }
                          }))
                        }
                      />
                      <output>{profile.wallpaper.blur} px</output>
                    </div>
                  </label>

                  {profile.wallpaper.activeSourceTab === "official" ? (
                    <div className="wallpaper-grid">
                      {visibleBuiltinWallpapers.map((wallpaper) => (
                        <article
                          key={wallpaper.id}
                          className={getWallpaperKey(profile) === wallpaper.id ? "wallpaper-card active" : "wallpaper-card"}
                        >
                          <button
                            type="button"
                            className="wallpaper-preview"
                            style={{ backgroundImage: wallpaper.css }}
                            onClick={() => void selectWallpaper(wallpaper.id)}
                          >
                            <span>{wallpaper.title}</span>
                          </button>
                          <div className="wallpaper-actions">
                            <button
                              type="button"
                              disabled={getWallpaperKey(profile) === wallpaper.id}
                              aria-pressed={getWallpaperKey(profile) === wallpaper.id}
                              onClick={() => void selectWallpaper(wallpaper.id)}
                            >
                              {getWallpaperKey(profile) === wallpaper.id ? "当前壁纸" : "设为壁纸"}
                            </button>
                            <button
                              type="button"
                              disabled={profile.wallpaper.selectedIds.includes(wallpaper.id)}
                              aria-pressed={profile.wallpaper.selectedIds.includes(wallpaper.id)}
                              onClick={() => void addToSelected(wallpaper.id)}
                            >
                              {profile.wallpaper.selectedIds.includes(wallpaper.id) ? "已选择" : "加入已选择"}
                            </button>
                          </div>
                        </article>
                      ))}
                      {officialRemoteWallpapers.map((wallpaper) => {
                        const variant = wallpaper.variants[0];
                        if (!variant) return null;
                        const key = getRemoteWallpaperKey(wallpaper, variant.id);
                        const previewUrl = webPreviewUrls[wallpaper.id] ?? getWallpaperVariantUrl(variant);
                        return (
                          <article key={wallpaper.id} className={getWallpaperKey(profile) === key ? "wallpaper-card active" : "wallpaper-card"}>
                            <button
                              type="button"
                              className="wallpaper-preview web-preview"
                              style={{ backgroundImage: `${previewUrl ? `url("${previewUrl}"), ` : ""}${wallpaper.previewCss}` }}
                              onClick={() => void selectWallpaper(key)}
                            >
                              <span>{wallpaper.title}</span>
                            </button>
                            <div className="wallpaper-actions">
                              <button
                                type="button"
                                disabled={getWallpaperKey(profile) === key}
                                aria-pressed={getWallpaperKey(profile) === key}
                                onClick={() => void selectWallpaper(key)}
                              >{getWallpaperKey(profile) === key ? "当前壁纸" : "设为壁纸"}</button>
                              <button
                                type="button"
                                disabled={profile.wallpaper.selectedIds.includes(key)}
                                aria-pressed={profile.wallpaper.selectedIds.includes(key)}
                                onClick={() => void addToSelected(key)}
                              >{profile.wallpaper.selectedIds.includes(key) ? "已选择" : "加入已选择"}</button>
                            </div>
                          </article>
                        );
                      })}
                    </div>
                  ) : null}

                  {profile.wallpaper.activeSourceTab === "web" && !backendAuth ? (
                    <div className="locked-resource-card">
                      <h3>登录后可用全网资源</h3>
                      <p>全网资源需要从你的后端 API 拉取。未登录时仍可使用官方自选、本地上传和已选择壁纸。</p>
                      <button className="command primary" type="button" onClick={() => setSettingsTab("sync")}>
                        去登录
                      </button>
                    </div>
                  ) : null}

                  {profile.wallpaper.activeSourceTab === "web" && backendAuth ? (
                    <div className="wallpaper-grid web-grid">
                      {visibleRemoteWallpapers.map((wallpaper) => {
                        const variantId = webVariantById[wallpaper.id] ?? wallpaper.variants[0].id;
                        const variant = wallpaper.variants.find((item) => item.id === variantId) ?? wallpaper.variants[0];
                        const previewUrl = webPreviewUrls[wallpaper.id] ?? getWallpaperVariantUrl(variant);
                        const key = getRemoteWallpaperKey(wallpaper, variant.id);
                        const saving = webSavingKeys.has(key);

                        return (
                          <article
                            key={wallpaper.id}
                            className={`${getWallpaperKey(profile) === key ? "wallpaper-card active" : "wallpaper-card"}${saving ? " is-saving" : ""}`}
                            aria-busy={saving || undefined}
                          >
                            <button
                              type="button"
                              className="wallpaper-preview web-preview"
                              style={{ backgroundImage: `${previewUrl ? `url("${previewUrl}"), ` : ""}${wallpaper.previewCss}` }}
                              disabled={saving}
                              onClick={() => void saveWebWallpaperToLocal(wallpaper, variant, true)}
                            >
                              <span>{wallpaper.title}</span>
                            </button>
                            <div className="variant-tabs" aria-label={`${wallpaper.title} 分辨率`}>
                              {wallpaper.variants.map((item) => (
                                <button
                                  key={item.id}
                                  type="button"
                                  className={variant.id === item.id ? "active" : ""}
                                  onClick={() =>
                                    setWebVariantById((current) => ({
                                      ...current,
                                      [wallpaper.id]: item.id
                                    }))
                                  }
                                >
                                  {item.label}
                                </button>
                              ))}
                            </div>
                            <div className="wallpaper-meta">
                              <span>{wallpaper.provider}</span>
                              <span>{wallpaper.orientation === "landscape" ? "横屏" : "竖屏"}</span>
                            </div>
                            <div className="tag-row">
                              {wallpaper.tags.map((tag) => (
                                <span key={tag}>{tag}</span>
                              ))}
                            </div>
                            <div className="wallpaper-actions">
                              <button
                                type="button"
                                disabled={saving}
                                onClick={() => void saveWebWallpaperToLocal(wallpaper, variant, true)}
                              >
                                <BusyLabel busy={saving} busyText="保存中…">设为壁纸</BusyLabel>
                              </button>
                              <button
                                type="button"
                                disabled={saving}
                                onClick={() => void saveWebWallpaperToLocal(wallpaper, variant, false)}
                              >
                                <BusyLabel busy={saving} busyText="保存中…">加入已选择</BusyLabel>
                              </button>
                            </div>
                          </article>
                        );
                      })}
                      <div className="wallpaper-load-more">
                        {webError ? <p>{webError}</p> : null}
                        {webLoading ? <span>正在加载更多</span> : null}
                        {!webLoading && (webBackendNextPage > 0 || webNextPageUrl) ? (
                          <button type="button" onClick={() => void loadMoreWebWallpapers()}>
                            加载更多
                          </button>
                        ) : null}
                        {!webLoading && webBackendNextPage <= 0 && !webNextPageUrl && !webError ? <span>没有更多了</span> : null}
                      </div>
                    </div>
                  ) : null}

                  {profile.wallpaper.activeSourceTab === "local" ? (
                    <div className="wallpaper-grid">
                      {localWallpapers.map((wallpaper) => {
                        const key = `local:${wallpaper.assetId}`;
                        return (
                          <article
                            key={wallpaper.assetId}
                            className={getWallpaperKey(profile) === key ? "wallpaper-card active" : "wallpaper-card"}
                          >
                            <button
                              type="button"
                              className="wallpaper-preview"
                              style={{ backgroundImage: `url("${wallpaper.url}")` }}
                              onClick={() => void selectWallpaper(key)}
                            >
                              <span>{wallpaper.name}</span>
                            </button>
                            <div className="wallpaper-actions">
                              <button
                                type="button"
                                disabled={getWallpaperKey(profile) === key}
                                aria-pressed={getWallpaperKey(profile) === key}
                                onClick={() => void selectWallpaper(key)}
                              >
                                {getWallpaperKey(profile) === key ? "当前壁纸" : "设为壁纸"}
                              </button>
                              <button
                                type="button"
                                disabled={profile.wallpaper.selectedIds.includes(key)}
                                aria-pressed={profile.wallpaper.selectedIds.includes(key)}
                                onClick={() => void addToSelected(key)}
                              >
                                {profile.wallpaper.selectedIds.includes(key) ? "已选择" : "加入已选择"}
                              </button>
                            </div>
                          </article>
                        );
                      })}
                    </div>
                  ) : null}

                  {profile.wallpaper.activeSourceTab === "selected" ? (
                    <div className="wallpaper-grid">
                      {profile.wallpaper.selectedIds.map((wallpaperId) => {
                        return (
                          <article
                            key={wallpaperId}
                            className={getWallpaperKey(profile) === wallpaperId ? "wallpaper-card active" : "wallpaper-card"}
                          >
                            <button
                              type="button"
                              className="wallpaper-preview"
                              style={{ backgroundImage: getSelectedWallpaperBackground(wallpaperId) }}
                              onClick={() => void selectWallpaper(wallpaperId)}
                            >
                              <span>{getWallpaperTitle(wallpaperId)}</span>
                            </button>
                            <div className="wallpaper-actions">
                              <button type="button" onClick={() => void selectWallpaper(wallpaperId)}>
                                设为壁纸
                              </button>
                              <button type="button" onClick={() => void removeFromSelected(wallpaperId)}>
                                移除
                              </button>
                            </div>
                          </article>
                        );
                      })}
                    </div>
                  ) : null}
                </div>
              ) : null}

              {settingsTab === "search" ? (
                <div className="settings-section">
                  <label className="field-row">
                    <span>模式</span>
                    <select
                      value={profile.search.mode}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          search: { ...current.search, mode: event.currentTarget.value as Profile["search"]["mode"] }
                        }))
                      }
                    >
                      <option value="custom">自定义</option>
                      <option value="browser-default">浏览器默认</option>
                    </select>
                  </label>
                  <label className="field-row">
                    <span>引擎</span>
                    <select
                      value={profile.search.selectedEngineId}
                      disabled={profile.search.mode === "browser-default"}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          search: { ...current.search, selectedEngineId: event.currentTarget.value }
                        }))
                      }
                    >
                      {profile.search.engines.map((engine) => (
                        <option key={engine.id} value={engine.id}>
                          {engine.title}
                        </option>
                      ))}
                    </select>
                  </label>
                  <div className="engine-grid" aria-label="搜索引擎快捷选择">
                    {profile.search.engines.map((engine, index) => (
                      <button
                        key={engine.id}
                        type="button"
                        className={profile.search.selectedEngineId === engine.id ? "active" : ""}
                        aria-pressed={profile.search.selectedEngineId === engine.id}
                        disabled={profile.search.mode === "browser-default"}
                        onClick={() =>
                          void updateProfile((current) => ({
                            ...current,
                            search: { ...current.search, selectedEngineId: engine.id, mode: "custom" }
                          }))
                        }
                      >
                        <span>{index + 1}</span>
                        {engine.title}
                      </button>
                    ))}
                  </div>
                  <label className="field-row">
                    <span>打开方式</span>
                    <select
                      value={profile.search.disposition}
                      onChange={(event) =>
                        void updateProfile((current) => ({
                          ...current,
                          search: {
                            ...current.search,
                            disposition: event.currentTarget.value as Profile["search"]["disposition"]
                          }
                        }))
                      }
                    >
                      <option value="CURRENT_TAB">当前标签页</option>
                      <option value="NEW_TAB">新标签页</option>
                    </select>
                  </label>
                </div>
              ) : null}

              {settingsTab === "backup" ? (
                <div className="settings-section backup-panel">
                  <section className="backup-task bookmark-import-task" aria-labelledby="bookmark-import-title">
                    <div className="backup-copy">
                      <h3 id="bookmark-import-title">导入浏览器书签</h3>
                      <p>支持 Chrome、Microsoft Edge、夸克等浏览器导出的 HTML；一次可选择 1–10 个文件。</p>
                      <p>导入只会追加到当前链接，不会替换原有分组、搜索、壁纸或同步设置。</p>
                    </div>
                    <div className="bookmark-import-rule" role="note" aria-label="书签分组规则">
                      <strong>分组规则</strong>
                      <span>忽略浏览器自带的书签栏外层；其中直属链接归入“书签栏”，外层之外的根级链接归入“未分类”，其余保留完整文件夹路径；同一路径跨文件合并，同一分组内按链接去重。</span>
                    </div>
                    <div className="option-actions" aria-label="导入浏览器书签">
                      <label
                        className={`command primary import-command${backupAction ? " disabled" : ""}`}
                        aria-disabled={Boolean(backupAction)}
                      >
                        <BusyLabel busy={backupAction === "bookmarks"} busyText="正在检查…">
                          <Upload size={17} aria-hidden="true" />
                          选择书签 HTML
                        </BusyLabel>
                        <input
                          type="file"
                          accept=".html,.htm,text/html"
                          multiple
                          disabled={Boolean(backupAction)}
                          onChange={(event) => {
                            void importBrowserBookmarks(event.currentTarget.files);
                            event.currentTarget.value = "";
                          }}
                        />
                      </label>
                    </div>
                    <ActionStatus feedback={bookmarkImportFeedback} />
                  </section>

                  <section className="backup-task config-backup-task" aria-labelledby="config-backup-title">
                    <div className="backup-copy">
                      <h3 id="config-backup-title">配置备份</h3>
                      <p>导出/导入只包含快捷方式、分组、搜索、壁纸选择和外观设置。</p>
                      <p>本地上传图片、全网资源已缓存图片和网站图标缓存只存在当前浏览器本地，不会写进配置文件。</p>
                    </div>
                    <div className="option-actions" aria-label="配置备份">
                      <button
                        className="command primary"
                        type="button"
                        disabled={Boolean(backupAction)}
                        aria-busy={backupAction === "export" || undefined}
                        onClick={() => void exportConfig()}
                      >
                        <BusyLabel busy={backupAction === "export"} busyText="正在生成…">导出配置</BusyLabel>
                      </button>
                      <label className={`command import-command${backupAction ? " disabled" : ""}`} aria-disabled={Boolean(backupAction)}>
                        <BusyLabel busy={backupAction === "import"} busyText="正在导入…">导入配置</BusyLabel>
                        <input
                          type="file"
                          accept="application/json,.json"
                          disabled={Boolean(backupAction)}
                          onChange={(event) => {
                            void importConfig(event.currentTarget.files);
                            event.currentTarget.value = "";
                          }}
                        />
                      </label>
                      <button
                        className="command danger"
                        type="button"
                        disabled={Boolean(backupAction)}
                        aria-busy={backupAction === "reset" || undefined}
                        onClick={() => void resetLocalConfig()}
                      >
                        <BusyLabel busy={backupAction === "reset"} busyText="正在重置…">重置本地配置</BusyLabel>
                      </button>
                    </div>
                    <ActionStatus feedback={{ tone: inferFeedbackTone(backupStatus), message: backupStatus }} />
                  </section>
                </div>
              ) : null}

              {settingsTab === "sync" ? (
                <div className="settings-section sync-panel">
                  <div className="sync-copy">
                    <h3>后端同步</h3>
                    <p>登录后，本机更改会自动上传；全新浏览器首次登录会加载当时的云端配置。其他浏览器后续写入的变化，需要在这里点击“从后端加载”。</p>
                    <p>只同步快捷方式、分组、搜索、壁纸选择和外观设置；本地上传图片、全网资源已缓存图片和网站图标缓存不会上传。</p>
                  </div>

                  <div className="sync-behavior-note" role="note" aria-label="新浏览器登录后的同步规则">
                    <strong>新浏览器登录后的同步规则</strong>
                    <ul>
                      <li>新安装的 Chrome 或 Microsoft Edge 本机没有用户配置时，会自动加载账号中的云端配置。</li>
                      <li>本机和云端都有配置时，会先显示选择弹窗；关闭或取消不会覆盖任何一方。</li>
                      <li>本地上传图片和缓存仍只保留在原浏览器，需要在新浏览器重新添加。</li>
                    </ul>
                  </div>

                  <div className="sync-card">
                    <label className="sync-field">
                      <span>邮箱</span>
                      <input
                        value={backendEmail}
                        type="email"
                        autoComplete="email"
                        aria-invalid={syncFormError?.field === "email" || undefined}
                        aria-describedby={syncFormError?.field === "email" ? "sync-email-error" : undefined}
                        onInput={(event) => {
                          setBackendEmail(event.currentTarget.value);
                          setSyncFormError((current) => current?.field === "email" ? null : current);
                          setSyncAuthError("");
                        }}
                        placeholder="you@example.com"
                        disabled={Boolean(backendAuth) || syncBusy}
                      />
                      {syncFormError?.field === "email" ? (
                        <span className="sync-field-error" id="sync-email-error" role="alert">{syncFormError.message}</span>
                      ) : null}
                    </label>
                    {!backendAuth ? (
                      <label className="sync-field">
                        <span>密码</span>
                        <input
                          value={backendPassword}
                          type="password"
                          autoComplete="current-password"
                          minLength={minimumPluginPasswordLength}
                          aria-invalid={syncFormError?.field === "password" || undefined}
                          aria-describedby={syncFormError?.field === "password" ? "sync-password-help sync-password-error" : "sync-password-help"}
                          onInput={(event) => {
                            setBackendPassword(event.currentTarget.value);
                            setSyncFormError((current) => current?.field === "password" ? null : current);
                            setSyncAuthError("");
                          }}
                          placeholder={`密码至少 ${minimumPluginPasswordLength} 位`}
                          disabled={syncBusy}
                        />
                        {syncFormError?.field === "password" ? (
                          <span className="sync-field-error" id="sync-password-error" role="alert">{syncFormError.message}</span>
                        ) : null}
                      </label>
                    ) : (
                      <div className="sync-account" aria-live="polite">
                        <span>{backendAuth.readOnly ? "迁移只读" : "已登录"}</span>
                        <strong>{backendAuth.email}</strong>
                      </div>
                    )}
                    {!backendAuth ? (
                      <p className="sync-password-help" id="sync-password-help">登录和注册密码至少 {minimumPluginPasswordLength} 位。</p>
                    ) : null}
                    {syncAuthError ? <p className="sync-form-feedback" role="alert">{syncAuthError}</p> : null}
                  </div>

                  {!backendAuth ? (
                    <>
                      <div className="option-actions" aria-label="后端账号" aria-busy={syncBusy && Boolean(syncAuthAction)}>
                        <button className="command primary" type="button" disabled={syncBusy} onClick={() => void connectBackend("login")}>
                          {syncAuthAction === "login" ? "正在登录…" : "登录"}
                        </button>
                        <button className="command" type="button" disabled={syncBusy} onClick={() => void connectBackend("register")}>
                          {syncAuthAction === "register" ? "正在注册…" : "注册"}
                        </button>
                      </div>
                      <div className="option-actions" aria-label="账号验证与恢复">
                        <button className="command" type="button" disabled={syncBusy} onClick={() => void requestAccountEmail("resend-verification")}>
                          <BusyLabel busy={syncAction === "resend-email"} busyText="正在发送…">重发验证邮件</BusyLabel>
                        </button>
                        <button className="command" type="button" disabled={syncBusy} onClick={() => void requestAccountEmail("forgot-password")}>
                          <BusyLabel busy={syncAction === "forgot-password"} busyText="正在申请…">忘记密码</BusyLabel>
                        </button>
                      </div>
                    </>
                  ) : (
                    <div className="option-actions" aria-label="后端同步操作">
                      <button
                        className="command primary"
                        type="button"
                        disabled={syncBusy || !canWriteSyncSession(backendAuth)}
                        aria-busy={syncAction === "sync" || undefined}
                        onClick={() => void saveProfileToBackend()}
                      >
                        <BusyLabel busy={syncAction === "sync"} busyText="正在同步…">立即同步</BusyLabel>
                      </button>
                      <button
                        className="command"
                        type="button"
                        disabled={syncBusy}
                        aria-busy={syncAction === "load" || undefined}
                        onClick={() => void loadProfileFromBackend()}
                      >
                        <BusyLabel busy={syncAction === "load"} busyText="正在加载…">从后端加载</BusyLabel>
                      </button>
                      <button
                        className="command"
                        type="button"
                        disabled={resourceRefreshing || !canWriteSyncSession(backendAuth)}
                        aria-busy={resourceRefreshing || undefined}
                        onClick={() => void refreshBackendResources(undefined, "manual")}
                      >
                        <BusyLabel busy={resourceRefreshing} busyText="正在刷新…">刷新后端资源</BusyLabel>
                      </button>
                      {backendAuth.readOnly ? (
                        <button className="command" type="button" disabled={syncBusy} onClick={() => void requestAccountEmail("resend-verification")}>
                          <BusyLabel busy={syncAction === "resend-email"} busyText="正在发送…">重发验证邮件</BusyLabel>
                        </button>
                      ) : null}
                      <button
                        className="command danger"
                        type="button"
                        disabled={syncBusy}
                        aria-busy={syncAction === "logout" || undefined}
                        onClick={() => void disconnectBackend()}
                      >
                        <BusyLabel busy={syncAction === "logout"} busyText="正在退出…">退出登录</BusyLabel>
                      </button>
                    </div>
                  )}
                  <ActionStatus feedback={resourceFeedback} />
                  {backendAuth ? (
                    <p className="sync-hint">
                      {backendAuth.readOnly
                        ? "迁移只读会话只允许读取自己的既有配置；不会刷新令牌、提交队列或访问私有目录。验证邮箱后请退出并重新登录。"
                        : "更改会先原子写入持久队列；新标签页关闭后仍由后台 Worker 在安静期结束时同步。"}
                    </p>
                  ) : null}
                  {syncConflict ? (
                    <section className="sync-card" role="alert" aria-label="同步冲突">
                      <strong>配置冲突需要逐项选择</strong>
                      <p>
                        云端版本 {syncConflict.remoteVersion} 与本机修改重叠。系统已固定保存 base、本机和云端三份快照，选择不会静默覆盖未确认的数据。
                      </p>
                      {syncMergeResult?.conflicts.map((conflict) => (
                        <label className="sync-field" key={`${conflict.kind}:${conflict.path}`}>
                          <span><code>{conflict.path}</code> · {conflict.kind}</span>
                          <select
                            value={syncConflictChoices[conflict.path] ?? ""}
                            disabled={syncBusy || !canWriteSyncSession(backendAuth)}
                            onInput={(event) => {
                              const value = event.currentTarget.value as MergeConflictChoice | "";
                              setSyncConflictChoices((current) => {
                                if (!value) {
                                  const next = { ...current };
                                  delete next[conflict.path];
                                  return next;
                                }
                                return { ...current, [conflict.path]: value };
                              });
                            }}
                          >
                            <option value="">请选择</option>
                            <option value="local">保留本机项</option>
                            <option value="remote">采用云端项</option>
                            {conflict.canKeepBoth ? <option value="both">两者都保留（克隆新 ID）</option> : null}
                          </select>
                        </label>
                      ))}
                      {!syncMergeResult ? <p className="option-status">快照 schema 不兼容，无法自动生成逐项解决结果；请先导出 JSON。</p> : null}
                      <div className="option-actions">
                        <button
                          className="command"
                          type="button"
                          disabled={syncBusy || !syncMergeResult || !canWriteSyncSession(backendAuth)}
                          onClick={() => setSyncConflictChoices(Object.fromEntries(syncMergeResult?.conflicts.map((conflict) => [conflict.path, "local"]) ?? []))}
                        >全部选本机</button>
                        <button
                          className="command"
                          type="button"
                          disabled={syncBusy || !syncMergeResult || !canWriteSyncSession(backendAuth)}
                          onClick={() => setSyncConflictChoices(Object.fromEntries(syncMergeResult?.conflicts.map((conflict) => [conflict.path, "remote"]) ?? []))}
                        >全部选云端</button>
                        <button
                          className="command primary"
                          type="button"
                          disabled={syncBusy || !syncMergeResult || !canWriteSyncSession(backendAuth) || syncMergeResult.conflicts.some((conflict) => !syncConflictChoices[conflict.path])}
                          aria-busy={syncAction === "resolve-conflict" || undefined}
                          onClick={() => void resolveSyncConflict()}
                        ><BusyLabel busy={syncAction === "resolve-conflict"} busyText="正在提交…">提交逐项结果</BusyLabel></button>
                        <button className="command" type="button" onClick={exportSyncConflict}>导出三份 JSON</button>
                      </div>
                    </section>
                  ) : null}
                  {releaseNotice ? <p className="option-status" role="status">{releaseNotice}</p> : null}
                  {syncStatus || profile.sync.lastSyncedAt ? (
                    <ActionStatus
                      feedback={{
                        tone: inferFeedbackTone(syncStatus),
                        message: syncStatus || `上次同步：${new Date(profile.sync.lastSyncedAt || "").toLocaleString()}`
                      }}
                    />
                  ) : null}

                  <div className="sync-copy">
                    <h3>GitHub Gist 手动备份</h3>
                    <p>使用你自己的 GitHub token，把版本化 SharedProfile 备份到私有 Gist 的 {githubProfileFilename}。它不参与后端实时同步，本地图片、图标缓存和凭据不会上传。</p>
                  </div>

                  <div className="sync-card">
                    <label className="sync-field">
                      <span>Token</span>
                      <div className="sync-token-row">
                        <input
                          value={githubToken}
                          type="password"
                          autoComplete="off"
                          onInput={(event) => setGithubToken(event.currentTarget.value)}
                          placeholder="GitHub token，只需 gist 权限"
                          disabled={githubBusy}
                        />
                        <a className="token-help-link" href={githubTokenCreateUrl} target="_blank" rel="noreferrer">
                          <Github size={16} />
                          获取 Token
                        </a>
                      </div>
                    </label>
                    <label className="sync-field">
                      <span>Gist ID</span>
                      <input
                        value={githubGistId}
                        onInput={(event) => setGithubGistId(event.currentTarget.value)}
                        placeholder="留空则保存时新建私有 Gist"
                        disabled={githubBusy}
                      />
                    </label>
                    {githubAuth ? (
                      <div className="sync-account" aria-live="polite">
                        <span>已保存</span>
                        <strong>GitHub Gist</strong>
                        <code>{githubAuth.gistId}</code>
                      </div>
                    ) : null}
                  </div>

                  <div className="option-actions" aria-label="GitHub 备份操作">
                    <button
                      className="command primary"
                      type="button"
                      disabled={githubBusy}
                      aria-busy={githubAction === "backup" || undefined}
                      onClick={() => void saveProfileToGitHub()}
                    >
                      <BusyLabel busy={githubAction === "backup"} busyText="正在备份…">备份到 GitHub</BusyLabel>
                    </button>
                    <button
                      className="command"
                      type="button"
                      disabled={githubBusy}
                      aria-busy={githubAction === "load" || undefined}
                      onClick={() => void loadProfileFromGitHub()}
                    >
                      <BusyLabel busy={githubAction === "load"} busyText="正在加载…">从 GitHub 加载</BusyLabel>
                    </button>
                    <button
                      className="command danger"
                      type="button"
                      disabled={githubBusy || !githubAuth}
                      aria-busy={githubAction === "clear" || undefined}
                      onClick={() => void disconnectGitHub()}
                    >
                      <BusyLabel busy={githubAction === "clear"} busyText="正在清除…">清除 GitHub 凭据</BusyLabel>
                    </button>
                  </div>
                  <ActionStatus feedback={{ tone: inferFeedbackTone(githubStatus), message: githubStatus }} />
                </div>
              ) : null}
              </fieldset>
            </div>
          </div>
        </section>
      ) : null}

      {shortcutForm ? (
        <section className="modal-backdrop" role="dialog" aria-modal="true" aria-label="快捷方式">
          <form className="shortcut-form" onSubmit={(event) => void submitShortcut(event)}>
            <header>
              <h2>{shortcutForm.id ? "编辑图标" : "添加图标"}</h2>
              <button type="button" className="icon-button" aria-label="关闭" onClick={() => setShortcutForm(null)}>
                <X size={20} />
              </button>
            </header>
            <div className="shortcut-form-body">
              <label>
                <span>名称</span>
                <input
                  value={shortcutForm.title}
                  autoFocus
                  onInput={(event) =>
                    setShortcutForm((current) => (current ? { ...current, title: event.currentTarget.value } : current))
                  }
                  required
                />
              </label>
              <label>
                <span>地址</span>
                <input
                  value={shortcutForm.url}
                  onInput={(event) =>
                    setShortcutForm((current) => (current ? { ...current, url: event.currentTarget.value } : current))
                  }
                  required
                />
              </label>

              <fieldset className="shortcut-choice-field">
                <legend>图标</legend>
                <div className="shortcut-choice-grid icon-mode-choices">
                  {shortcutIconModeOptions.map((option) => (
                    <label
                      key={option.value}
                      className={`shortcut-choice${shortcutForm.iconMode === option.value ? " active" : ""}`}
                    >
                      <input
                        type="radio"
                        name="shortcut-icon-mode"
                        value={option.value}
                        checked={shortcutForm.iconMode === option.value}
                        onChange={() =>
                          setShortcutForm((current) =>
                            current
                              ? {
                                  ...current,
                                  iconMode: option.value,
                                  iconText: current.iconText || getShortcutInitial(current.title)
                                }
                              : current
                          )
                        }
                      />
                      <span>{option.label}</span>
                    </label>
                  ))}
                </div>
              </fieldset>

              <fieldset className="shortcut-choice-field">
                <legend>分组</legend>
                <div className="shortcut-choice-grid group-choices">
                  {sortedGroups.map((group) => (
                    <label
                      key={group.id}
                      className={`shortcut-choice${shortcutForm.groupId === group.id ? " active" : ""}`}
                    >
                      <input
                        type="radio"
                        name="shortcut-group"
                        value={group.id}
                        checked={shortcutForm.groupId === group.id}
                        onChange={() =>
                          setShortcutForm((current) => (current ? { ...current, groupId: group.id } : current))
                        }
                      />
                      <span>{group.title}</span>
                    </label>
                  ))}
                </div>
                {sortedGroups.length === 1 ? (
                  <p className="shortcut-choice-hint">当前只有一个分组；可在“设置 → 分组”中新建。</p>
                ) : null}
              </fieldset>

              {shortcutForm.iconMode === "text" ? (
                <label>
                  <span>图标文字</span>
                  <input
                    value={shortcutForm.iconText}
                    maxLength={3}
                    onInput={(event) =>
                      setShortcutForm((current) =>
                        current ? { ...current, iconText: event.currentTarget.value.toUpperCase() } : current
                      )
                    }
                  />
                </label>
              ) : null}
              {shortcutForm.iconMode === "url" ? (
                <label>
                  <span>图片链接</span>
                  <input
                    value={shortcutForm.iconUrl}
                    onInput={(event) =>
                      setShortcutForm((current) => (current ? { ...current, iconUrl: event.currentTarget.value } : current))
                    }
                    required
                  />
                </label>
              ) : null}
            </div>
            <footer className="shortcut-form-actions">
              {formError ? <p className="form-error">{formError}</p> : null}
              <button className="command primary" type="submit">
                保存
              </button>
            </footer>
          </form>
        </section>
      ) : null}
      {dialogs.node}
    </main>
  );
}
