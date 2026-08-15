import { describe, expect, it } from "vitest";
import { inferFeedbackTone } from "./Feedback";

describe("inferFeedbackTone", () => {
  it("取消操作保持中性，不伪装成成功", () => {
    expect(inferFeedbackTone("已取消导入，当前配置未更改。")).toBe("info");
  });

  it("区分进行中、警告、失败和成功", () => {
    expect(inferFeedbackTone("正在刷新后端资源…")).toBe("pending");
    expect(inferFeedbackTone("当前会话只读，请完成验证。")).toBe("warning");
    expect(inferFeedbackTone("刷新失败，已有资源仍可使用。")).toBe("error");
    expect(inferFeedbackTone("资源已刷新。")).toBe("success");
  });
});
