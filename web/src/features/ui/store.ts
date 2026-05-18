import { create } from "zustand";

export type ToastLevel = "success" | "error" | "info" | "warning";

export interface Toast {
  id: string;
  level: ToastLevel;
  message: string;
  /** auto-dismiss after this many ms; default 5000. 0 = sticky. */
  durationMs?: number;
}

export interface UiState {
  toasts: Toast[];
  pushToast: (t: Omit<Toast, "id">) => string;
  dismissToast: (id: string) => void;
  clearToasts: () => void;
}

let counter = 0;
const nextId = (): string => {
  counter += 1;
  // Stable, monotonic IDs are easier to test than UUIDs and good enough for UI.
  return `t-${Date.now().toString(36)}-${counter.toString(36)}`;
};

export const useUiStore = create<UiState>()((set) => ({
  toasts: [],
  pushToast: (t) => {
    const id = nextId();
    set((s) => ({ toasts: [...s.toasts, { ...t, id }] }));
    return id;
  },
  dismissToast: (id) =>
    set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
  clearToasts: () => set({ toasts: [] }),
}));
