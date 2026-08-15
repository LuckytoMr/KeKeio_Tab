import { AlertCircle, CheckCircle2, Info, LoaderCircle, TriangleAlert } from "lucide-preact";
import type { ComponentChildren } from "preact";

export type FeedbackTone = "pending" | "success" | "warning" | "error" | "info";

export type ActionFeedback = {
  tone: FeedbackTone;
  message: string;
};

export function inferFeedbackTone(message: string): FeedbackTone {
  if (/取消|未更改|未发生变化/.test(message)) return "info";
  if (/失败|错误|无法|冲突|冻结|过期|未保存/.test(message)) return "error";
  if (/只读|需验证|请确认|暂停/.test(message)) return "warning";
  if (/已|完成|成功|受理|最新/.test(message)) return "success";
  if (/正在|等待|重试|处理中/.test(message)) return "pending";
  return "info";
}

export function BusyLabel({ busy, busyText, children }: {
  busy: boolean;
  busyText: string;
  children: ComponentChildren;
}) {
  return busy ? (
    <>
      <LoaderCircle className="spin" aria-hidden="true" size={17} />
      {busyText}
    </>
  ) : <>{children}</>;
}

export function ActionStatus({ feedback, compact = false }: {
  feedback: ActionFeedback;
  compact?: boolean;
}) {
  if (!feedback.message) return null;
  const Icon = feedback.tone === "pending"
    ? LoaderCircle
    : feedback.tone === "success"
      ? CheckCircle2
      : feedback.tone === "error"
        ? AlertCircle
        : feedback.tone === "warning"
          ? TriangleAlert
          : Info;
  const role = feedback.tone === "error" ? "alert" : "status";

  return (
    <div
      className={`action-status tone-${feedback.tone}${compact ? " compact" : ""}`}
      role={role}
      aria-live={feedback.tone === "error" ? "assertive" : "polite"}
    >
      <Icon className={feedback.tone === "pending" ? "spin" : undefined} aria-hidden="true" size={17} />
      <span>{feedback.message}</span>
    </div>
  );
}
