export interface ApiErrorShape {
  code?: string;
  message?: string;
  details?: unknown;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details?: unknown;
  readonly requestId?: string;

  constructor(input: { status: number; code?: string; message?: string; details?: unknown; requestId?: string }) {
    super(input.message || `请求失败（${input.status}）`);
    this.name = "ApiError";
    this.status = input.status;
    this.code = input.code || `HTTP_${input.status}`;
    this.details = input.details;
    this.requestId = input.requestId;
  }
}

interface Envelope<T> {
  data: T;
  requestId?: string;
}

function isEnvelope<T>(value: unknown): value is Envelope<T> {
  return Boolean(value && typeof value === "object" && "data" in value);
}

function isStateChanging(method: string): boolean {
  return method !== "GET" && method !== "HEAD" && method !== "OPTIONS";
}

export class ApiClient {
  private csrfToken = "";
  private unauthorizedListeners = new Set<() => void>();
  lastRequestId = "";

  setCsrfToken(value: string | null | undefined): void {
    this.csrfToken = value?.trim() ?? "";
  }

  onUnauthorized(listener: () => void): () => void {
    this.unauthorizedListeners.add(listener);
    return () => this.unauthorizedListeners.delete(listener);
  }

  async get<T>(path: string, signal?: AbortSignal): Promise<T> {
    return this.request<T>(path, { method: "GET", signal });
  }

  async getWithLegacy<T>(path: string, legacyPath: string, signal?: AbortSignal): Promise<T> {
    try {
      return await this.get<T>(path, signal);
    } catch (error) {
      if (!(error instanceof ApiError) || ![404, 501].includes(error.status)) throw error;
      return this.get<T>(legacyPath, signal);
    }
  }

  async post<T = unknown>(path: string, body: unknown, signal?: AbortSignal): Promise<T> {
    return this.request<T>(path, { method: "POST", body: JSON.stringify(body), signal });
  }

  async put<T = unknown>(path: string, body: unknown, signal?: AbortSignal): Promise<T> {
    return this.request<T>(path, { method: "PUT", body: JSON.stringify(body), signal });
  }

  async delete<T = unknown>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    return this.request<T>(path, {
      method: "DELETE",
      body: body === undefined ? undefined : JSON.stringify(body),
      signal
    });
  }

  async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const method = (init.method || "GET").toUpperCase();
    const headers = new Headers(init.headers);
    if (init.body !== undefined && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    if (isStateChanging(method) && this.csrfToken) headers.set("X-CSRF-Token", this.csrfToken);

    const response = await fetch(path, {
      ...init,
      method,
      headers,
      credentials: "same-origin"
    });

    const raw = await response.text();
    let payload: unknown;
    if (raw) {
      try {
        payload = JSON.parse(raw);
      } catch {
        throw new ApiError({ status: response.status, code: "INVALID_RESPONSE", message: "服务器返回了无法解析的响应" });
      }
    }

    const requestId =
      payload && typeof payload === "object" && "requestId" in payload && typeof payload.requestId === "string"
        ? payload.requestId
        : response.headers.get("X-Request-Id") || undefined;
    this.lastRequestId = requestId ?? "";

    if (!response.ok) {
      const rawError = payload && typeof payload === "object" && "error" in payload ? payload.error : undefined;
      const error: ApiErrorShape =
        typeof rawError === "string"
          ? { message: rawError }
          : rawError && typeof rawError === "object"
            ? (rawError as ApiErrorShape)
            : {};
      if (response.status === 401 && !path.startsWith("/api/admin/v1/auth/")) {
        this.csrfToken = "";
        for (const listener of this.unauthorizedListeners) listener();
      }
      throw new ApiError({
        status: response.status,
        code: error.code,
        message: error.message,
        details: error.details,
        requestId
      });
    }

    return (isEnvelope<T>(payload) ? payload.data : payload) as T;
  }
}

export const apiClient = new ApiClient();
