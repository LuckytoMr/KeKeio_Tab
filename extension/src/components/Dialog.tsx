import { AlertTriangle, CircleHelp, Cloud, LoaderCircle, Monitor, X } from "lucide-preact";
import type { ComponentChildren } from "preact";
import { useCallback, useEffect, useId, useRef, useState } from "preact/hooks";

export type DialogTone = "neutral" | "warning" | "danger";

export type ConfirmRequest = {
  kind?: "confirm";
  dedupeKey?: string;
  title: string;
  description: ComponentChildren;
  confirmLabel: string;
  cancelLabel?: string;
  tone?: DialogTone;
};

export type DecisionOption<T extends string> = {
  value: T;
  label: string;
  description: string;
  consequence?: string;
  icon?: "device" | "cloud";
};

export type DecisionRequest<T extends string> = {
  kind?: "decision";
  dedupeKey?: string;
  title: string;
  description: ComponentChildren;
  options: readonly DecisionOption<T>[];
  confirmLabel: string;
  cancelLabel?: string;
  tone?: DialogTone;
};

type ConfirmDialogRequest = ConfirmRequest & { kind: "confirm" };
type AnyDecisionRequest = DecisionRequest<string> & { kind: "decision" };
type AnyDialogRequest = ConfirmDialogRequest | AnyDecisionRequest;
type DialogResult = boolean | string | null;

type QueuedDialog = {
  id: number;
  request: AnyDialogRequest;
  resolve: (value: DialogResult) => void;
};

const focusableSelector = [
  "button:not(:disabled)",
  "a[href]",
  "input:not(:disabled)",
  "select:not(:disabled)",
  "textarea:not(:disabled)",
  "[tabindex]:not([tabindex='-1'])"
].join(",");

function DialogToneIcon({ tone }: { tone: DialogTone }) {
  if (tone === "warning" || tone === "danger") return <AlertTriangle aria-hidden="true" size={22} />;
  return <CircleHelp aria-hidden="true" size={22} />;
}

