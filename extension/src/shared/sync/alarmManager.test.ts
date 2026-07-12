import { describe, expect, it, vi } from "vitest";
import { dueAlarmName, heartbeatAlarmName, shouldSuspendSyncDue, SyncAlarmManager, type AlarmPort } from "./alarmManager";

describe("SyncAlarmManager", () => {
  it("suspends due work for both read-only and first-connection-pending sessions", () => {
    expect(shouldSuspendSyncDue({ readOnly: true, firstConnectionPending: false })).toBe(true);
    expect(shouldSuspendSyncDue({ readOnly: false, firstConnectionPending: true })).toBe(true);
    expect(shouldSuspendSyncDue({ readOnly: false, firstConnectionPending: false })).toBe(false);
    expect(shouldSuspendSyncDue(undefined)).toBe(false);
  });

  it("recreates missing alarms whenever the worker starts", async () => {
    const alarms = new Map<string, { name: string; scheduledTime: number }>();
    const create = vi.fn(async (name: string, info: { when?: number; periodInMinutes?: number }) => {
      alarms.set(name, { name, scheduledTime: info.when ?? 60_000 });
    });
    const manager = new SyncAlarmManager({
      get: async (name) => alarms.get(name),
      create
    });

    await manager.ensure(120_000);

    expect(create).toHaveBeenCalledWith(heartbeatAlarmName, { delayInMinutes: 5, periodInMinutes: 30 });
    expect(create).toHaveBeenCalledWith(dueAlarmName, { when: 120_000 });
  });

  it("never postpones an already earlier due alarm", async () => {
    const create = vi.fn(async () => undefined);
    const manager = new SyncAlarmManager({
      get: async (name) => name === dueAlarmName ? { name, scheduledTime: 100_000 } : { name, scheduledTime: 60_000 },
      create
    });

    await manager.ensure(120_000);

    expect(create).not.toHaveBeenCalled();
  });

  it("keeps the heartbeat but clears the due alarm while sync writes are suspended", async () => {
    const create = vi.fn(async () => undefined);
    const clear = vi.fn(async () => true);
    const port: AlarmPort & { clear(name: string): Promise<boolean> } = {
      get: async (name) => ({ name, scheduledTime: name === dueAlarmName ? 50_000 : 60_000 }),
      create,
      clear
    };
    const manager = new SyncAlarmManager(port);

    await manager.ensure(undefined, { suspendDue: true });

    expect(clear).toHaveBeenCalledWith(dueAlarmName);
    expect(create).not.toHaveBeenCalled();
  });
});
