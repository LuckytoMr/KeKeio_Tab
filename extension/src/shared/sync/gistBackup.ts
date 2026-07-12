import { z } from "zod";
import { migrateProfile } from "../profile/migrate";
import { parseSharedProfileV2, sharedProfileV2Schema, toSharedProfileV2, type SharedProfileV2 } from "../profile/sharedProfile";
import type { Profile } from "../profile/types";

export const gistBackupFormat = "full-pro-shared-profile-backup";

const envelopeSchema = z.object({
  format: z.literal(gistBackupFormat),
  formatVersion: z.literal(2),
  schemaVersion: z.literal(2),
  exportedAt: z.string().datetime({ offset: true }),
  canonicalProfileSha256: z.string().regex(/^[a-f0-9]{64}$/),
  profile: sharedProfileV2Schema
}).strict();

export type GistBackupEnvelope = z.infer<typeof envelopeSchema>;

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, entry]) => [key, canonicalize(entry)])
  );
}

export function canonicalJson(value: unknown) {
  return JSON.stringify(canonicalize(value));
}

export async function sha256Hex(value: string) {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export async function createGistBackupEnvelope(profile: Profile, exportedAt = new Date()): Promise<GistBackupEnvelope> {
  const shared = toSharedProfileV2(profile);
  return envelopeSchema.parse({
    format: gistBackupFormat,
    formatVersion: 2,
    schemaVersion: 2,
    exportedAt: exportedAt.toISOString(),
    canonicalProfileSha256: await sha256Hex(canonicalJson(shared)),
    profile: shared
  });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function parseLegacyProfile(value: unknown): SharedProfileV2 | undefined {
  const candidate = isRecord(value) && isRecord(value.profile) ? value.profile : value;
  if (!isRecord(candidate) || candidate.schemaVersion !== 1) return undefined;
  return toSharedProfileV2(migrateProfile(candidate as Profile));
}

export async function parseGistBackup(text: string): Promise<SharedProfileV2> {
  let value: unknown;
  try {
    value = JSON.parse(text) as unknown;
  } catch {
    throw new Error("Gist backup is not valid JSON");
  }

  const envelope = envelopeSchema.safeParse(value);
  if (envelope.success) {
    const expected = await sha256Hex(canonicalJson(envelope.data.profile));
    if (expected !== envelope.data.canonicalProfileSha256) throw new Error("Gist backup hash mismatch");
    return parseSharedProfileV2(envelope.data.profile);
  }

  const legacy = parseLegacyProfile(value);
  if (legacy) return legacy;
  throw new Error("Unsupported Gist backup format");
}
