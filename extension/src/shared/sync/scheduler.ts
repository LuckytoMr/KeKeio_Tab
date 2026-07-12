export const backendSyncDelayMs = 3 * 60 * 1000;

type TimerHandle = ReturnType<typeof setTimeout>;

export class BackendSyncScheduler {
  private timer: TimerHandle | undefined;

  constructor(
    private readonly run: () => void | Promise<void>,
    private readonly delayMs = backendSyncDelayMs
  ) {}

  schedule() {
    this.cancel();
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.run();
    }, this.delayMs);
  }

  flush() {
    const hadPending = Boolean(this.timer);
    this.cancel();
    void this.run();
    return hadPending;
  }

  cancel() {
    if (!this.timer) return;
    clearTimeout(this.timer);
    this.timer = undefined;
  }

  hasPending() {
    return Boolean(this.timer);
  }
}
