import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ControlBar } from "./ControlBar";

describe("ControlBar", () => {
  it("enables pause/cancel and disables resume for a running task", () => {
    render(<ControlBar status="running" pending={false} onAction={() => {}} />);
    expect(screen.getByTestId("control-pause")).toBeEnabled();
    expect(screen.getByTestId("control-cancel")).toBeEnabled();
    expect(screen.getByTestId("control-resume")).toBeDisabled();
  });

  it("enables resume/cancel and disables pause for a paused task", () => {
    render(<ControlBar status="paused" pending={false} onAction={() => {}} />);
    expect(screen.getByTestId("control-resume")).toBeEnabled();
    expect(screen.getByTestId("control-cancel")).toBeEnabled();
    expect(screen.getByTestId("control-pause")).toBeDisabled();
  });

  it.each(["succeeded", "failed", "cancelled"])("disables all actions in terminal %s", (status) => {
    render(<ControlBar status={status} pending={false} onAction={() => {}} />);
    expect(screen.getByTestId("control-pause")).toBeDisabled();
    expect(screen.getByTestId("control-resume")).toBeDisabled();
    expect(screen.getByTestId("control-cancel")).toBeDisabled();
  });

  it("exposes a reason via title on a disabled action", () => {
    render(<ControlBar status="running" pending={false} onAction={() => {}} />);
    // Resume is disabled on a running task — it must explain why.
    expect(screen.getByTestId("control-resume")).toHaveAttribute(
      "title",
      expect.stringContaining("paused"),
    );
  });

  it("disables every action while a request is in flight", () => {
    render(<ControlBar status="running" pending={true} onAction={() => {}} />);
    expect(screen.getByTestId("control-pause")).toBeDisabled();
    expect(screen.getByTestId("control-resume")).toBeDisabled();
    expect(screen.getByTestId("control-cancel")).toBeDisabled();
  });

  it("invokes onAction with the clicked action", async () => {
    const onAction = vi.fn();
    render(<ControlBar status="running" pending={false} onAction={onAction} />);
    await userEvent.click(screen.getByTestId("control-pause"));
    expect(onAction).toHaveBeenCalledWith("pause");
  });
});
