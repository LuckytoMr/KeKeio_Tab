import { syncStore } from "../shared/storage/profileStore";
import { createChromeAlarmManager, dueAlarmName, heartbeatAlarmName, shouldSuspendSyncDue } from "../shared/sync/alarmManager";
import { createChromeCredentialVault, type PublicWorkerSession } from "../shared/sync/credentialVault";
import { ApiError, backendHostPermissionOrigin } from "../shared/sync/syncApi";
import { fixedBackendUrl } from "../shared/sync/backendEndpoint";
import { dispatchWorkerMessage, type SyncWorkerMessage } from "../shared/sync/workerProtocol";
import { SyncWorkerRuntime } from "../shared/sync/workerRuntime";

const syncRuntime = new SyncWorkerRuntime(syncStore, createChromeCredentialVault());
const syncAlarms = createChromeAlarmManager();

async function ensureSyncAlarms() {
  const [metadata, session] = await Promise.all([
    syncStore.getActiveMetadata(),
    dispatchWorkerMessage(syncRuntime, { type: "auth:session" }) as Promise<PublicWorkerSession | undefined>
  ]);
  const suspendDue = shouldSuspendSyncDue(session);
  const nextDueAt = metadata && !suspendDue
    ? await syncStore.getNextWakeAt(metadata.accountScope, metadata.profileId)
    : undefined;
  await syncAlarms.ensure(nextDueAt, { suspendDue });
}

async function drainAndReschedule() {
  await dispatchWorkerMessage(syncRuntime, { type: "auth:session" });
  const outcome = await syncRuntime.drain();
  await ensureSyncAlarms();
  return outcome;
}

async function notifyProfileInvalidation() {
  const local = await syncStore.getLocalProfile();
  if (!local) return;
  await chrome.runtime.sendMessage({
    type: "profile:invalidation",
    payload: {
      type: "profile-invalidated",
      profileId: local.profile.profileId,
      revision: local.revision,
      sourceId: "service-worker"
    }
  }).catch(() => undefined);
}

async function dispatchAuthorizedSyncMessage(message: SyncWorkerMessage) {
  if (message.type !== "auth:session" && message.type !== "auth:logout") {
    await dispatchWorkerMessage(syncRuntime, { type: "auth:session" });
  }
  if (message.type === "catalog:get" && message.kind === "bootstrap") {
    const origin = backendHostPermissionOrigin(fixedBackendUrl);
    if (!(await chrome.permissions.contains({ origins: [origin] }))) {
      throw new ApiError("需要允许访问 KeKeIO Tab 云端服务", 403, "HOST_PERMISSION_REQUIRED");
    }
  }
  return dispatchWorkerMessage(syncRuntime, message);
}

void ensureSyncAlarms();
chrome.runtime.onInstalled.addListener(() => void ensureSyncAlarms());
chrome.runtime.onStartup.addListener(() => void ensureSyncAlarms());
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name !== heartbeatAlarmName && alarm.name !== dueAlarmName) return;
  void drainAndReschedule();
});

const UHDPAPER_REFERER = "https://www.uhdpaper.com/";

type ResourceRuntimeRequest =
  | {
      type: "uhdpaper:fetch-page";
      url: string;
    }
  | {
      type: "uhdpaper:fetch-image";
      url: string;
    }
  | {
      type: "shortcut-icon:fetch-page";
      url: string;
    }
  | {
      type: "shortcut-icon:fetch-image";
      url: string;
    };

const MAX_SHORTCUT_ICON_BYTES = 2 * 1024 * 1024;

function assertUhdpaperPageUrl(rawUrl: string) {
  const url = new URL(rawUrl);
  if (url.protocol !== "https:" || url.hostname !== "www.uhdpaper.com") {
    throw new Error("只允许加载 UHDpaper 页面");
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
    throw new Error("只允许加载 UHDpaper 图片资源");
  }
  return url.href;
}

function assertHttpUrl(rawUrl: string) {
  const url = new URL(rawUrl);
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("只允许加载 HTTP/HTTPS 资源");
  }
  return url.href;
}

function arrayBufferToDataUrl(buffer: ArrayBuffer, mimeType: string) {
  const bytes = new Uint8Array(buffer);
  const chunkSize = 0x8000;
  let binary = "";

  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunkSize));
  }

  return `data:${mimeType};base64,${btoa(binary)}`;
}

