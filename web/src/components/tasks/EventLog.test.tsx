import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import type { EventItem } from "@/features/tasks/types";
import { EventLog } from "./EventLog";

function ev(id: number, kind: string, payload: unknown): EventItem {
  return {
    id,
    version_id: "ver-1",
    run_id: "r",
    seq: id,
    kind,
    payload,
    created_at: "2026-05-26T00:00:00Z",
  };
}

describe("EventLog split-block rendering", () => {
  it("renders the summary as its own answer card (prose, no JSON, separate from process)", () => {
    render(
      <EventLog
        events={[
          ev(1, "plan", { steps: ["a", "b"] }),
          ev(2, "step", { verdict: "advance", title: "do a" }),
          ev(3, "summary", { summary: "Built the settings page." }),
        ]}
      />,
    );
    const summary = screen.getByTestId("event-summary");
    expect(summary).toHaveTextContent("Built the settings page.");
    expect(summary.textContent).not.toContain("{");
    // The summary card is distinct from the plan card and the process list.
    expect(screen.getByTestId("event-plan")).toBeInTheDocument();
    expect(screen.getByTestId("event-process")).toBeInTheDocument();
    // Summary is NOT inside the process list.
    expect(screen.getByTestId("event-process").contains(summary)).toBe(false);
  });

  it("renders the plan as its own ordered-list card", () => {
    render(<EventLog events={[ev(1, "plan", { steps: ["scaffold", "style", "test"] })]} />);
    const plan = screen.getByTestId("event-plan");
    expect(plan).toHaveTextContent("scaffold");
    expect(plan).toHaveTextContent("test");
    // No process card when there are only plan events.
    expect(screen.queryByTestId("event-process")).toBeNull();
  });

  it("renders step progress inside the process card with title and summary", () => {
    render(
      <EventLog
        events={[
          ev(1, "step", { verdict: "advance", title: "write css", summary: "added style.css" }),
        ]}
      />,
    );
    const process = screen.getByTestId("event-process");
    const row = within(process).getByTestId("event-row");
    expect(row).toHaveAttribute("data-kind", "step");
    expect(row).toHaveTextContent("write css");
    expect(row).toHaveTextContent("added style.css");
  });

  it("hides artifact events entirely (products live in the aggregate card)", () => {
    render(
      <EventLog
        events={[
          ev(1, "artifact", { artifact_id: "a9", path: "index.html" }),
          ev(2, "status", { status: "running" }),
        ]}
      />,
    );
    // No artifact row anywhere; only the status row renders in the process card.
    expect(screen.queryByText("index.html")).toBeNull();
    const rows = screen.getAllByTestId("event-row");
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveAttribute("data-kind", "status");
  });

  it("hides artifact_deleted events entirely (no raw fallback row)", () => {
    render(
      <EventLog
        events={[
          ev(1, "artifact_deleted", { path: "styles.css", version_id: "ver-1" }),
          ev(2, "status", { status: "running" }),
        ]}
      />,
    );
    // No fallback/JSON row for the deletion; only the status row renders.
    expect(screen.queryByText("styles.css")).toBeNull();
    expect(screen.queryByText(/artifact_deleted/)).toBeNull();
    const rows = screen.getAllByTestId("event-row");
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveAttribute("data-kind", "status");
  });

  it("hides non-conversational kinds (title)", () => {
    render(
      <EventLog events={[ev(1, "title", { title: "My task" }), ev(2, "log", { message: "hi" })]} />,
    );
    const rows = screen.getAllByTestId("event-row");
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveAttribute("data-kind", "log");
  });

  it("renders an error as a destructive process row naming the code", () => {
    render(
      <EventLog
        events={[ev(1, "error", { code: "deadline_exceeded", message: "ran too long" })]}
      />,
    );
    const row = within(screen.getByTestId("event-process")).getByTestId("event-row");
    expect(row).toHaveAttribute("data-kind", "error");
    expect(row).toHaveTextContent("deadline_exceeded");
  });

  it("falls back to compact JSON for an unknown kind and a malformed plan", () => {
    render(<EventLog events={[ev(1, "mystery", { a: 1 }), ev(2, "plan", { notsteps: true })]} />);
    // No dedicated plan card (malformed); both land as fallback process rows.
    expect(screen.queryByTestId("event-plan")).toBeNull();
    const rows = within(screen.getByTestId("event-process")).getAllByTestId("event-row");
    expect(rows.every((r) => r.getAttribute("data-kind") === "fallback")).toBe(true);
  });

  it("shows a truncation hint when the page is capped", () => {
    render(<EventLog events={[ev(1, "status", { status: "running" })]} truncated />);
    expect(screen.getByTestId("event-log-truncated")).toBeInTheDocument();
  });

  it("renders the empty state for no events", () => {
    render(<EventLog events={[]} />);
    expect(screen.getByTestId("event-log-empty")).toBeInTheDocument();
  });
});
