import { describe, expect, it } from "vitest";
import { createDefaultProfile } from "./defaults";
import { buildProfileBackupFilename, exportProfileBackup, parseProfileBackup } from "./backup";

describe("profile backup", () => {
  it("exports a versioned SharedProfileV2 envelope through the portable allowlist", () => {
    const profile = createDefaultProfile();
    profile.deviceId = "device:private";
    profile.shortcuts[0].icon = { kind: "local", assetId: "icon-secret", localOnly: true };
    profile.wallpaper.selected = { kind: "local", assetId: "wallpaper-secret", localOnly: true };
    profile.wallpaper.portableSelected = { kind: "builtin", id: "mist" };
    profile.wallpaper.selectedIds = ["local:wallpaper-secret", "mist"];
    profile.wallpaper.rotationHistory = ["local:wallpaper-secret"];
    profile.sync = {
      provider: "backend",
      status: "error",
      backendUrl: "https://sync.example.test",
      errorMessage: "private error"
    };

    const text = exportProfileBackup(profile, new Date("2026-07-08T00:00:00.000Z"));
    const parsed = JSON.parse(text);
    const serialized = JSON.stringify(parsed);

    expect(parsed).toMatchObject({
      format: "full-pro-profile-backup",
      formatVersion: 2,
      schemaVersion: 2,
      exportedAt: "2026-07-08T00:00:00.000Z",
      profile: {
        schemaVersion: 2,
        profileId: profile.profileId
      }
    });
    expect(parsed).not.toHaveProperty("version");
    expect(serialized).not.toContain("device:private");
    expect(serialized).not.toContain("icon-secret");
    expect(serialized).not.toContain("wallpaper-secret");
    expect(serialized).not.toContain("rotationHistory");
    expect(serialized).not.toContain("sync.example.test");
    expect(serialized).not.toContain("private error");
  });

  it("imports a wrapped SharedProfileV2 backup without replacing device-local state", () => {
    const profile = createDefaultProfile();
    profile.theme.iconShape = "circle";
    const local = createDefaultProfile();
    local.deviceId = "device:keep";
    local.wallpaper.rotationHistory = ["local:keep"];
    local.sync = { provider: "backend", status: "ready", backendUrl: "https://sync.example.test" };

    const imported = parseProfileBackup(exportProfileBackup(profile), local);

    expect(imported.profileId).toBe(profile.profileId);
    expect(imported.deviceId).toBe("device:keep");
    expect(imported.theme.iconShape).toBe("circle");
    expect(imported.wallpaper.rotationHistory).toEqual(["local:keep"]);
    expect(imported.sync).toEqual({
      provider: "backend",
      status: "signed-out",
      backendUrl: "https://tab.kekeio.com",
      errorMessage: "云端服务地址已升级，请重新登录。"
    });
  });

  it("imports a raw profile json for GitHub profile compatibility", () => {
    const profile = createDefaultProfile();
    const imported = parseProfileBackup(JSON.stringify(profile), createDefaultProfile());

    expect(imported.profileId).toBe(profile.profileId);
    expect(imported.shortcuts.length).toBeGreaterThan(0);
  });

  it("rejects files that are not KeKeIO Tab profile backups", () => {
    const local = createDefaultProfile();
    expect(() => parseProfileBackup("{}", local)).toThrow("不是有效的 KeKeIO Tab 配置文件");
    expect(() => parseProfileBackup("not-json", local)).toThrow("配置文件不是有效 JSON");
  });

  it("builds a stable readable backup filename", () => {
    const profile = createDefaultProfile();

    expect(buildProfileBackupFilename(profile, new Date("2026-07-08T12:34:56.000Z"))).toBe(
      `kekeio-tab-profile-${profile.profileId.slice(0, 8)}-2026-07-08.json`
    );
  });
});
