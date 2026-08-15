import { parseSharedProfileV2, type SharedProfileV2 } from "../profile/sharedProfile";
import type { SyncStore } from "../storage/syncStore";
import { createAccountScope } from "../storage/syncStore";
import { CredentialVault, type WorkerCredentials } from "./credentialVault";
import {
  ApiError,
  canonicalBackendBaseUrl,
  SyncApiClient,
  type PopulatedProfileRecordResponse,
  type ProfileConflictDetails,
  type ProfileRecordResponse,
  type PutProfileRequest,
  type TokenResponse
} from "./syncApi";

export type FirstConnectionState = "both-empty" | "local-only" | "remote-only" | "both-have-data";

export function classifyFirstConnection(localHasUserEdits: boolean, remoteHasProfile: boolean): FirstConnectionState {
  if (localHasUserEdits && remoteHasProfile) return "both-have-data";
  if (localHasUserEdits) return "local-only";
  if (remoteHasProfile) return "remote-only";
  return "both-empty";
}

export interface WorkerApi {
  login(input: { email: string; password: string; deviceId: string }): Promise<TokenResponse>;
  register(input: { email: string; password: string }): Promise<unknown>;
  resendVerification(email: string): Promise<unknown>;
  forgotPassword(email: string): Promise<unknown>;
  refresh(refreshToken: string, requestId: string): Promise<TokenResponse>;
  logout(accessToken: string, refreshToken?: string): Promise<unknown>;
  getProfile(accessToken: string): Promise<ProfileRecordResponse>;
  putProfile(accessToken: string, input: PutProfileRequest): Promise<PopulatedProfileRecordResponse>;
  bootstrap(): Promise<unknown>;
  listOfficialWallpapers(accessToken: string): Promise<unknown>;
  listWebWallpapers(accessToken: string, query?: string): Promise<unknown>;
  listStyles(accessToken: string): Promise<unknown>;
  fetchUhdpaperPage(accessToken: string, url: string): Promise<unknown>;
  fetchUhdpaperImage(accessToken: string, url: string): Promise<unknown>;
}

export type WorkerApiFactory = (baseUrl: string) => WorkerApi;

type AuthenticatedOptions = {
  requireFull?: boolean;
  requireReady?: boolean;
};

export type ExpectedSyncSession = {
  expectedAccountScope: string;
  expectedSessionGeneration: string;
};

function timestamp(value: string) {
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) throw new Error("AUTH_TOKEN_EXPIRY_INVALID");
  return parsed;
}

export class SyncWorkerRuntime {
  private readonly refreshFlights = new Map<string, Promise<WorkerCredentials>>();
  private sessionEpoch = 0;

  constructor(
    private readonly store: SyncStore,
    private readonly vault: CredentialVault,
    private readonly apiFactory: WorkerApiFactory = (baseUrl) => new SyncApiClient(baseUrl),
    private readonly now: () => number = () => Date.now()
  ) {}

  private sessionChanged() {
    return new ApiError("Authentication session changed while the operation was running", 409, "AUTH_SESSION_CHANGED");
  }

  private assertSessionEpoch(expectedEpoch: number) {
    if (this.sessionEpoch !== expectedEpoch) throw this.sessionChanged();
  }

  private generationMatches(actual: string | undefined, expected: string) {
    return actual === expected || (actual === undefined && expected.startsWith("legacy:"));
  }

  private validateAuthenticatedCredentials(
    credentials: WorkerCredentials | undefined,
    accountScope: string,
    expectedGeneration: string,
    options: AuthenticatedOptions
  ) {
    if (!credentials || credentials.accountScope !== accountScope || credentials.sessionGeneration !== expectedGeneration) {
      throw this.sessionChanged();
    }
    if (options.requireFull && credentials.scope !== "full") {
      throw new ApiError("Migration sessions cannot perform this operation", 403, "MIGRATION_READ_ONLY");
    }
    if (options.requireReady && credentials.firstConnectionPending) {
      throw new ApiError("Choose the first-connection strategy before syncing", 409, "FIRST_CONNECTION_PENDING");
    }
    return credentials;
  }

