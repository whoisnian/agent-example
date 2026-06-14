import { create } from "zustand";

/**
 * Theme preference state (local UI state → Zustand, not React Query).
 *
 * Persisted to `localStorage` under the plain key `theme` as one of
 * `"light" | "dark" | "system"` — a plain string (NOT a JSON blob) so the inline
 * FOUC-safe boot script in `index.html` can read and resolve it with the exact
 * same rules before first paint. The boot script and this store MUST stay in
 * sync on: the storage key, the accepted values, and the resolution rule
 * (`system` → `prefers-color-scheme`). See `index.html` and design D1/D2.
 *
 * Both `localStorage` and `window.matchMedia` access are fully guarded so the
 * store never throws on import in a non-browser / jsdom test environment (where
 * `matchMedia` is undefined) — keeping the existing contract-test suite green.
 */
export type ThemePreference = "light" | "dark" | "system";
export type ResolvedTheme = "light" | "dark";

export const THEME_STORAGE_KEY = "theme";

function readStoredPreference(): ThemePreference {
  try {
    const v = localStorage.getItem(THEME_STORAGE_KEY);
    if (v === "light" || v === "dark" || v === "system") return v;
  } catch {
    /* localStorage unavailable (privacy mode) → fall through to default */
  }
  return "system";
}

function systemPrefersDark(): boolean {
  try {
    return (
      typeof window !== "undefined" &&
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches
    );
  } catch {
    return false;
  }
}

/** Resolve a preference to the concrete appearance. Shared rule with the boot script. */
export function resolveTheme(pref: ThemePreference): ResolvedTheme {
  if (pref === "system") return systemPrefersDark() ? "dark" : "light";
  return pref;
}

/** Apply the resolved appearance to <html> (`.dark` present iff dark). */
function applyResolved(pref: ThemePreference): void {
  try {
    document.documentElement.classList.toggle("dark", resolveTheme(pref) === "dark");
  } catch {
    /* no document (SSR/test without DOM) — nothing to apply */
  }
}

function persistPreference(pref: ThemePreference): void {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, pref);
  } catch {
    /* localStorage unavailable → preference is session-only */
  }
}

export interface ThemeState {
  /** The stored preference the toggle reflects (light/dark/system). */
  theme: ThemePreference;
  /** The currently-resolved appearance (system → light|dark). */
  resolved: ResolvedTheme;
  /** Set the preference: persist, apply to <html>, and update state. */
  setTheme: (pref: ThemePreference) => void;
  /** Re-resolve after an OS change while preference is `system`. */
  syncResolved: () => void;
}

const initialPreference = readStoredPreference();

export const useThemeStore = create<ThemeState>()((set, get) => ({
  // The inline boot script already applied the class before first paint; the
  // store initializes to the SAME decision (same key + rules) and does NOT
  // re-apply on init, avoiding a second flash (design D2).
  theme: initialPreference,
  resolved: resolveTheme(initialPreference),
  setTheme: (pref) => {
    persistPreference(pref);
    applyResolved(pref);
    set({ theme: pref, resolved: resolveTheme(pref) });
  },
  syncResolved: () => {
    if (get().theme !== "system") return;
    applyResolved("system");
    set({ resolved: resolveTheme("system") });
  },
}));

/**
 * Subscribe to OS color-scheme changes; only repaints while the preference is
 * `system`. Call once at app start (main.tsx). Returns an unsubscribe fn.
 * Guarded so it is a safe no-op where `matchMedia` is unavailable (jsdom).
 */
export function initThemeSystemSync(): () => void {
  try {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
      return () => {};
    }
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = (): void => useThemeStore.getState().syncResolved();
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  } catch {
    return () => {};
  }
}
