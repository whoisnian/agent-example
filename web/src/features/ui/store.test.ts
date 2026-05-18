import { beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "@/features/ui/store";

describe("ui store — toasts", () => {
  beforeEach(() => {
    useUiStore.setState({ toasts: [] });
  });

  it("pushToast assigns an id and appends to the list", () => {
    const id = useUiStore.getState().pushToast({ level: "error", message: "oops" });
    expect(id).toMatch(/^t-/);
    const ts = useUiStore.getState().toasts;
    expect(ts).toHaveLength(1);
    expect(ts[0]?.id).toBe(id);
    expect(ts[0]?.message).toBe("oops");
  });

  it("dismissToast removes by id", () => {
    const id = useUiStore.getState().pushToast({ level: "info", message: "hi" });
    useUiStore.getState().dismissToast(id);
    expect(useUiStore.getState().toasts).toHaveLength(0);
  });

  it("clearToasts empties the queue", () => {
    useUiStore.getState().pushToast({ level: "info", message: "a" });
    useUiStore.getState().pushToast({ level: "info", message: "b" });
    useUiStore.getState().clearToasts();
    expect(useUiStore.getState().toasts).toHaveLength(0);
  });
});