  private async assertAuthenticatedSession(
    accountScope: string,
    expectedGeneration: string,
    options: AuthenticatedOptions
  ) {
    return this.validateAuthenticatedCredentials(
      await this.vault.loadPrivate(),
      accountScope,
      expectedGeneration,
      options
    );
  }

  private async hasActiveSyncContext(
    expected: WorkerCredentials,
    profileId: string,
    options: { requireReady?: boolean } = {}
  ) {
    const [current, metadata] = await Promise.all([
      this.vault.loadPrivate(),
      this.store.getActiveMetadata()
    ]);
    if (!current || current.accountScope !== expected.accountScope || current.sessionGeneration !== expected.sessionGeneration) {
      return false;
    }
    if (options.requireReady && (current.scope !== "full" || current.firstConnectionPending)) return false;
    return Boolean(
      metadata &&
      metadata.active === 1 &&
      metadata.accountScope === expected.accountScope &&
      metadata.profileId === profileId &&
      this.generationMatches(metadata.sessionGeneration, expected.sessionGeneration)
    );
  }

  private async refresh(credentials: WorkerCredentials, force = false) {
    if (!force && credentials.accessExpiresAt > this.now() + 30_000) return credentials;
    if (credentials.scope === "migration_read") {
      throw new ApiError("Migration read-only session expired; verify the email and sign in again", 401, "MIGRATION_READ_SESSION_EXPIRED");
    }
    const flightKey = `${credentials.accountScope}:${credentials.sessionGeneration}`;
    const existing = this.refreshFlights.get(flightKey);
    if (existing) return existing;
    if (credentials.refreshExpiresAt <= this.now()) throw new ApiError("Refresh token expired", 401, "REFRESH_EXPIRED");

    const flight = (async () => {
      const pendingRefresh = credentials.pendingRefresh?.refreshToken === credentials.refreshToken
        ? credentials.pendingRefresh
        : {
            requestId: `refresh_${crypto.randomUUID()}`,
            refreshToken: credentials.refreshToken,
            startedAt: this.now()
          };
      const prepared: WorkerCredentials = { ...credentials, pendingRefresh };
      const preparedStored = await this.vault.replaceIfCurrent(
        { sessionGeneration: credentials.sessionGeneration, refreshToken: credentials.refreshToken },
        prepared
      );
      if (!preparedStored) throw this.sessionChanged();
      const response = await this.apiFactory(credentials.baseUrl).refresh(credentials.refreshToken, pendingRefresh.requestId);
      if (response.user.id !== credentials.userId) throw new Error("AUTH_REFRESH_SUBJECT_MISMATCH");
      if (response.scope !== "full") throw new Error("AUTH_REFRESH_SCOPE_MISMATCH");
      const { pendingRefresh: _acknowledged, ...acknowledgedCredentials } = prepared;
      const updated: WorkerCredentials = {
        ...acknowledgedCredentials,
        accessToken: response.accessToken,
        accessExpiresAt: timestamp(response.accessExpiresAt),
        refreshToken: response.refreshToken,
        refreshExpiresAt: timestamp(response.refreshExpiresAt),
        tokenFamily: response.tokenFamily,
        updatedAt: this.now()
      };
      const updatedStored = await this.vault.replaceIfCurrent(
        { sessionGeneration: credentials.sessionGeneration, refreshToken: credentials.refreshToken },
        updated
      );
      if (!updatedStored) throw this.sessionChanged();
      return updated;
    })().finally(() => {
      this.refreshFlights.delete(flightKey);
    });
    this.refreshFlights.set(flightKey, flight);
    return flight;
  }

