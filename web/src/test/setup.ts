import "@testing-library/jest-dom/vitest";
import { afterAll, afterEach, beforeAll } from "vitest";
import { server } from "./mocks/server";

// Tests resolve relative paths through this base; matches scaffold convention.
process.env["VITE_API_BASE_URL"] ??= "http://localhost";

// jsdom lacks ResizeObserver / scrollIntoView, which Radix popper-positioned
// components (DropdownMenu) require to open. Minimal no-op polyfills.
if (typeof globalThis.ResizeObserver === "undefined") {
  class ResizeObserverStub {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }
  globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;
}
if (typeof window !== "undefined" && !window.HTMLElement.prototype.scrollIntoView) {
  window.HTMLElement.prototype.scrollIntoView = () => {};
}
// jsdom has no matchMedia; the theme store + DropdownMenu-bearing components
// touch it. Minimal stub (no-op listeners, defaults to light / not-dark).
if (typeof window !== "undefined" && typeof window.matchMedia !== "function") {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}

beforeAll(() => {
  server.listen({ onUnhandledRequest: "error" });
});

afterEach(() => {
  server.resetHandlers();
  // Clear browser storage between tests so the auth store doesn't leak.
  try {
    window.localStorage.clear();
  } catch {
    /* jsdom may not be initialized in some test files */
  }
});

afterAll(() => {
  server.close();
});
