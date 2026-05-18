import "@testing-library/jest-dom/vitest";
import { afterAll, afterEach, beforeAll } from "vitest";
import { server } from "./mocks/server";

// Tests resolve relative paths through this base; matches scaffold convention.
process.env["VITE_API_BASE_URL"] ??= "http://localhost";

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