  async authenticated<T>(
    accountScope: string,
    expectedGeneration: string,
    operation: (accessToken: string, api: WorkerApi) => Promise<T>,
    options: AuthenticatedOptions = {}
  ) {
    let credentials = this.validateAuthenticatedCredentials(
      await this.vault.loadPrivate(),
      accountScope,
      expectedGeneration,
      options
    );
    credentials = await this.refresh(credentials);
    this.validateAuthenticatedCredentials(credentials, accountScope, expectedGeneration, options);
    const api = this.apiFactory(credentials.baseUrl);
    try {
      const result = await operation(credentials.accessToken, api);
      await this.assertAuthenticatedSession(accountScope, expectedGeneration, options);
      return result;
    } catch (error) {
      if (!(error instanceof ApiError) || error.status !== 401) throw error;
      const latest = await this.assertAuthenticatedSession(accountScope, expectedGeneration, options);
      credentials = latest.accessToken === credentials.accessToken
        ? await this.refresh(latest, true)
        : await this.refresh(latest);
      this.validateAuthenticatedCredentials(credentials, accountScope, expectedGeneration, options);
      const result = await operation(credentials.accessToken, this.apiFactory(credentials.baseUrl));
      await this.assertAuthenticatedSession(accountScope, expectedGeneration, options);
      return result;
    }
  }

  async login(input: { baseUrl: string; email: string; password: string }) {
    const operationEpoch = ++this.sessionEpoch;
    const local = await this.store.getLocalProfile();
    const device = await this.store.getDeviceState();
    this.assertSessionEpoch(operationEpoch);
    if (!local || !device) throw new Error("LOCAL_PROFILE_NOT_INITIALIZED");
    const baseUrl = canonicalBackendBaseUrl(input.baseUrl);
    const api = this.apiFactory(baseUrl);
    const tokens = await api.login({ email: input.email.trim(), password: input.password, deviceId: device.deviceId });
    this.assertSessionEpoch(operationEpoch);
    const accountScope = await createAccountScope(baseUrl, tokens.user.id);
    this.assertSessionEpoch(operationEpoch);
    const sessionGeneration = `session_${crypto.randomUUID()}`;
    const credentialBase = {
      version: 2 as const,
      sessionGeneration,
      firstConnectionPending: true,
      accountScope,
      baseUrl,
      userId: tokens.user.id,
      email: tokens.user.email,
      accessToken: tokens.accessToken,
      accessExpiresAt: timestamp(tokens.accessExpiresAt),
      ...(tokens.tokenFamily ? { tokenFamily: tokens.tokenFamily } : {}),
      updatedAt: this.now()
    };
    const credentials: WorkerCredentials = tokens.scope === "full"
      ? {
          ...credentialBase,
          scope: "full",
          refreshToken: tokens.refreshToken,
          refreshExpiresAt: timestamp(tokens.refreshExpiresAt)
        }
      : { ...credentialBase, scope: "migration_read" };
    await this.vault.save(credentials);
    try {
      this.assertSessionEpoch(operationEpoch);
    } catch (error) {
      await this.vault.clearIfCurrent(sessionGeneration);
      throw error;
    }
    let remote: ProfileRecordResponse;
    try {
      remote = await api.getProfile(credentials.accessToken);
      this.assertSessionEpoch(operationEpoch);
    } catch (error) {
      await this.vault.clearIfCurrent(sessionGeneration);
      throw error;
    }
    const baseSnapshot = remote.profile ?? local.sharedSnapshot;
    await this.store.activateAccount({
      accountScope,
      sessionGeneration,
      baseUrl: credentials.baseUrl,
      userId: credentials.userId,
      profileId: local.profile.profileId,
      baseVersion: remote.version,
      ...(remote.profile ? { baseHash: remote.profileHash } : {}),
      baseSnapshot
    });
    try {
      this.assertSessionEpoch(operationEpoch);
    } catch (error) {
      await this.store.deactivateAccount(accountScope, local.profile.profileId, sessionGeneration);
      await this.vault.clearIfCurrent(sessionGeneration);
      throw error;
    }
    return {
      session: await this.vault.getPublicSession(),
      firstConnection: classifyFirstConnection(device.hasUserEdits, Boolean(remote.profile)),
      remote,
      local: local.sharedSnapshot
    };
  }

  register(input: { baseUrl: string; email: string; password: string }) {
    return this.apiFactory(input.baseUrl).register({ email: input.email.trim(), password: input.password });
  }

  resendVerification(input: { baseUrl: string; email: string }) {
    return this.apiFactory(input.baseUrl).resendVerification(input.email.trim());
  }

