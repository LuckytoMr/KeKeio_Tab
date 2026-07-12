import "fake-indexeddb/auto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createDefaultProfile } from "../profile/defaults";
import { ProfileStore, type ProfileInvalidation, type ProfileInvalidationBus } from "./profileStore";
import { LocalRevisionConflictError, SyncStore } from "./syncStore";

class MemoryInvalidationBus implements ProfileInvalidationBus {
  private readonly listeners = new Set<(message: ProfileInvalidation) => void>();

  publish(message: ProfileInvalidation) {
    for (const listener of this.listeners) listener(message);
  }

  subscribe(listener: (message: ProfileInvalidation) => void) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
}

describe("ProfileStore migration", () => {
  let syncStore: SyncStore;

  beforeEach(() => {
    syncStore = new SyncStore(`FullProProfileStoreTest-${crypto.randomUUID()}`);
  });

  afterEach(async () => {
    await syncStore.delete();
  });

  it("imports the existing chrome-storage profile once and makes IndexedDB authoritative", async () => {
    const legacy = createDefaultProfile();
    legacy.deviceId = "device:legacy";
    const loadLegacy = vi.fn(async () => legacy);
    const store = new ProfileStore(syncStore, loadLegacy);

    const first = await store.load();
    const second = await store.load();

    expect(first.deviceId).toBe("device:legacy");
    expect(second).toEqual(first);
    expect(loadLegacy).toHaveBeenCalledTimes(1);
    expect(await syncStore.getLocalProfile()).toMatchObject({ revision: 0, profile: first });
    expect(await syncStore.getDeviceState()).toMatchObject({ hasUserEdits: true });
  });

  it("resets shared fields without changing the device or profile identity", async () => {
    const legacy = createDefaultProfile();
    legacy.deviceId = "device:keep";
    legacy.profileId = "profile:keep";
    legacy.theme.showBrand = true;
    const store = new ProfileStore(syncStore, async () => legacy);
    await store.load();

    const reset = await store.reset();

    expect(reset.deviceId).toBe("device:keep");
    expect(reset.profileId).toBe("profile:keep");
    expect(reset.theme.showBrand).toBe(false);
  });

  it("rejects a stale tab write by revision CAS and broadcasts invalidation", async () => {
    const initial = createDefaultProfile();
    const bus = new MemoryInvalidationBus();
    const firstTab = new ProfileStore(syncStore, async () => initial, bus);
    const secondTab = new ProfileStore(syncStore, async () => undefined, bus);
    const firstSnapshot = await firstTab.load();
    const secondSnapshot = await secondTab.load();
    const invalidated = vi.fn();
    const unsubscribe = secondTab.subscribeInvalidation(invalidated);

    await firstTab.save({
      ...firstSnapshot,
      updatedAt: "2026-07-12T01:00:00.000Z",
      theme: { ...firstSnapshot.theme, showBrand: true }
    });

    expect(invalidated).toHaveBeenCalledWith(expect.objectContaining({ revision: 1 }));
    await expect(secondTab.save({
      ...secondSnapshot,
      updatedAt: "2026-07-12T02:00:00.000Z",
      theme: { ...secondSnapshot.theme, columns: 7 }
    })).rejects.toBeInstanceOf(LocalRevisionConflictError);
    expect((await syncStore.getLocalProfile())?.profile.theme).toMatchObject({ showBrand: true, columns: initial.theme.columns });

    const refreshed = await secondTab.load();
    await secondTab.save({
      ...refreshed,
      updatedAt: "2026-07-12T03:00:00.000Z",
      theme: { ...refreshed.theme, columns: 7 }
    });
    expect((await syncStore.getLocalProfile())?.revision).toBe(2);
    unsubscribe();
  });
});
