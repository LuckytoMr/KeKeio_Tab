import { describe, expect, it, vi } from "vitest";
import { dispatchWorkerMessage, type SyncWorkerMessage, type WorkerRuntimePort } from "./workerProtocol";

describe("worker protocol", () => {
  it("routes credential-bearing login through the worker runtime", async () => {
    const login = vi.fn(async () => ({ firstConnection: "both-empty" }));
    const runtime = { login } as unknown as WorkerRuntimePort;
    const message: SyncWorkerMessage = {
      type: "auth:login",
      email: "one@example.test",
      password: "secret"
    };

    await expect(dispatchWorkerMessage(runtime, message)).resolves.toEqual({ firstConnection: "both-empty" });
    expect(login).toHaveBeenCalledWith({
      baseUrl: "https://tab.kekeio.com",
      email: "one@example.test",
      password: "secret"
    });
  });

  it("clears a session created for the former configurable backend", async () => {
    const getSession = vi.fn(async () => ({ baseUrl: "https://legacy.example.test", email: "one@example.test" }));
    const logout = vi.fn(async () => undefined);
    const runtime = { getSession, logout } as unknown as WorkerRuntimePort;

    await expect(dispatchWorkerMessage(runtime, { type: "auth:session" })).resolves.toBeUndefined();
    expect(logout).toHaveBeenCalledOnce();
  });

  it("rejects unknown messages instead of silently dispatching them", async () => {
    await expect(dispatchWorkerMessage({} as WorkerRuntimePort, { type: "unknown" } as never)).rejects.toThrow(/unknown/i);
  });

  it("returns a fixed conflict snapshot for the resolution UI", async () => {
    const getConflict = vi.fn(async () => ({ conflictId: "conflict:local" }));
    const runtime = { getConflict } as unknown as WorkerRuntimePort;

    await expect(dispatchWorkerMessage(runtime, { type: "sync:get-conflict", conflictId: "conflict:local" })).resolves.toEqual({
      conflictId: "conflict:local"
    });
  });

  it("uses the fixed public backend URL for anonymous bootstrap", async () => {
    const getCatalog = vi.fn(async () => ({ latestRelease: { version: "0.2.0" } }));
    const runtime = { getCatalog } as unknown as WorkerRuntimePort;
    const message: SyncWorkerMessage = {
      type: "catalog:get",
      kind: "bootstrap"
    };

    await dispatchWorkerMessage(runtime, message);

    expect(getCatalog).toHaveBeenCalledWith("bootstrap", undefined, "https://tab.kekeio.com");
  });

  it("routes account recovery requests without exposing them to the new-tab page fetch context", async () => {
    const resendVerification = vi.fn(async () => ({ accepted: true }));
    const forgotPassword = vi.fn(async () => ({ accepted: true }));
    const runtime = { resendVerification, forgotPassword } as unknown as WorkerRuntimePort;

    await expect(dispatchWorkerMessage(runtime, {
      type: "auth:resend-verification",
      email: " pending@example.test "
    })).resolves.toEqual({ accepted: true });
    await expect(dispatchWorkerMessage(runtime, {
      type: "auth:forgot-password",
      email: " one@example.test "
    })).resolves.toEqual({ accepted: true });

    expect(resendVerification).toHaveBeenCalledWith({
      baseUrl: "https://tab.kekeio.com",
      email: " pending@example.test "
    });
    expect(forgotPassword).toHaveBeenCalledWith({
      baseUrl: "https://tab.kekeio.com",
      email: " one@example.test "
    });
  });
});