  forgotPassword(input: { baseUrl: string; email: string }) {
    return this.apiFactory(input.baseUrl).forgotPassword(input.email.trim());
  }

  async logout(expectedSession: ExpectedSyncSession) {
    const operationEpoch = this.sessionEpoch;
    const credentials = this.validateAuthenticatedCredentials(
      await this.vault.loadPrivate(),
      expectedSession.expectedAccountScope,
      expectedSession.expectedSessionGeneration,
      {}
    );
    if (!(await this.vault.clearIfCurrent(credentials.sessionGeneration))) throw this.sessionChanged();
    // 若登录在凭据清除后启动，它拥有更高的 epoch，应由较新的登录继续接管会话。
    if (this.sessionEpoch === operationEpoch) ++this.sessionEpoch;
    const metadata = await this.store.getActiveMetadata();
    if (metadata?.accountScope === credentials.accountScope) {
      await this.store.deactivateAccount(credentials.accountScope, metadata.profileId, credentials.sessionGeneration);
    }
    await this.apiFactory(credentials.baseUrl)
      .logout(credentials.accessToken, credentials.scope === "full" ? credentials.refreshToken : undefined)
      .catch(() => undefined);
  }

  getSession() {
    return this.vault.getPublicSession();
  }

  async completeFirstConnection(strategy: "use-local" | "use-remote", expectedSession: ExpectedSyncSession) {
    const operationEpoch = this.sessionEpoch;
    const credentials = this.validateAuthenticatedCredentials(
      await this.vault.loadPrivate(),
      expectedSession.expectedAccountScope,
      expectedSession.expectedSessionGeneration,
      {}
    );
    this.assertSessionEpoch(operationEpoch);
    if (credentials.scope === "migration_read" && strategy === "use-local") {
      throw new ApiError("Migration sessions cannot write local data to the backend", 403, "MIGRATION_READ_ONLY");
    }
    const local = await this.store.getLocalProfile();
    if (!local) throw new Error("LOCAL_PROFILE_NOT_INITIALIZED");
    const remote = await this.authenticated(
      credentials.accountScope,
      credentials.sessionGeneration,
      (token, api) => api.getProfile(token)
    );
    this.assertSessionEpoch(operationEpoch);

    if (strategy === "use-remote") {
      if (!remote.profile) throw new Error("FIRST_CONNECTION_REMOTE_PROFILE_EMPTY");
      const profile = await this.store.acceptRemote(
        credentials.accountScope,
        local.profile.profileId,
        { version: remote.version, profileHash: remote.profileHash, profile: remote.profile },
        {
          sessionGeneration: credentials.sessionGeneration,
          expectedLocalRevision: local.revision,
          isSessionCurrent: () => this.sessionEpoch === operationEpoch
        }
      );
      if (!(await this.vault.completeFirstConnection(credentials.sessionGeneration))) throw this.sessionChanged();
      const session = await this.vault.getPublicSession();
      this.assertSessionEpoch(operationEpoch);
      return { status: "remote-applied" as const, profile, session };
    }

    const mutation = await this.store.forceEnqueueCurrent(
      credentials.accountScope,
      local.profile.profileId,
      this.now(),
      { sessionGeneration: credentials.sessionGeneration }
    );
    this.assertSessionEpoch(operationEpoch);
    if (!(await this.vault.completeFirstConnection(credentials.sessionGeneration))) throw this.sessionChanged();
    const session = await this.vault.getPublicSession();
    this.assertSessionEpoch(operationEpoch);
    return { status: "local-queued" as const, mutationId: mutation.mutationId, session };
  }

