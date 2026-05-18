import { describe, expect, it, beforeEach } from "vitest";
import { useAuthStore } from "@/features/auth/store";

describe("auth store", () => {
  beforeEach(() => {
    window.localStorage.clear();
    useAuthStore.setState({ token: null });
  });

  it("persists token to localStorage", async () => {
    useAuthStore.getState().setToken("abc");
    // zustand's persist middleware writes after a microtask
    await Promise.resolve();
    const raw = window.localStorage.getItem("auth.token");
    expect(raw).toBeTruthy();
    expect(JSON.parse(raw ?? "{}").state.token).toBe("abc");
  });

  it("setToken(null) clears the token", () => {
    useAuthStore.getState().setToken("abc");
    useAuthStore.getState().setToken(null);
    expect(useAuthStore.getState().token).toBeNull();
  });
});
