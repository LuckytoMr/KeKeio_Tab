export const heartbeatAlarmName = "full-pro-sync-heartbeat-v2";
export const dueAlarmName = "full-pro-sync-due-v2";

export function shouldSuspendSyncDue(session?: { readOnly: boolean; firstConnectionPending: boolean }) {
  return session?.readOnly === true || session?.firstConnectionPending === true;
}

export type AlarmView = { name: string; scheduledTime: number };
export interface AlarmPort {
  get(name: string): Promise<AlarmView | undefined>;
  create(name: string, info: { when?: number; delayInMinutes?: number; periodInMinutes?: number }): Promise<void> | void;
  clear?(name: string): Promise<boolean> | boolean;
}

export class SyncAlarmManager {
  constructor(private readonly alarms: AlarmPort) {}

  async ensure(nextDueAt?: number, options: { suspendDue?: boolean } = {}) {
    const heartbeat = await this.alarms.get(heartbeatAlarmName);
    if (!heartbeat) {
      await this.alarms.create(heartbeatAlarmName, { delayInMinutes: 5, periodInMinutes: 30 });
    }
    if (options.suspendDue) {
      await this.alarms.clear?.(dueAlarmName);
      return;
    }
    if (nextDueAt === undefined) return;
    const due = await this.alarms.get(dueAlarmName);
    if (!due || due.scheduledTime > nextDueAt) {
      await this.alarms.create(dueAlarmName, { when: nextDueAt });
    }
  }
}

export function createChromeAlarmManager() {
  return new SyncAlarmManager({
    get: (name) => chrome.alarms.get(name) as Promise<AlarmView | undefined>,
    create: (name, info) => chrome.alarms.create(name, info),
    clear: (name) => chrome.alarms.clear(name)
  });
}
