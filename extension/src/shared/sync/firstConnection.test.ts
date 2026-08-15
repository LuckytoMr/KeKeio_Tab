import { describe, expect, it, vi } from "vitest";
import {
  getAutomaticFirstConnectionStrategy,
  requiresExplicitReadOnlyRemoteApproval,
  resolveFirstConnectionStrategy
} from "./firstConnection";

describe("getAutomaticFirstConnectionStrategy", () => {
  it("在新浏览器只有云端配置时自动采用云端配置", () => {
    expect(getAutomaticFirstConnectionStrategy("remote-only")).toBe("use-remote");
  });

  it("本机和云端都有配置时必须等待用户明确选择", () => {
    expect(getAutomaticFirstConnectionStrategy("both-have-data")).toBeNull();
  });

  it.each(["both-empty", "local-only"] as const)("%s 默认保留本机配置", (kind) => {
    expect(getAutomaticFirstConnectionStrategy(kind)).toBe("use-local");
  });
});

describe("requiresExplicitReadOnlyRemoteApproval", () => {
  it("只读会话在本机和云端都有配置时仍必须明确确认", () => {
    expect(requiresExplicitReadOnlyRemoteApproval("both-have-data")).toBe(true);
    expect(requiresExplicitReadOnlyRemoteApproval("remote-only")).toBe(false);
  });
});

describe("resolveFirstConnectionStrategy", () => {
  it("新浏览器只有云端配置时直接采用云端，不打开决策弹窗", async () => {
    const requestDecision = vi.fn(async () => "use-local" as const);
    const cancelPendingConnection = vi.fn(async () => undefined);

    await expect(resolveFirstConnectionStrategy("remote-only", requestDecision, cancelPendingConnection))
      .resolves.toBe("use-remote");
    expect(requestDecision).not.toHaveBeenCalled();
    expect(cancelPendingConnection).not.toHaveBeenCalled();
  });

  it("本机和云端都有配置时返回用户明确选择的结果", async () => {
    const requestDecision = vi.fn(async () => "use-local" as const);
    const cancelPendingConnection = vi.fn(async () => undefined);

    await expect(resolveFirstConnectionStrategy("both-have-data", requestDecision, cancelPendingConnection))
      .resolves.toBe("use-local");
    expect(requestDecision).toHaveBeenCalledOnce();
    expect(cancelPendingConnection).not.toHaveBeenCalled();
  });

  it("关闭或取消决策时退出半连接会话，不暗含采用云端", async () => {
    const requestDecision = vi.fn(async () => null);
    const cancelPendingConnection = vi.fn(async () => undefined);

    await expect(resolveFirstConnectionStrategy("both-have-data", requestDecision, cancelPendingConnection))
      .resolves.toBeNull();
    expect(requestDecision).toHaveBeenCalledOnce();
    expect(cancelPendingConnection).toHaveBeenCalledOnce();
  });
});
