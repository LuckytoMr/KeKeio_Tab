import { z } from "zod";
import { migrateProfile } from "./migrate";
import {
  sharedProfileToLocalProfile,
  sharedProfileV2Schema,
  toSharedProfileV2
} from "./sharedProfile";
import type { Profile } from "./types";

export const profileBackupFormat = "full-pro-profile-backup";

const profileBackupEnvelopeSchema = z.object({
  format: z.literal(profileBackupFormat),
  formatVersion: z.literal(2),
  schemaVersion: z.literal(2),
  exportedAt: z.string().datetime({ offset: true }),
  profile: sharedProfileV2Schema
}).strict();

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function looksLikeProfile(value: unknown): value is Profile {
  return isRecord(value) && value.schemaVersion === 1 && typeof value.profileId === "string";
}

export function exportProfileBackup(profile: Profile, exportedAt = new Date()) {
  const backup = profileBackupEnvelopeSchema.parse({
    format: profileBackupFormat,
    formatVersion: 2,
    schemaVersion: 2,
    exportedAt: exportedAt.toISOString(),
    profile: toSharedProfileV2(profile)
  });

  return `${JSON.stringify(backup, null, 2)}\n`;
}

export function parseProfileBackup(text: string, localProfile: Profile) {
  let parsed: unknown;

  try {
    parsed = JSON.parse(text);
  } catch {
    throw new Error("配置文件不是有效 JSON");
  }

  const envelope = profileBackupEnvelopeSchema.safeParse(parsed);
  if (envelope.success) {
    return sharedProfileToLocalProfile(envelope.data.profile, localProfile);
  }

  const legacyProfile = isRecord(parsed) && parsed.format === profileBackupFormat && looksLikeProfile(parsed.profile)
    ? parsed.profile
    : parsed;
  if (looksLikeProfile(legacyProfile)) {
    return sharedProfileToLocalProfile(toSharedProfileV2(migrateProfile(legacyProfile)), localProfile);
  }

  throw new Error("不是有效的 KeKeIO Tab 配置文件");
}

export function buildProfileBackupFilename(profile: Profile, exportedAt = new Date()) {
  const date = exportedAt.toISOString().slice(0, 10);
  return `kekeio-tab-profile-${profile.profileId.slice(0, 8)}-${date}.json`;
}
