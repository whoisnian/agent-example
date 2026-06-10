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

  // Three-column shell layout state (local UI state → Zustand, not React Query).
  /** Left navigation column collapsed to an icon rail / hidden. */
  navCollapsed: boolean;
  /** Right Artifact Preview column collapsed / drawer-closed. */
  previewCollapsed: boolean;
  /** Version id anchoring the right preview panel; null = nothing selected. */
  selectedVersionId: string | null;
  /** Artifact id previewed in the right panel; null = list only. Shared by the
   *  conversation turns and the panel rows (one selection, two entry points). */
  selectedArtifactId: string | null;
  toggleNav: () => void;
  togglePreview: () => void;
  setNavCollapsed: (v: boolean) => void;
  setPreviewCollapsed: (v: boolean) => void;
  setSelectedVersionId: (id: string | null) => void;
  setSelectedArtifactId: (id: string | null) => void;
  /** Atomic pair write (conversation artifact card): selects the version AND
   *  the artifact, and expands the preview column if it was collapsed. */
  selectArtifact: (versionId: string, artifactId: string) => void;
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

  navCollapsed: false,
  previewCollapsed: false,
  selectedVersionId: null,
  selectedArtifactId: null,
  toggleNav: () => set((s) => ({ navCollapsed: !s.navCollapsed })),
  togglePreview: () => set((s) => ({ previewCollapsed: !s.previewCollapsed })),
  setNavCollapsed: (v) => set({ navCollapsed: v }),
  setPreviewCollapsed: (v) => set({ previewCollapsed: v }),
  // Invariant: a lone version change (e.g. the detail page re-anchoring to a
  // new current_version after iterate/rollback) clears the artifact selection
  // so it can never dangle across versions. Only selectArtifact writes both.
  setSelectedVersionId: (id) =>
    set((s) =>
      id === s.selectedVersionId
        ? { selectedVersionId: id }
        : { selectedVersionId: id, selectedArtifactId: null },
    ),
  setSelectedArtifactId: (id) => set({ selectedArtifactId: id }),
  selectArtifact: (versionId, artifactId) =>
    set({
      selectedVersionId: versionId,
      selectedArtifactId: artifactId,
      previewCollapsed: false,
    }),
}));
