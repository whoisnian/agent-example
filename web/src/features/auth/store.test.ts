import { describe, expect, it, beforeEach } from "vitest";
import { useAuthStore } from "@/features/auth/store";

const USER = { id: "u1", tenant_id: "t1", email: "dev@example.com" } as const;

describe("auth store", () => {
  beforeEach(() => {
    window.localStorage.clear();
    useAuthStore.setState({ token: null, user: null });
  });

  it("persists the session (token + user) to localStorage", async () => {
    useAuthStore.getState().setSession("abc", USER);
    // zustand's persist middleware writes after a microtask
    await Promise.resolve();
    const raw = window.localStorage.getItem("auth.token");
    expect(raw).toBeTruthy();
    const parsed = JSON.parse(raw ?? "{}");
    expect(parsed.state.token).toBe("abc");
    expect(parsed.state.user).toEqual(USER);
  });

  it("logout clears token, user, and the persisted session", async () => {
    useAuthStore.getState().setSession("abc", USER);
    await Promise.resolve();
    useAuthStore.getState().logout();
    await Promise.resolve();
    expect(useAuthStore.getState().token).toBeNull();
    expect(useAuthStore.getState().user).toBeNull();
    const parsed = JSON.parse(window.localStorage.getItem("auth.token") ?? "{}");
    expect(parsed.state.token).toBeNull();
    expect(parsed.state.user).toBeNull();
  });

  it("migrates a legacy token-only (v0) blob to logged-out", async () => {
    window.localStorage.setItem(
      "auth.token",
      JSON.stringify({ state: { token: "legacy" }, version: 0 }),
    );
    await useAuthStore.persist.rehydrate();
    expect(useAuthStore.getState().token).toBeNull();
    expect(useAuthStore.getState().user).toBeNull();
  });
});