async function fetchUhdpaperPage(rawUrl: string) {
  const url = assertUhdpaperPageUrl(rawUrl);
  const response = await fetch(url, {
    credentials: "omit",
    referrer: UHDPAPER_REFERER
  });

  if (!response.ok) throw new Error(`UHDpaper 页面加载失败：${response.status}`);
  return { html: await response.text() };
}

async function fetchUhdpaperImage(rawUrl: string) {
  const url = assertUhdpaperImageUrl(rawUrl);
  const response = await fetch(url, {
    credentials: "omit",
    referrer: UHDPAPER_REFERER
  });
  const mimeType = response.headers.get("content-type") ?? "application/octet-stream";

  if (!response.ok) throw new Error(`UHDpaper 图片加载失败：${response.status}`);
  if (!mimeType.startsWith("image/")) throw new Error("UHDpaper 返回的不是图片");

  return {
    mimeType,
    dataUrl: arrayBufferToDataUrl(await response.arrayBuffer(), mimeType)
  };
}

async function fetchShortcutIconPage(rawUrl: string) {
  const url = assertHttpUrl(rawUrl);
  const response = await fetch(url, {
    credentials: "omit",
    redirect: "follow",
    referrerPolicy: "no-referrer"
  });

  if (!response.ok) throw new Error(`图标页面加载失败：${response.status}`);
  return { html: await response.text() };
}

async function fetchShortcutIconImage(rawUrl: string) {
  const url = assertHttpUrl(rawUrl);
  const response = await fetch(url, {
    credentials: "omit",
    redirect: "follow",
    referrerPolicy: "no-referrer"
  });
  const mimeType = response.headers.get("content-type") ?? "application/octet-stream";

  if (!response.ok) throw new Error(`图标图片加载失败：${response.status}`);
  if (!mimeType.startsWith("image/")) throw new Error("图标地址返回的不是图片");

  const buffer = await response.arrayBuffer();
  if (buffer.byteLength > MAX_SHORTCUT_ICON_BYTES) throw new Error("图标图片过大");

  return {
    mimeType,
    dataUrl: arrayBufferToDataUrl(buffer, mimeType)
  };
}

chrome.runtime.onMessage.addListener((message: ResourceRuntimeRequest | SyncWorkerMessage, _sender, sendResponse) => {
  if ((message as { type?: string })?.type === "profile:invalidation") {
    sendResponse({ ok: true });
    return false;
  }
  if (
    typeof message?.type === "string" &&
    (message.type.startsWith("auth:") || message.type.startsWith("sync:") || message.type.startsWith("catalog:"))
  ) {
    const syncMessage = message as SyncWorkerMessage;
    const task = syncMessage.type === "sync:notify-change"
      ? ensureSyncAlarms().then(() => ({ queued: true }))
      : syncMessage.type === "sync:flush"
        ? drainAndReschedule()
        : dispatchAuthorizedSyncMessage(syncMessage).finally(() => ensureSyncAlarms());
    task
      .then(async (data) => {
        if (
          (syncMessage.type === "sync:complete-first-connection" && syncMessage.strategy === "use-remote") ||
          syncMessage.type === "sync:resolve-conflict"
        ) {
          await notifyProfileInvalidation();
        }
        return data;
      })
      .then((data) => sendResponse({ ok: true, data }))
      .catch((error: unknown) => sendResponse({
        ok: false,
        code: error instanceof ApiError ? error.code : "WORKER_ERROR",
        error: error instanceof Error ? error.message : "后台同步失败"
      }));
    return true;
  }

  const resourceMessage = message as ResourceRuntimeRequest;
  const task =
    resourceMessage?.type === "uhdpaper:fetch-page"
      ? fetchUhdpaperPage(resourceMessage.url)
      : resourceMessage?.type === "uhdpaper:fetch-image"
        ? fetchUhdpaperImage(resourceMessage.url)
        : resourceMessage?.type === "shortcut-icon:fetch-page"
          ? fetchShortcutIconPage(resourceMessage.url)
          : resourceMessage?.type === "shortcut-icon:fetch-image"
            ? fetchShortcutIconImage(resourceMessage.url)
            : Promise.reject(new Error("未知后台请求"));

  task
    .then((data) => sendResponse({ ok: true, data }))
    .catch((error: unknown) => {
      sendResponse({
        ok: false,
        error: error instanceof Error ? error.message : "后台请求失败"
      });
    });

  return true;
});

export {};
