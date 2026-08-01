import { syncStore } from "../shared/storage/profileStore";
import { createChromeAlarmManager, dueAlarmName, heartbeatAlarmName, shouldSuspendSyncDue } from "../shared/sync/alarmManager";
import { createChromeCredentialVault, type PublicWorkerSession } from "../shared/sync/credentialVault";
import { ApiError } from "../shared/sync/syncApi";
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

async function dispatchSyncMessage(message: SyncWorkerMessage) {
  if (message.type !== "auth:session" && message.type !== "auth:logout") {
    await dispatchWorkerMessage(syncRuntime, { type: "auth:session" });
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

chrome.runtime.onMessage.addListener((message: SyncWorkerMessage | { type?: string }, _sender, sendResponse) => {
  const messageType = message?.type;
  if (messageType === "profile:invalidation") {
    sendResponse({ ok: true });
    return false;
  }
  if (
    typeof messageType === "string" &&
    (messageType.startsWith("auth:") || messageType.startsWith("sync:") || messageType.startsWith("catalog:"))
  ) {
    const syncMessage = message as SyncWorkerMessage;
    const task = syncMessage.type === "sync:notify-change"
      ? ensureSyncAlarms().then(() => ({ queued: true }))
      : syncMessage.type === "sync:flush"
        ? drainAndReschedule()
        : dispatchSyncMessage(syncMessage).finally(() => ensureSyncAlarms());
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
  return false;
});

export {};
