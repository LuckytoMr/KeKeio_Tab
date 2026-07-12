import { z } from "zod";

const credentialKey = "fullProWorkerCredentialsV2";

const credentialsBaseSchema = z.object({
  version: z.literal(2),
  sessionGeneration: z.string().min(1),
  firstConnectionPending: z.boolean(),
  accountScope: z.string().min(1),
  baseUrl: z.string().url(),
  userId: z.string().min(1),
  email: z.string().email(),
  accessToken: z.string().min(1),
  accessExpiresAt: z.number().int().nonnegative(),
  tokenFamily: z.string().optional(),
  updatedAt: z.number().int().nonnegative()
});

const fullCredentialsSchema = credentialsBaseSchema.extend({
  scope: z.literal("full"),
  refreshToken: z.string().min(1),
  refreshExpiresAt: z.number().int().nonnegative(),
  pendingRefresh: z.object({
    requestId: z.string().min(1),
    refreshToken: z.string().min(1),
    startedAt: z.number().int().nonnegative()
  }).strict().optional()
}).strict();

const migrationReadCredentialsSchema = credentialsBaseSchema.extend({
  scope: z.literal("migration_read")
}).strict();

const canonicalCredentialsSchema = z.discriminatedUnion("scope", [
  fullCredentialsSchema,
  migrationReadCredentialsSchema
]);

const credentialsSchema = z.preprocess((value) => {
  if (!value || typeof value !== "object" || Array.isArray(value)) return value;
  const record = value as Record<string, unknown>;
  const migrated = { ...record };
  if (migrated.scope === undefined && typeof migrated.refreshToken === "string") migrated.scope = "full";
  if (typeof migrated.sessionGeneration !== "string" || migrated.sessionGeneration.trim() === "") {
    migrated.sessionGeneration = `legacy:${String(migrated.accountScope ?? "unknown")}:${String(migrated.updatedAt ?? 0)}`;
  }
  if (typeof migrated.firstConnectionPending !== "boolean") migrated.firstConnectionPending = false;
  return migrated;
}, canonicalCredentialsSchema);

export type WorkerCredentials = z.infer<typeof canonicalCredentialsSchema>;
export type PublicWorkerSession = {
  accountScope: string;
  sessionGeneration: string;
  baseUrl: string;
  userId: string;
  email: string;
  scope: WorkerCredentials["scope"];
  readOnly: boolean;
  firstConnectionPending: boolean;
  accessExpiresAt: number;
  refreshExpiresAt?: number;
  updatedAt: number;
};

export function canWriteSyncSession(
  session?: Pick<PublicWorkerSession, "readOnly" | "firstConnectionPending"> | null
) {
  return Boolean(session && !session.readOnly && !session.firstConnectionPending);
}

export function isSameWritableSyncSession(
  expected: Pick<PublicWorkerSession, "accountScope" | "sessionGeneration" | "readOnly" | "firstConnectionPending">,
  current?: Pick<PublicWorkerSession, "accountScope" | "sessionGeneration" | "readOnly" | "firstConnectionPending"> | null
) {
  return canWriteSyncSession(expected) &&
    canWriteSyncSession(current) &&
    expected.accountScope === current?.accountScope &&
    expected.sessionGeneration === current?.sessionGeneration;
}

export interface KeyValueStorage {
  get(key: string): Promise<Record<string, unknown>>;
  set(values: Record<string, unknown>): Promise<void>;
  remove(key: string): Promise<void>;
}

export class CredentialVault {
  private mutationTail: Promise<void> = Promise.resolve();

  constructor(private readonly storage: KeyValueStorage) {}

  async save(credentials: WorkerCredentials) {
    const parsed = credentialsSchema.parse(credentials);
    await this.mutate(async () => {
      await this.storage.set({ [credentialKey]: parsed });
    });
  }

  async loadPrivate() {
    return this.loadParsed();
  }

  async replaceIfCurrent(
    expected: { sessionGeneration: string; refreshToken?: string; accessToken?: string },
    credentials: WorkerCredentials
  ) {
    const parsed = credentialsSchema.parse(credentials);
    return this.mutate(async () => {
      const current = await this.loadParsed();
      if (!current || current.sessionGeneration !== expected.sessionGeneration) return false;
      if (expected.accessToken !== undefined && current.accessToken !== expected.accessToken) return false;
      if (expected.refreshToken !== undefined && (current.scope !== "full" || current.refreshToken !== expected.refreshToken)) return false;
      await this.storage.set({
        [credentialKey]: credentialsSchema.parse({
          ...parsed,
          firstConnectionPending: current.firstConnectionPending
        })
      });
      return true;
    });
  }

  async completeFirstConnection(sessionGeneration: string) {
    return this.mutate(async () => {
      const current = await this.loadParsed();
      if (!current || current.sessionGeneration !== sessionGeneration) return false;
      if (!current.firstConnectionPending) return true;
      await this.storage.set({
        [credentialKey]: credentialsSchema.parse({ ...current, firstConnectionPending: false })
      });
      return true;
    });
  }

  async takeAndClear() {
    return this.mutate(async () => {
      const current = await this.loadParsed();
      await this.storage.remove(credentialKey);
      return current;
    });
  }

  async clearIfCurrent(sessionGeneration: string) {
    return this.mutate(async () => {
      const current = await this.loadParsed();
      if (!current || current.sessionGeneration !== sessionGeneration) return false;
      await this.storage.remove(credentialKey);
      return true;
    });
  }

  private async loadParsed() {
    const value = (await this.storage.get(credentialKey))[credentialKey];
    const parsed = credentialsSchema.safeParse(value);
    return parsed.success ? parsed.data : undefined;
  }

  private mutate<T>(operation: () => Promise<T>) {
    const pending = this.mutationTail.then(operation, operation);
    this.mutationTail = pending.then(() => undefined, () => undefined);
    return pending;
  }

  async getPublicSession(): Promise<PublicWorkerSession | undefined> {
    const credentials = await this.loadPrivate();
    if (!credentials) return undefined;
    const {
      accountScope,
      sessionGeneration,
      baseUrl,
      userId,
      email,
      scope,
      accessExpiresAt,
      updatedAt
    } = credentials;
    return {
      accountScope,
      sessionGeneration,
      baseUrl,
      userId,
      email,
      scope,
      readOnly: scope === "migration_read",
      firstConnectionPending: credentials.firstConnectionPending,
      accessExpiresAt,
      ...(credentials.scope === "full" ? { refreshExpiresAt: credentials.refreshExpiresAt } : {}),
      updatedAt
    };
  }

  async clear() {
    await this.mutate(async () => {
      await this.storage.remove(credentialKey);
    });
  }
}

export function createChromeCredentialVault() {
  return new CredentialVault(chrome.storage.local as unknown as KeyValueStorage);
}
