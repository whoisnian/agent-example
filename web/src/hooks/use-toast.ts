import { useUiStore, type Toast } from "@/features/ui/store";

type Payload = string | (Omit<Toast, "id" | "level"> & { message: string });

function toToast(level: Toast["level"], p: Payload): Omit<Toast, "id"> {
  if (typeof p === "string") return { level, message: p };
  return { level, ...p };
}

export function useToast(): {
  success: (p: Payload) => string;
  error: (p: Payload) => string;
  info: (p: Payload) => string;
  warning: (p: Payload) => string;
  dismiss: (id: string) => void;
} {
  const push = useUiStore((s) => s.pushToast);
  const dismiss = useUiStore((s) => s.dismissToast);
  return {
    success: (p): string => push(toToast("success", p)),
    error: (p): string => push(toToast("error", p)),
    info: (p): string => push(toToast("info", p)),
    warning: (p): string => push(toToast("warning", p)),
    dismiss,
  };
}
