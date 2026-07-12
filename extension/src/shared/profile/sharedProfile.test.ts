import { describe, expect, it } from "vitest";
import { createDefaultProfile } from "./defaults";
import {
  parseSharedProfileV2,
  sharedProfileToLocalProfile,
  toSharedProfileV2
} from "./sharedProfile";

describe("SharedProfileV2", () => {
  it("serializes through an allowlist and replaces device-local assets with portable fallbacks", () => {
    const profile = createDefaultProfile();
    profile.deviceId = "device:private";
    profile.shortcuts[0].icon = { kind: "local", assetId: "icon-secret", localOnly: true };
    profile.wallpaper.portableSelected = { kind: "remote", id: "remote", variantId: "4k" };
    profile.wallpaper.selected = { kind: "local", assetId: "wallpaper-secret", localOnly: true };
    profile.wallpaper.selectedIds = ["local:wallpaper-secret", "mist", "web:remote:4k"];
    profile.wallpaper.rotationHistory = ["local:wallpaper-secret"];
    profile.sync = {
      provider: "backend",
      status: "error",
      backendUrl: "https://sync.example.test",
      errorMessage: "private error",
      lastSyncAttemptAt: "2026-07-12T00:00:00.000Z"
    };

    const shared = toSharedProfileV2(profile);
    const serialized = JSON.stringify(shared);

    expect(shared.schemaVersion).toBe(2);
    expect(shared.shortcuts[0].icon).toEqual({ kind: "text", text: "Y" });
    expect(shared.wallpaper.selected).toEqual({ kind: "remote", id: "remote", variantId: "4k" });
    expect(shared.wallpaper.selectedIds).toEqual(["mist", "web:remote:4k"]);
    expect(serialized).not.toContain("device:private");
    expect(serialized).not.toContain("icon-secret");
    expect(serialized).not.toContain("wallpaper-secret");
    expect(serialized).not.toContain("sync.example.test");
    expect(serialized).not.toContain("private error");
    expect(serialized).not.toContain("rotationHistory");
  });

  it("rejects unknown fields instead of preserving them", () => {
    const valid = toSharedProfileV2(createDefaultProfile());

    expect(() => parseSharedProfileV2({ ...valid, deviceId: "device:leak" })).toThrow(/SharedProfileV2/);
    expect(() =>
      parseSharedProfileV2({
        ...valid,
        wallpaper: { ...valid.wallpaper, selected: { kind: "local", assetId: "secret", localOnly: true } }
      })
    ).toThrow(/SharedProfileV2/);
  });

  it("matches backend identity and reference invariants", () => {
    const valid = toSharedProfileV2(createDefaultProfile());
    const duplicateGroup = { ...valid, groups: [...valid.groups, { ...valid.groups[0] }] };
    const duplicateShortcut = { ...valid, shortcuts: [...valid.shortcuts, { ...valid.shortcuts[0] }] };
    const unknownGroup = {
      ...valid,
      shortcuts: valid.shortcuts.map((shortcut, index) => index === 0 ? { ...shortcut, groupId: "missing-group" } : shortcut)
    };
    const duplicateEngine = {
      ...valid,
      search: { ...valid.search, engines: [...valid.search.engines, { ...valid.search.engines[0] }] }
    };
    const missingCustomEngine = {
      ...valid,
      search: { ...valid.search, mode: "custom" as const, selectedEngineId: "missing-engine" }
    };
    const localWallpaperID = {
      ...valid,
      wallpaper: { ...valid.wallpaper, selectedIds: [...valid.wallpaper.selectedIds, "local:private"] }
    };

    for (const invalid of [duplicateGroup, duplicateShortcut, unknownGroup, duplicateEngine, missingCustomEngine, localWallpaperID]) {
      expect(() => parseSharedProfileV2(invalid)).toThrow(/SharedProfileV2/);
    }

    expect(() => parseSharedProfileV2({
      ...valid,
      search: { ...valid.search, mode: "browser-default", selectedEngineId: "browser-owned-engine" }
    })).not.toThrow();
  });

  it("rejects explicit empty deletedAt values and omits legacy empty local values", () => {
    const valid = toSharedProfileV2(createDefaultProfile());
    expect(() => parseSharedProfileV2({
      ...valid,
      groups: valid.groups.map((group, index) => index === 0 ? { ...group, deletedAt: "" } : group)
    })).toThrow(/SharedProfileV2/);

    const local = createDefaultProfile();
    local.groups[0].deletedAt = "";
    expect(toSharedProfileV2(local).groups[0]).not.toHaveProperty("deletedAt");
  });

  it("rejects insecure HTTP remote icon descriptors", () => {
    const valid = toSharedProfileV2(createDefaultProfile());
    expect(() => parseSharedProfileV2({
      ...valid,
      shortcuts: valid.shortcuts.map((shortcut, index) => index === 0
        ? { ...shortcut, icon: { kind: "url" as const, url: "http://icons.example.test/icon.png", fallbackText: "I" } }
        : shortcut)
    })).toThrow(/SharedProfileV2/);
  });

  it("hydrates shared data without replacing the local device identity and runtime state", () => {
    const local = createDefaultProfile();
    local.deviceId = "device:keep";
    local.wallpaper.rotationHistory = ["mist"];
    local.sync = { provider: "backend", status: "ready", backendUrl: "https://tab.kekeio.com" };
    const shared = toSharedProfileV2(createDefaultProfile());

    const hydrated = sharedProfileToLocalProfile(shared, local);

    expect(hydrated.deviceId).toBe("device:keep");
    expect(hydrated.sync).toEqual(local.sync);
    expect(hydrated.wallpaper.rotationHistory).toEqual(["mist"]);
    expect(hydrated.schemaVersion).toBe(1);
  });
});
