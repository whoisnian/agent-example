import { create } from "zustand";
import { persist, createJSONStorage } from "zustand/middleware";

export interface AuthState {
  token: string | null;
  setToken: (token: string | null) => void;
}

/**
 * Auth store — token only. Persisted to `localStorage` under `auth.token`.
 *
 * Read synchronously by `apiFetch` and `realtimeClient` at request time
 * (no React subscription), per design D4 / D12.
 */
export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      token: null,
      setToken: (token) => set({ token }),
    }),
    {
      name: "auth.token",
      storage: createJSONStorage(() => localStorage),
      // Only persist the token — actions are not serialized.
      partialize: (state) => ({ token: state.token }),
    },
  ),
);
