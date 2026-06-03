import { create } from "zustand";
import { persist, createJSONStorage } from "zustand/middleware";
import type { AuthUser } from "./types";

export interface AuthState {
  token: string | null;
  user: AuthUser | null;
  /** Establish a session from a successful login. The only non-null writer. */
  setSession: (token: string, user: AuthUser) => void;
  /** Clear the whole session (token + user). The only clearer. */
  logout: () => void;
}

/**
 * Auth store — token + authenticated principal. Persisted to `localStorage`
 * under `auth.token`.
 *
 * Read synchronously by `apiFetch` and `realtimeClient` at request time
 * (no React subscription), per design D4 / D12.
 */
export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      token: null,
      user: null,
      setSession: (token, user) => set({ token, user }),
      logout: () => set({ token: null, user: null }),
    }),
    {
      name: "auth.token",
      version: 1,
      storage: createJSONStorage(() => localStorage),
      // Persist the session fields only — actions are not serialized.
      partialize: (state) => ({ token: state.token, user: state.user }),
      // v0 blobs persisted only `{ token }` and predate server-side JWT
      // enforcement; those tokens can no longer be honored. Drop the legacy
      // session so a returning user re-logs in, rather than rehydrating into a
      // half-populated `{ token, user: undefined }` state (see design D2/S4).
      migrate: () => ({ token: null, user: null }),
    },
  ),
);
