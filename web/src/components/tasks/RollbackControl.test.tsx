import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RollbackControl } from "./RollbackControl";

const base = {
  branchDisabled: false,
  switchDisabled: false,
  pending: false,
  onRollback: () => {},
};

describe("RollbackControl", () => {
  it("disables both actions with reasons while the task is active", async () => {
    render(
      <RollbackControl
        {...base}
        branchDisabled
        branchReason="Task is busy"
        switchDisabled
        switchReason="Task is busy"
      />,
    );
    await userEvent.click(screen.getByTestId("rollback-button"));
    expect(screen.getByTestId("rollback-switch")).toBeDisabled();
    expect(screen.getByTestId("rollback-branch")).toBeDisabled();
    expect(screen.getByTestId("rollback-switch")).toHaveAttribute(
      "title",
      expect.stringContaining("busy"),
    );
  });

  it("disables only switch when the target version is not terminal", async () => {
    render(
      <RollbackControl
        {...base}
        switchDisabled
        switchReason="Can only switch to a finished version"
      />,
    );
    await userEvent.click(screen.getByTestId("rollback-button"));
    expect(screen.getByTestId("rollback-switch")).toBeDisabled();
    expect(screen.getByTestId("rollback-branch")).toBeEnabled();
    expect(screen.getByTestId("rollback-switch")).toHaveAttribute(
      "title",
      expect.stringContaining("finished"),
    );
  });

  it("fires onRollback('switch') when Switch is clicked", async () => {
    const onRollback = vi.fn();
    render(<RollbackControl {...base} onRollback={onRollback} />);
    await userEvent.click(screen.getByTestId("rollback-button"));
    await userEvent.click(screen.getByTestId("rollback-switch"));
    expect(onRollback).toHaveBeenCalledWith("switch");
  });

  it("reveals a prompt and submits branch with the typed text", async () => {
    const onRollback = vi.fn();
    render(<RollbackControl {...base} onRollback={onRollback} />);
    await userEvent.click(screen.getByTestId("rollback-button"));
    await userEvent.click(screen.getByTestId("rollback-branch"));
    await userEvent.type(screen.getByTestId("rollback-prompt"), "tweak the title");
    await userEvent.click(screen.getByTestId("rollback-submit"));
    expect(onRollback).toHaveBeenCalledWith("branch", "tweak the title");
  });

  it("submits branch with an empty prompt", async () => {
    const onRollback = vi.fn();
    render(<RollbackControl {...base} onRollback={onRollback} />);
    await userEvent.click(screen.getByTestId("rollback-button"));
    await userEvent.click(screen.getByTestId("rollback-branch"));
    await userEvent.click(screen.getByTestId("rollback-submit"));
    expect(onRollback).toHaveBeenCalledWith("branch", "");
  });

  it("disables both actions while a request is in flight", async () => {
    render(<RollbackControl {...base} pending />);
    await userEvent.click(screen.getByTestId("rollback-button"));
    expect(screen.getByTestId("rollback-switch")).toBeDisabled();
    expect(screen.getByTestId("rollback-branch")).toBeDisabled();
  });
});
