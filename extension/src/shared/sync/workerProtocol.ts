import type { SharedProfileV2 } from "../profile/sharedProfile";
import { fixedBackendUrl, isFixedBackendUrl } from "./backendEndpoint";

export type SyncWorkerMessage =
  | { type: "auth:session" }
  | { type: "auth:register"; email: string; password: string }
  | { type: "auth:login"; email: string; password: string }
  | { type: "auth:resend-verification"; email: string }
  | { type: "auth:forgot-password"; email: string }
  | {
      type: "auth:logout";
      expectedAccountScope: string;
      expectedSessionGeneration: string;
    }
  | {
      type: "sync:complete-first-connection";
      strategy: "use-local" | "use-remote";
      expectedAccountScope: string;
      expectedSessionGeneration: string;
    }
  | { type: "sync:notify-change" }
  | { type: "sync:flush" }
  | { type: "sync:get-conflict"; conflictId: string }
  | { type: "sync:resolve-conflict"; conflictId: string; profile: SharedProfileV2 }
  | { type: "catalog:get"; kind: "bootstrap" }
  | {
      type: "catalog:get";
      kind: "official-wallpapers" | "web-wallpapers" | "styles";
      query?: string;
    }
  | { type: "catalog:get"; kind: "uhdpaper-page" | "uhdpaper-image"; query: string };

export interface WorkerRuntimePort {
  login(input: { baseUrl: string; email: string; password: string }): Promise<unknown>;
  register(input: { baseUrl: string; email: string; password: string }): Promise<unknown>;
  resendVerification(input: { baseUrl: string; email: string }): Promise<unknown>;
  forgotPassword(input: { baseUrl: string; email: string }): Promise<unknown>;
  logout(expectedSession: {
    expectedAccountScope: string;
    expectedSessionGeneration: string;
  }): Promise<unknown>;
  getSession(): Promise<unknown>;
  completeFirstConnection(
    strategy: "use-local" | "use-remote",
    expectedSession: {
      expectedAccountScope: string;
      expectedSessionGeneration: string;
    }
  ): Promise<unknown>;
  drain(): Promise<unknown>;
  getConflict(conflictId: string): Promise<unknown>;
  resolveConflict(conflictId: string, profile: SharedProfileV2): Promise<unknown>;
  getCatalog(
    kind: "bootstrap" | "official-wallpapers" | "web-wallpapers" | "styles" | "uhdpaper-page" | "uhdpaper-image",
    query?: string,
    baseUrl?: string
  ): Promise<unknown>;
}

async function getFixedBackendSession(runtime: WorkerRuntimePort) {
  const session = await runtime.getSession();
  if (!session || typeof session !== "object") return session;

  const { baseUrl, accountScope, sessionGeneration } = session as {
    baseUrl?: unknown;
    accountScope?: unknown;
    sessionGeneration?: unknown;
  };
  if (isFixedBackendUrl(baseUrl)) return session;

  if (typeof accountScope === "string" && typeof sessionGeneration === "string") {
    await runtime.logout({
      expectedAccountScope: accountScope,
      expectedSessionGeneration: sessionGeneration
    });
  }
  return undefined;
}

export function dispatchWorkerMessage(runtime: WorkerRuntimePort, message: SyncWorkerMessage) {
  switch (message.type) {
    case "auth:session":
      return getFixedBackendSession(runtime);
    case "auth:register":
      return runtime.register({ baseUrl: fixedBackendUrl, email: message.email, password: message.password });
    case "auth:login":
      return runtime.login({ baseUrl: fixedBackendUrl, email: message.email, password: message.password });
    case "auth:resend-verification":
      return runtime.resendVerification({ baseUrl: fixedBackendUrl, email: message.email });
    case "auth:forgot-password":
      return runtime.forgotPassword({ baseUrl: fixedBackendUrl, email: message.email });
    case "auth:logout":
      return runtime.logout({
        expectedAccountScope: message.expectedAccountScope,
        expectedSessionGeneration: message.expectedSessionGeneration
      });
    case "sync:complete-first-connection":
      return runtime.completeFirstConnection(message.strategy, {
        expectedAccountScope: message.expectedAccountScope,
        expectedSessionGeneration: message.expectedSessionGeneration
      });
    case "sync:notify-change":
      return Promise.resolve({ queued: true });
    case "sync:flush":
      return runtime.drain();
    case "sync:get-conflict":
      return runtime.getConflict(message.conflictId);
    case "sync:resolve-conflict":
      return runtime.resolveConflict(message.conflictId, message.profile);
    case "catalog:get":
      return message.kind === "bootstrap"
        ? runtime.getCatalog(message.kind, undefined, fixedBackendUrl)
        : runtime.getCatalog(message.kind, message.query);
    default:
      return Promise.reject(new Error("Unknown sync worker message"));
  }
}

export async function sendWorkerMessage<T>(message: SyncWorkerMessage): Promise<T> {
  const response = await chrome.runtime.sendMessage(message) as { ok: boolean; data?: T; error?: string; code?: string };
  if (!response?.ok) throw new Error(response?.error || response?.code || "Worker request failed");
  return response.data as T;
}
