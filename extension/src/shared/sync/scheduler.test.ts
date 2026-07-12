import { afterEach, describe, expect, test, vi } from "vitest";
import { BackendSyncScheduler, backendSyncDelayMs } from "./scheduler";

afterEach(() => {
  vi.useRealTimers();
});

describe("BackendSyncScheduler", () => {
  test("debounces changes until the default delay", () => {
    vi.useFakeTimers();
    const run = vi.fn();
    const scheduler = new BackendSyncScheduler(run);

    scheduler.schedule();
    vi.advanceTimersByTime(backendSyncDelayMs - 1);
    scheduler.schedule();
    vi.advanceTimersByTime(backendSyncDelayMs - 1);
    expect(run).not.toHaveBeenCalled();

    vi.advanceTimersByTime(1);
    expect(run).toHaveBeenCalledTimes(1);
    expect(scheduler.hasPending()).toBe(false);
  });

  test("flushes immediately and cancels the pending timer", () => {
    vi.useFakeTimers();
    const run = vi.fn();
    const scheduler = new BackendSyncScheduler(run);

    scheduler.schedule();
    expect(scheduler.flush()).toBe(true);
    expect(run).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(backendSyncDelayMs);
    expect(run).toHaveBeenCalledTimes(1);
  });
});
