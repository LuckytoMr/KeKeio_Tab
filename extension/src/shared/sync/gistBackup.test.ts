import { describe, expect, it } from "vitest";
import { createDefaultProfile } from "../profile/defaults";
import { createGistBackupEnvelope, parseGistBackup } from "./gistBackup";

describe("versioned Gist backup envelope", () => {
  it("round-trips a hashed SharedProfileV2 envelope", async () => {
    const envelope = await createGistBackupEnvelope(createDefaultProfile(), new Date("2026-07-12T00:00:00.000Z"));

    expect(envelope).toMatchObject({
      format: "full-pro-shared-profile-backup",
      formatVersion: 2,
      schemaVersion: 2,
      exportedAt: "2026-07-12T00:00:00.000Z"
    });
    expect(envelope.canonicalProfileSha256).toMatch(/^[a-f0-9]{64}$/);
    expect(envelope).not.toHaveProperty("profileHash");
    await expect(parseGistBackup(JSON.stringify(envelope))).resolves.toEqual(envelope.profile);
  });

  it("rejects an envelope whose profile no longer matches its hash", async () => {
    const envelope = await createGistBackupEnvelope(createDefaultProfile());
    const tampered = { ...envelope, profile: { ...envelope.profile, profileId: "tampered" } };

    await expect(parseGistBackup(JSON.stringify(tampered))).rejects.toThrow(/hash/i);
  });

  it("imports a legacy raw profile only after stripping local and sync-only fields", async () => {
    const legacy = createDefaultProfile();
    legacy.deviceId = "device:secret";
    legacy.shortcuts[0].icon = { kind: "local", assetId: "icon-secret", localOnly: true };
    legacy.wallpaper.selected = { kind: "local", assetId: "wallpaper-secret", localOnly: true };
    legacy.wallpaper.selectedIds = ["local:wallpaper-secret", "mist"];
    legacy.sync = { provider: "github", status: "ready", repo: "secret-gist" };

    const imported = await parseGistBackup(JSON.stringify(legacy));
    const serialized = JSON.stringify(imported);

    expect(imported.schemaVersion).toBe(2);
    expect(serialized).not.toContain("device:secret");
    expect(serialized).not.toContain("icon-secret");
    expect(serialized).not.toContain("wallpaper-secret");
    expect(serialized).not.toContain("secret-gist");
  });
});
