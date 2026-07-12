import type { ComponentChildren } from "preact";
import { useEffect, useId, useRef } from "preact/hooks";

export function FormErrorSummary({
  errors,
  focusOnMount = false,
  label = "请修正以下问题"
}: {
  errors: Record<string, string>;
  focusOnMount?: boolean;
  label?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const entries = Object.entries(errors).filter(([, message]) => Boolean(message));

  useEffect(() => {
    if (focusOnMount && entries.length) ref.current?.focus();
  }, [focusOnMount, entries.length]);

  if (!entries.length) return null;
  return (
    <div ref={ref} class="error-summary" role="alert" aria-label={label} tabIndex={-1}>
      <strong>{label}</strong>
      <ul>
        {entries.map(([field, message]) => (
          <li key={field}>
            <a href={`#${field}`}>{message}</a>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  busy = false,
  onCancel,
  onConfirm
}: {
  open: boolean;
  title: string;
  description: ComponentChildren;
  confirmLabel: string;
  busy?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const titleId = useId();
  const cancelRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);
  const onCancelRef = useRef(onCancel);
  const busyRef = useRef(busy);
  onCancelRef.current = onCancel;
  busyRef.current = busy;

  useEffect(() => {
    if (!open) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const isolated: Array<{ element: HTMLElement; hadInert: boolean; ariaHidden: string | null }> = [];
    let branch: HTMLElement | null = dialogRef.current;
    while (branch && branch !== document.body) {
      const parent = branch.parentElement;
      if (!parent) break;
      for (const sibling of Array.from(parent.children)) {
        if (sibling === branch || !(sibling instanceof HTMLElement)) continue;
        isolated.push({ element: sibling, hadInert: sibling.hasAttribute("inert"), ariaHidden: sibling.getAttribute("aria-hidden") });
        sibling.setAttribute("inert", "");
        sibling.setAttribute("aria-hidden", "true");
      }
      branch = parent;
    }
    cancelRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busyRef.current) {
        event.preventDefault();
        onCancelRef.current();
      }
      if (event.key !== "Tab") return;
      const focusable = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>("button:not(:disabled), [href], input:not(:disabled)") ?? []);
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      for (const { element, hadInert, ariaHidden } of isolated) {
        if (!hadInert) element.removeAttribute("inert");
        if (ariaHidden === null) element.removeAttribute("aria-hidden");
        else element.setAttribute("aria-hidden", ariaHidden);
      }
      previous?.focus();
    };
  }, [open]);

  if (!open) return null;
  return (
    <div class="dialog-backdrop" role="presentation">
      <div ref={dialogRef} class="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div class="dialog-heading">
          <p class="section-label">需要确认</p>
          <h2 id={titleId}>{title}</h2>
        </div>
        <div class="dialog-copy">{description}</div>
        <div class="dialog-actions">
          <button ref={cancelRef} class="button secondary" type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button class="button danger" type="button" onClick={onConfirm} disabled={busy}>
            {busy ? "处理中…" : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
