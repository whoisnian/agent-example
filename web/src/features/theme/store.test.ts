import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useThemeStore, initThemeSystemSync, resolveTheme, THEME_STORAGE_KEY } from "./store";

/** Install a controllable matchMedia mock; returns a fn to flip the OS scheme. */
function mockMatchMedia(initialDark: boolean) {
  let matches = initialDark;
  const listeners = new Set<() => void>();
  window.matchMedia = ((query: string) => ({
    get matches() {
      return matches;
    },
    media: query,
    onchange: null,
    addEventListener: (_: string, cb: () => void) => listeners.add(cb),
    removeEventListener: (_: string, cb: () => void) => listeners.delete(cb),
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return (dark: boolean) => {
    matches = dark;
    listeners.forEach((cb) => cb());
  };
}

describe("theme store", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.classList.remove("dark");
    useThemeStore.setState({ theme: "system", resolved: "light" });
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it("setTheme persists, applies the class, and updates state", () => {
    useThemeStore.getState().setTheme("dark");
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(useThemeStore.getState()).toMatchObject({ theme: "dark", resolved: "dark" });

    useThemeStore.getState().setTheme("light");
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("light");
    expect(document.documentElement.classList.contains("dark")).toBe(false);
    expect(useThemeStore.getState()).toMatchObject({ theme: "light", resolved: "light" });
  });

  it("system preference resolves from prefers-color-scheme", () => {
    mockMatchMedia(true);
    expect(resolveTheme("system")).toBe("dark");
    useThemeStore.getState().setTheme("system");
    expect(useThemeStore.getState().resolved).toBe("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });

  it("follows OS changes live while preference is system", () => {
    const setOsDark = mockMatchMedia(false);
    const stop = initThemeSystemSync();
    useThemeStore.getState().setTheme("system");
    expect(useThemeStore.getState().resolved).toBe("light");

    setOsDark(true);
    expect(useThemeStore.getState().resolved).toBe("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);

    // A non-system preference must NOT follow the OS.
    useThemeStore.getState().setTheme("light");
    setOsDark(false);
    setOsDark(true);
    expect(useThemeStore.getState().resolved).toBe("light");
    stop();
  });

  it("degrades gracefully when localStorage throws", () => {
    const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(() => useThemeStore.getState().setTheme("dark")).not.toThrow();
    // Class still applied (session-only), state still updated.
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(useThemeStore.getState().theme).toBe("dark");
    spy.mockRestore();
  });

  it("does not throw when matchMedia is absent (jsdom default path)", () => {
    // @ts-expect-error force-remove for the guard path
    delete window.matchMedia;
    expect(() => resolveTheme("system")).not.toThrow();
    expect(resolveTheme("system")).toBe("light");
    const stop = initThemeSystemSync();
    expect(stop).toBeTypeOf("function");
    expect(() => stop()).not.toThrow();
  });

  it("reads the persisted preference on fresh load (reload)", async () => {
    localStorage.setItem(THEME_STORAGE_KEY, "dark");
    vi.resetModules();
    const fresh = await import("./store");
    expect(fresh.useThemeStore.getState().theme).toBe("dark");
  });
});