  async resolveConflict(conflictId: string, profile: SharedProfileV2) {
    const credentials = await this.vault.loadPrivate();
    if (!credentials) throw new ApiError("Not signed in", 401, "AUTH_REQUIRED");
    if (credentials.firstConnectionPending) {
      throw new ApiError("Choose the first-connection strategy before resolving conflicts", 409, "FIRST_CONNECTION_PENDING");
    }
    if (credentials.scope === "migration_read") {
      throw new ApiError("Migration sessions cannot resolve sync conflicts", 403, "MIGRATION_READ_ONLY");
    }
    const metadata = await this.store.getActiveMetadata();
    if (!metadata || metadata.accountScope !== credentials.accountScope) throw new Error("SYNC_ACCOUNT_NOT_FOUND");
    const resolution = await this.store.resolveConflict(
      credentials.accountScope,
      metadata.profileId,
      conflictId,
      profile,
      this.now(),
      { sessionGeneration: credentials.sessionGeneration }
    );
    return { resolution, sync: await this.drain() };
  }

  async getConflict(conflictId: string) {
    const credentials = await this.vault.loadPrivate();
    if (!credentials) throw new ApiError("Not signed in", 401, "AUTH_REQUIRED");
    const metadata = await this.store.getActiveMetadata();
    if (!metadata || metadata.accountScope !== credentials.accountScope) throw new Error("SYNC_ACCOUNT_NOT_FOUND");
    const conflict = await this.store.getConflict(credentials.accountScope, metadata.profileId, conflictId);
    if (!conflict) throw new Error("SYNC_CONFLICT_NOT_FOUND");
    if (!this.generationMatches(conflict.sessionGeneration, credentials.sessionGeneration)) {
      throw this.sessionChanged();
    }
    if (!(await this.hasActiveSyncContext(credentials, metadata.profileId))) throw this.sessionChanged();
    return conflict;
  }

