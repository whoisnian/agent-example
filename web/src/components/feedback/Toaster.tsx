import type { JSX } from "react";
import { useEffect } from "react";
import { useUiStore, type Toast } from "@/features/ui/store";

const LEVEL_CLASS: Record<Toast["level"], string> = {
  success: "border-success text-text",
  error: "border-danger text-text",
  warning: "border-warning text-text",
  info: "border-border text-text",
};

function ToastItem({ toast }: { toast: Toast }): JSX.Element {
  const dismiss = useUiStore((s) => s.dismissToast);
  const duration = toast.durationMs ?? 5000;

  useEffect(() => {
    if (duration <= 0) return;
    const t = setTimeout(() => dismiss(toast.id), duration);
    return (): void => clearTimeout(t);
  }, [toast.id, duration, dismiss]);

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>): void => {
    if (e.key === "Escape") dismiss(toast.id);
  };

  return (
    <div
      role="status"
      tabIndex={0}
      onKeyDown={onKeyDown}
      data-testid={`toast-${toast.level}`}
      className={[
        "min-w-64 max-w-96 rounded border bg-surface p-3 shadow-md outline-none",
        LEVEL_CLASS[toast.level],
      ].join(" ")}
    >
      <div className="flex items-start justify-between gap-3">
        <span className="text-sm">{toast.message}</span>
        <button
          type="button"
          onClick={(): void => dismiss(toast.id)}
          aria-label="Dismiss toast"
          className="text-xs text-text-muted hover:text-text"
        >
          x
        </button>
      </div>
    </div>
  );
}

export function Toaster(): JSX.Element {
  const toasts = useUiStore((s) => s.toasts);
  return (
    <div
      className="pointer-events-none fixed bottom-4 right-4 z-20 flex flex-col gap-2"
      data-testid="toaster"
    >
      {toasts.map((t) => (
        <div key={t.id} className="pointer-events-auto">
          <ToastItem toast={t} />
        </div>
      ))}
    </div>
  );
}
