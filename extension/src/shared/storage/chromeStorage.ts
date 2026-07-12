import type { Profile } from "../profile/types";
import {
  ProfileStore,
  syncStore,
  type ProfileInvalidation,
  type ProfileInvalidationBus
} from "./profileStore";

const PROFILE_KEY = "fullProProfile";

function hasChromeStorage() {
  return typeof chrome !== "undefined" && Boolean(chrome.storage?.local);
}

async function loadLegacyProfile(): Promise<Profile | undefined> {
  if (hasChromeStorage()) {
    const result = await chrome.storage.local.get(PROFILE_KEY);
    return result[PROFILE_KEY] as Profile | undefined;
  }

  const raw = globalThis.localStorage?.getItem(PROFILE_KEY);
  return raw ? (JSON.parse(raw) as Profile) : undefined;
}

function isProfileInvalidation(value: unknown): value is ProfileInvalidation {
  if (!value || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return record.type === "profile-invalidated" &&
    typeof record.profileId === "string" &&
    typeof record.revision === "number" &&
    typeof record.sourceId === "string";
}

function createBrowserInvalidationBus(): ProfileInvalidationBus {
  const listeners = new Set<(message: ProfileInvalidation) => void>();
  const dispatch = (message: unknown) => {
    if (!isProfileInvalidation(message)) return;
    for (const listener of listeners) listener(message);
  };
  const channel = typeof BroadcastChannel === "function"
    ? new BroadcastChannel("full-pro-profile-invalidation-v2")
    : undefined;
  channel?.addEventListener("message", (event) => dispatch(event.data));
  if (typeof chrome !== "undefined" && chrome.runtime?.onMessage) {
    chrome.runtime.onMessage.addListener((message: unknown) => {
      const record = message && typeof message === "object" ? message as Record<string, unknown> : undefined;
      if (record?.type === "profile:invalidation") dispatch(record.payload);
      return false;
    });
  }
  return {
    publish(message) {
      channel?.postMessage(message);
      if (typeof chrome !== "undefined" && chrome.runtime?.sendMessage) {
        void chrome.runtime.sendMessage({ type: "profile:invalidation", payload: message }).catch(() => undefined);
      }
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    }
  };
}

const profileStore = new ProfileStore(syncStore, loadLegacyProfile, createBrowserInvalidationBus());

export function loadProfile(): Promise<Profile> {
  return profileStore.load();
}

export async function saveProfile(profile: Profile) {
  await profileStore.save(profile);
}

export async function clearProfile() {
  await profileStore.reset();
}

export function subscribeProfileInvalidation(listener: (message: ProfileInvalidation) => void) {
  return profileStore.subscribeInvalidation(listener);
}