  async drain() {
    const credentials = await this.vault.loadPrivate();
    if (!credentials) return { status: "signed-out" as const };
    if (credentials.scope === "migration_read") return { status: "read-only" as const };
    if (credentials.firstConnectionPending) return { status: "connection-pending" as const };
    const metadata = await this.store.getActiveMetadata();
    if (!metadata || metadata.accountScope !== credentials.accountScope) return { status: "inactive" as const };
    const guard = { sessionGeneration: credentials.sessionGeneration };
    const terminalMutation = await this.store.getTerminalOutboxMutation(credentials.accountScope, metadata.profileId);
    if (terminalMutation?.terminalCode === "SERVER_EMPTY_CONFLICT" && this.generationMatches(terminalMutation.sessionGeneration, credentials.sessionGeneration)) {
      if (!(await this.hasActiveSyncContext(credentials, metadata.profileId, { requireReady: true }))) {
        return { status: "stale" as const };
      }
      return {
        status: "server-empty-conflict" as const,
        conflictId: terminalMutation.serverConflictId,
        mutationId: terminalMutation.mutationId
      };
    }
    const existingConflict = await this.store.getActiveConflict(credentials.accountScope, metadata.profileId);
    if (existingConflict && this.generationMatches(existingConflict.sessionGeneration, credentials.sessionGeneration)) {
      if (!(await this.hasActiveSyncContext(credentials, metadata.profileId, { requireReady: true }))) {
        return { status: "stale" as const };
      }
      return { status: "conflict" as const, conflictId: existingConflict.conflictId };
    }
    const mutation = await this.store.claimDue(credentials.accountScope, metadata.profileId, this.now(), guard);
    if (!mutation) return { status: "idle" as const };
    const device = await this.store.getDeviceState();
    if (!device) throw new Error("SYNC_DEVICE_STATE_MISSING");
    const profile = parseSharedProfileV2(JSON.parse(mutation.canonicalProfileJson) as unknown);

    try {
      const response = await this.authenticated(credentials.accountScope, credentials.sessionGeneration, (token, api) =>
        api.putProfile(token, {
          baseVersion: mutation.baseVersion!,
          mutationId: mutation.mutationId,
          deviceId: device.deviceId,
          schemaVersion: 2,
          profile,
          ...(mutation.resolvesConflictId ? { resolvesConflictId: mutation.resolvesConflictId } : {})
        }),
        { requireFull: true, requireReady: true }
      );
      if (!response.profile) throw new ApiError("PUT response omitted profile", 502, "INVALID_RESPONSE");
      await this.store.acknowledge(
        credentials.accountScope,
        metadata.profileId,
        mutation.mutationId,
        { version: response.version, profileHash: response.profileHash, profile: response.profile },
        guard
      );
      return { status: "synced" as const, version: response.version, idempotentReplay: response.idempotentReplay ?? false };
    } catch (error) {
      if (error instanceof ApiError && error.code === "PROFILE_CONFLICT") {
        const details = error.details as ProfileConflictDetails | undefined;
        if (
          typeof details?.conflictId === "string" &&
          details.currentVersion === 0 &&
          details.currentProfile === null &&
          (details.currentHash === "server-empty" || details.currentHash === null)
        ) {
          await this.store.freezeServerEmptyConflict(
            credentials.accountScope,
            metadata.profileId,
            mutation.mutationId,
            details.conflictId,
            guard
          );
          if (!(await this.hasActiveSyncContext(credentials, metadata.profileId, { requireReady: true }))) {
            return { status: "stale" as const };
          }
          return {
            status: "server-empty-conflict" as const,
            conflictId: details.conflictId,
            mutationId: mutation.mutationId
          };
        }
        if (
          typeof details?.currentVersion !== "number" ||
          typeof details.conflictId !== "string" ||
          typeof details.currentHash !== "string" ||
          details.currentHash.length === 0 ||
          !details.currentProfile
        ) {
          await this.store.scheduleRetry(
            credentials.accountScope,
            metadata.profileId,
            mutation.mutationId,
            { errorCode: "INVALID_CONFLICT" },
            this.now(),
            guard
          );
          return { status: "error" as const, code: "INVALID_CONFLICT" };
        }
        const currentProfile = parseSharedProfileV2(details.currentProfile);
        const conflict = await this.store.recordConflict(
          credentials.accountScope,
          metadata.profileId,
          mutation.mutationId,
          {
            conflictId: details.conflictId,
            version: details.currentVersion,
            profileHash: details.currentHash,
            profile: currentProfile
          },
          this.now(),
          guard
        );
        if (!(await this.hasActiveSyncContext(credentials, metadata.profileId, { requireReady: true }))) {
          return { status: "stale" as const };
        }
        return { status: "conflict" as const, conflictId: conflict.conflictId };
      }
      const apiError = error instanceof ApiError ? error : undefined;
      const code = apiError?.code ?? "NETWORK_ERROR";
      if (code === "AUTH_SESSION_CHANGED" || code === "FIRST_CONNECTION_PENDING" || code === "MIGRATION_READ_ONLY") {
        throw error;
      }
      await this.store.scheduleRetry(
        credentials.accountScope,
        metadata.profileId,
        mutation.mutationId,
        { errorCode: code, retryAfterMs: apiError?.retryAfterMs },
        this.now(),
        guard
      );
      return { status: "retrying" as const, code };
    }
  }

  async getCatalog(
    kind: "bootstrap" | "official-wallpapers" | "web-wallpapers" | "styles" | "uhdpaper-page" | "uhdpaper-image",
    query = "",
    publicBaseUrl?: string
  ) {
    if (kind === "bootstrap") {
      if (!publicBaseUrl) throw new ApiError("Backend URL is required", 400, "BACKEND_URL_REQUIRED");
      return this.apiFactory(canonicalBackendBaseUrl(publicBaseUrl)).bootstrap();
    }
    const credentials = await this.vault.loadPrivate();
    if (!credentials) throw new ApiError("Not signed in", 401, "AUTH_REQUIRED");
    if (credentials.scope === "migration_read") {
      throw new ApiError("Migration sessions cannot access the private catalog", 403, "MIGRATION_READ_ONLY");
    }
    return this.authenticated(credentials.accountScope, credentials.sessionGeneration, (token, api) => {
      if (kind === "official-wallpapers") return api.listOfficialWallpapers(token);
      if (kind === "web-wallpapers") return api.listWebWallpapers(token, query);
      if (kind === "uhdpaper-page") return api.fetchUhdpaperPage(token, query);
      if (kind === "uhdpaper-image") return api.fetchUhdpaperImage(token, query);
      return api.listStyles(token);
    }, { requireFull: true, requireReady: true });
  }
}