export function DialogShell({
  open,
  title,
  description,
  tone = "neutral",
  cancelLabel = "取消",
  confirmLabel,
  confirmDisabled = false,
  busy = false,
  children,
  onCancel,
  onConfirm
}: {
  open: boolean;
  title: string;
  description: ComponentChildren;
  tone?: DialogTone;
  cancelLabel?: string;
  confirmLabel: string;
  confirmDisabled?: boolean;
  busy?: boolean;
  children?: ComponentChildren;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const titleId = useId();
  const descriptionId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const onCancelRef = useRef(onCancel);
  const busyRef = useRef(busy);
  onCancelRef.current = onCancel;
  busyRef.current = busy;

  useEffect(() => {
    if (!open) return;

    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const isolated: Array<{ element: HTMLElement; hadInert: boolean; ariaHidden: string | null }> = [];
    const previousOverflow = document.documentElement.style.overflow;
    cancelRef.current?.focus({ preventScroll: true });
    let branch: HTMLElement | null = dialogRef.current;

    while (branch && branch !== document.body) {
      const parent = branch.parentElement;
      if (!parent) break;
      for (const sibling of Array.from(parent.children)) {
        if (sibling === branch || !(sibling instanceof HTMLElement)) continue;
        isolated.push({
          element: sibling,
          hadInert: sibling.hasAttribute("inert"),
          ariaHidden: sibling.getAttribute("aria-hidden")
        });
        sibling.setAttribute("inert", "");
        sibling.setAttribute("aria-hidden", "true");
      }
      branch = parent;
    }

    document.documentElement.style.overflow = "hidden";
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busyRef.current) {
        event.preventDefault();
        onCancelRef.current();
        return;
      }
      if (event.key !== "Tab") return;

      const focusable = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>(focusableSelector) ?? [])
        .filter((element) => element.offsetParent !== null || element === document.activeElement);
      if (!focusable.length) {
        event.preventDefault();
        dialogRef.current?.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (!dialogRef.current?.contains(document.activeElement)) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
        return;
      }
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      document.documentElement.style.overflow = previousOverflow;
      for (const { element, hadInert, ariaHidden } of isolated) {
        if (!hadInert) element.removeAttribute("inert");
        if (ariaHidden === null) element.removeAttribute("aria-hidden");
        else element.setAttribute("aria-hidden", ariaHidden);
      }
      const previousFocusAvailable = Boolean(
        previousFocus?.isConnected
        && !previousFocus.matches(":disabled, [aria-disabled='true']")
        && !previousFocus.closest("[inert]")
      );
      if (previousFocusAvailable) {
        previousFocus?.focus({ preventScroll: true });
      } else {
        const fallback = Array.from(document.querySelectorAll<HTMLElement>(focusableSelector)).find((element) => (
          !dialogRef.current?.contains(element)
          && !element.closest("[inert]")
          && element.offsetParent !== null
        ));
        fallback?.focus({ preventScroll: true });
      }
    };
  }, [open]);

  if (!open) return null;

  return (
    <div className="enterprise-dialog-backdrop" role="presentation">
      <div
        ref={dialogRef}
        className={`enterprise-dialog tone-${tone}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
        aria-busy={busy || undefined}
        tabIndex={-1}
      >
        <header className="enterprise-dialog-header">
          <span className="enterprise-dialog-icon" aria-hidden="true">
            <DialogToneIcon tone={tone} />
          </span>
          <div>
            <h2 id={titleId}>{title}</h2>
          </div>
          <button
            type="button"
            className="icon-button enterprise-dialog-close"
            aria-label="关闭弹窗"
            onClick={onCancel}
            disabled={busy}
          >
            <X size={20} />
          </button>
        </header>

        <div className="enterprise-dialog-body">
          <div id={descriptionId} className="enterprise-dialog-description">
            {description}
          </div>
          {children}
        </div>

        <footer className="enterprise-dialog-actions">
          <button ref={cancelRef} className="command" type="button" onClick={onCancel} disabled={busy}>
            {cancelLabel}
          </button>
          <button
            className={`command ${tone === "danger" ? "danger solid" : "primary"}`}
            type="button"
            onClick={onConfirm}
            disabled={busy || confirmDisabled}
          >
            {busy ? <LoaderCircle className="spin" aria-hidden="true" size={17} /> : null}
            {busy ? "正在处理…" : confirmLabel}
          </button>
        </footer>
      </div>
    </div>
  );
}

export function ConfirmDialog({ request, onCancel, onConfirm }: {
  request: ConfirmDialogRequest;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <DialogShell
      open
      title={request.title}
      description={request.description}
      tone={request.tone}
      cancelLabel={request.cancelLabel}
      confirmLabel={request.confirmLabel}
      onCancel={onCancel}
      onConfirm={onConfirm}
    />
  );
}

export function DecisionDialog({ request, onCancel, onConfirm }: {
  request: AnyDecisionRequest;
  onCancel: () => void;
  onConfirm: (value: string) => void;
}) {
  const [selected, setSelected] = useState<string | null>(null);

  return (
    <DialogShell
      open
      title={request.title}
      description={request.description}
      tone={request.tone}
      cancelLabel={request.cancelLabel ?? "稍后决定"}
      confirmLabel={request.confirmLabel}
      confirmDisabled={!selected}
      onCancel={onCancel}
      onConfirm={() => selected && onConfirm(selected)}
    >
      <fieldset className="decision-options">
        <legend className="sr-only">请选择要使用的配置</legend>
        {request.options.map((option) => {
          const checked = option.value === selected;
          const OptionIcon = option.icon === "cloud" ? Cloud : Monitor;
          return (
            <label key={option.value} className={`decision-option${checked ? " selected" : ""}`}>
              <input
                type="radio"
                name="dialog-decision"
                value={option.value}
                checked={checked}
                onChange={() => setSelected(option.value)}
              />
              <span className="decision-option-icon" aria-hidden="true"><OptionIcon size={20} /></span>
              <span className="decision-option-copy">
                <strong>{option.label}</strong>
                <span>{option.description}</span>
                {option.consequence ? <small>{option.consequence}</small> : null}
              </span>
            </label>
          );
        })}
      </fieldset>
    </DialogShell>
  );
}

export function useDialogs() {
  const [active, setActive] = useState<QueuedDialog | null>(null);
  const activeRef = useRef<QueuedDialog | null>(null);
  const queueRef = useRef<QueuedDialog[]>([]);
  const nextIdRef = useRef(1);
  const mountedRef = useRef(true);

  const activate = useCallback((dialog: QueuedDialog | null) => {
    activeRef.current = dialog;
    if (mountedRef.current) setActive(dialog);
  }, []);

  const enqueue = useCallback((request: AnyDialogRequest) => {
    const cancelledResult = request.kind === "confirm" ? false : null;
    if (!mountedRef.current) return Promise.resolve<DialogResult>(cancelledResult);
    if (request.dedupeKey) {
      const pending = [activeRef.current, ...queueRef.current].some(
        (dialog) => dialog?.request.dedupeKey === request.dedupeKey
      );
      if (pending) return Promise.resolve<DialogResult>(cancelledResult);
    }

    return new Promise<DialogResult>((resolve) => {
      const dialog = { id: nextIdRef.current++, request, resolve };
      if (activeRef.current) queueRef.current.push(dialog);
      else activate(dialog);
    });
  }, [activate]);

  const settle = useCallback((id: number, value: DialogResult) => {
    const current = activeRef.current;
    if (!current || current.id !== id) return;
    activeRef.current = null;
    current.resolve(value);
    activate(queueRef.current.shift() ?? null);
  }, [activate]);

  useEffect(() => () => {
    mountedRef.current = false;
    const pending = [activeRef.current, ...queueRef.current].filter((item): item is QueuedDialog => Boolean(item));
    activeRef.current = null;
    queueRef.current = [];
    for (const dialog of pending) {
      dialog.resolve(dialog.request.kind === "confirm" ? false : null);
    }
  }, []);

  const confirm = useCallback((request: ConfirmRequest) => (
    enqueue({ ...request, kind: "confirm" }).then((value) => value === true)
  ), [enqueue]);

  const decide = useCallback(<T extends string>(request: DecisionRequest<T>) => (
    enqueue({ ...request, kind: "decision", options: request.options as readonly DecisionOption<string>[] })
      .then((value) => typeof value === "string" ? value as T : null)
  ), [enqueue]);

  const node = active ? (
    active.request.kind === "confirm" ? (
      <ConfirmDialog
        key={active.id}
        request={active.request}
        onCancel={() => settle(active.id, false)}
        onConfirm={() => settle(active.id, true)}
      />
    ) : (
      <DecisionDialog
        key={active.id}
        request={active.request}
        onCancel={() => settle(active.id, null)}
        onConfirm={(value) => settle(active.id, value)}
      />
    )
  ) : null;

  return { confirm, decide, node };
}
