import { z } from "zod";
import { sharedProfileV2Schema, type SharedProfileV2 } from "../profile/sharedProfile";

type ErrorPayload = {
  error?: { code?: unknown; message?: unknown; details?: unknown };
  requestId?: unknown;
};

type SyncRequestOptions = {
  method?: "GET" | "POST" | "PUT";
  accessToken?: string;
  body?: unknown;
  mutationId?: string;
  acceptNoContent?: boolean;
};

export const syncApiDefaultTimeoutMs = 30_000;

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
    readonly details?: unknown,
    readonly requestId?: string,
    readonly retryAfterMs?: number
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export type PutProfileRequest = {
  baseVersion: number;
  mutationId: string;
  deviceId: string;
  schemaVersion: 2;
  profile: SharedProfileV2;
  resolvesConflictId?: string;
};

const profileHashSchema = z.string().min(1);

const populatedProfileRecordSchema = z.object({
  profile: sharedProfileV2Schema,
  version: z.number().int().nonnegative(),
  profileHash: profileHashSchema,
  schemaVersion: z.literal(2),
  updatedAt: z.string().optional(),
  mutationId: z.string().optional(),
  idempotentReplay: z.boolean().optional()
});

const emptyProfileRecordSchema = z.object({
  profile: z.null(),
  version: z.literal(0),
  profileHash: z.null(),
  schemaVersion: z.literal(2)
}).strict();

const profileRecordResponseSchema = z.union([populatedProfileRecordSchema, emptyProfileRecordSchema]);

export type PopulatedProfileRecordResponse = z.infer<typeof populatedProfileRecordSchema>;
export type EmptyProfileRecordResponse = z.infer<typeof emptyProfileRecordSchema>;
export type ProfileRecordResponse = z.infer<typeof profileRecordResponseSchema>;

const populatedProfileConflictDetailsSchema = z.object({
  conflictId: z.string().min(1),
  baseVersion: z.number().int().nonnegative(),
  currentVersion: z.number().int().nonnegative(),
  currentHash: profileHashSchema,
  currentProfile: sharedProfileV2Schema
});

const emptyProfileConflictDetailsSchema = z.object({
  conflictId: z.string().min(1),
  baseVersion: z.number().int().nonnegative(),
  currentVersion: z.literal(0),
  currentHash: z.union([z.literal("server-empty"), z.null()]),
  currentProfile: z.null()
});

const profileConflictDetailsSchema = z.union([
  emptyProfileConflictDetailsSchema,
  populatedProfileConflictDetailsSchema
]);

export type ProfileConflictDetails = z.infer<typeof profileConflictDetailsSchema>;

const authUserSchema = z.object({
  id: z.string().min(1),
  email: z.string().email(),
  role: z.literal("user")
}).passthrough();

const tokenBaseSchema = z.object({
  user: authUserSchema,
  accessToken: z.string().min(1),
  accessExpiresAt: z.string().min(1),
  tokenFamily: z.string().optional()
});

const fullTokenSchema = tokenBaseSchema.extend({
  scope: z.literal("full"),
  refreshToken: z.string().min(1),
  refreshExpiresAt: z.string().min(1)
}).passthrough();

const migrationReadTokenSchema = tokenBaseSchema.extend({
  scope: z.literal("migration_read")
}).passthrough();

const tokenSchema = z.discriminatedUnion("scope", [fullTokenSchema, migrationReadTokenSchema]);

export type TokenResponse = z.infer<typeof tokenSchema>;

export function canonicalBackendBaseUrl(raw: string) {
  const url = new URL(raw.trim());
  if (url.protocol !== "https:" && !(url.protocol === "http:" && isPrivateHTTPHost(url.hostname))) {
    throw new Error("Backend URL must use HTTPS (HTTP is allowed only for localhost, RFC1918, or ULA addresses)");
  }
  url.username = "";
  url.password = "";
  url.search = "";
  url.hash = "";
  url.pathname = url.pathname.replace(/\/+$/, "");
  return url.toString().replace(/\/+$/, "");
}

function isPrivateHTTPHost(rawHostname: string) {
  const hostname = rawHostname.toLowerCase().replace(/^\[|\]$/g, "").replace(/\.$/, "");
  if (hostname === "localhost" || hostname === "::1") return true;
  if (/^(?:fc|fd)[0-9a-f]{2}:/i.test(hostname)) return true;
  const octets = hostname.split(".").map(Number);
  if (octets.length !== 4 || octets.some((value) => !Number.isInteger(value) || value < 0 || value > 255)) return false;
  return octets[0] === 10 || octets[0] === 127 || (octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31) || (octets[0] === 192 && octets[1] === 168);
}

function retryAfterMs(value: string | null) {
  if (!value) return undefined;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1000;
  const date = Date.parse(value);
  return Number.isFinite(date) ? Math.max(0, date - Date.now()) : undefined;
}

export class SyncApiClient {
  readonly baseUrl: string;

  constructor(baseUrl: string) {
    this.baseUrl = canonicalBackendBaseUrl(baseUrl);
  }

  private async request(
    path: string,
    options: SyncRequestOptions = {}
  ) {
    const controller = new AbortController();
    let timeoutId: ReturnType<typeof setTimeout> | undefined;
    const timeout = new Promise<never>((_resolve, reject) => {
      timeoutId = setTimeout(() => {
        const error = new ApiError("Backend request timed out", 0, "REQUEST_TIMEOUT");
        reject(error);
        controller.abort(error);
      }, syncApiDefaultTimeoutMs);
    });
    try {
      return await Promise.race([this.fetchAndParse(path, options, controller.signal), timeout]);
    } finally {
      if (timeoutId !== undefined) clearTimeout(timeoutId);
    }
  }

  private async fetchAndParse(path: string, options: SyncRequestOptions, signal: AbortSignal) {
    const headers: Record<string, string> = { Accept: "application/json" };
    if (options.body !== undefined) headers["Content-Type"] = "application/json";
    if (options.accessToken) headers.Authorization = `Bearer ${options.accessToken}`;
    if (options.mutationId) headers["Idempotency-Key"] = options.mutationId;
    const response = await fetch(`${this.baseUrl}${path}`, {
      method: options.method ?? "GET",
      credentials: "omit",
      redirect: "error",
      cache: "no-store",
      headers,
      signal,
      body: options.body === undefined ? undefined : JSON.stringify(options.body)
    });

    if (response.status === 204 && options.acceptNoContent) return undefined;

    const contentType = response.headers.get("content-type") ?? "";
    if (!contentType.toLowerCase().includes("application/json")) {
      throw new ApiError("Backend returned a non-JSON response", response.status, "INVALID_RESPONSE");
    }
    let payload: unknown;
    try {
      payload = await response.json();
    } catch {
      throw new ApiError("Backend returned invalid JSON", response.status, "INVALID_RESPONSE");
    }

    if (!response.ok) {
      const errorPayload = payload as ErrorPayload;
      const code = typeof errorPayload.error?.code === "string" ? errorPayload.error.code : `HTTP_${response.status}`;
      const message = typeof errorPayload.error?.message === "string" ? errorPayload.error.message : "Backend request failed";
      let details = errorPayload.error?.details;
      if (code === "PROFILE_CONFLICT") {
        const parsed = profileConflictDetailsSchema.safeParse(details);
        if (!parsed.success) {
          throw new ApiError(
            "Backend returned invalid conflict details",
            response.status,
            "INVALID_RESPONSE",
            undefined,
            typeof errorPayload.requestId === "string" ? errorPayload.requestId : undefined,
            retryAfterMs(response.headers.get("retry-after"))
          );
        }
        details = parsed.data;
      }
      throw new ApiError(
        message,
        response.status,
        code,
        details,
        typeof errorPayload.requestId === "string" ? errorPayload.requestId : undefined,
        retryAfterMs(response.headers.get("retry-after"))
      );
    }
    if (!payload || typeof payload !== "object" || !("data" in payload)) {
      throw new ApiError("Backend response envelope is invalid", response.status, "INVALID_RESPONSE");
    }
    return (payload as { data: unknown }).data;
  }

  async register(input: { email: string; password: string }) {
    return this.request("/api/v1/auth/register", { method: "POST", body: input });
  }

  async resendVerification(email: string) {
    return this.request("/api/v1/auth/resend-verification", { method: "POST", body: { email } });
  }

  async forgotPassword(email: string) {
    return this.request("/api/v1/auth/forgot-password", { method: "POST", body: { email } });
  }

  async login(input: { email: string; password: string; deviceId: string }) {
    return tokenSchema.parse(await this.request("/api/v1/auth/login", { method: "POST", body: input }));
  }

  async refresh(refreshToken: string, requestId: string) {
    return tokenSchema.parse(
      await this.request("/api/v1/auth/refresh", { method: "POST", body: { refreshToken, requestId } })
    );
  }

  async logout(accessToken: string, refreshToken?: string) {
    return this.request("/api/v1/auth/logout", {
      method: "POST",
      accessToken,
      body: refreshToken ? { refreshToken } : {},
      acceptNoContent: true
    });
  }

  async getProfile(accessToken: string) {
    return profileRecordResponseSchema.parse(await this.request("/api/v1/sync/profile", { accessToken }));
  }

  async putProfile(accessToken: string, input: PutProfileRequest) {
    if (input.mutationId.trim() === "") throw new Error("mutationId is required");
    return populatedProfileRecordSchema.parse(
      await this.request("/api/v1/sync/profile", {
        method: "PUT",
        accessToken,
        body: input,
        mutationId: input.mutationId
      })
    );
  }

  bootstrap() {
    return this.request("/api/v1/app/bootstrap");
  }

  listOfficialWallpapers(accessToken: string) {
    return this.request("/api/v1/catalog/wallpapers/official", { accessToken });
  }

  listWebWallpapers(accessToken: string, query = "") {
    return this.request(`/api/v1/catalog/wallpapers/web${query}`, { accessToken });
  }

  listStyles(accessToken: string) {
    return this.request("/api/v1/catalog/styles", { accessToken });
  }

  fetchUhdpaperPage(accessToken: string, url: string) {
    return this.request(`/api/v1/catalog/uhdpaper/page?url=${encodeURIComponent(url)}`, { accessToken });
  }

  fetchUhdpaperImage(accessToken: string, url: string) {
    return this.request(`/api/v1/catalog/uhdpaper/image?url=${encodeURIComponent(url)}`, { accessToken });
  }
}
