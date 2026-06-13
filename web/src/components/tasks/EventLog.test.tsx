import { afterEach, describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useUiStore } from "@/features/ui/store";
import type { EventItem } from "@/features/tasks/types";
import { EventLog } from "./EventLog";

function ev(id: number, kind: string, payload: unknown): EventItem {
  return { id, version_id: "ver-1", run_id: "r", seq: id, kind, payload, created_at: "2026-05-26T00:00:00Z" };
}

afterEach(() => {
  useUiStore.setState({ selectedVersionId: null, selectedArtifactId: null, previewCollapsed: false });
});

describe("EventLog per-kind rendering", () => {
  it("renders a summary event as plain assistant prose (no JSON, no kind label)", () => {
    render(<EventLog events={[ev(1, "summary", { summary: "Built the settings page." })]} />);
    const row = screen.getByTestId("event-row");
    expect(row).toHaveAttribute("data-kind", "summary");
    expect(row).toHaveTextContent("Built the settings page.");
    expect(row.textContent).not.toContain("{");
  });

  it("renders a plan as an ordered step list", () => {
    render(<EventLog events={[ev(1, "plan", { steps: ["scaffold", "style", "test"] })]} />);
    const items = screen.getAllByRole("listitem");
    expect(items.some((li) => li.textContent === "scaffold")).toBe(true);
    expect(screen.getByText("test")).toBeInTheDocument();
  });

  it("renders a step with its title and summary", () => {
    render(<EventLog events={[ev(1, "step", { verdict: "advance", title: "write css", summary: "added style.css" })]} />);
    const row = screen.getByTestId("event-row");
    expect(row).toHaveAttribute("data-kind", "step");
    expect(row).toHaveTextContent("write css");
    expect(row).toHaveTextContent("added style.css");
  });

  it("renders an artifact event as a selectable file line driving the preview", async () => {
    render(<EventLog events={[ev(1, "artifact", { artifact_id: "a9", path: "index.html" })]} />);
    const btn = screen.getByTestId("event-artifact");
    expect(btn).toHaveTextContent("index.html");
    await userEvent.click(btn);
    const s = useUiStore.getState();
    expect(s.selectedVersionId).toBe("ver-1");
    expect(s.selectedArtifactId).toBe("a9");
  });

  it("de-duplicates repeated artifact events by artifact_id", () => {
    render(
      <EventLog
        events={[
          ev(1, "artifact", { artifact_id: "a1", path: "index.html" }),
          ev(2, "artifact", { artifact_id: "a1", path: "index.html" }),
        ]}
      />,
    );
    expect(screen.getAllByTestId("event-artifact")).toHaveLength(1);
  });

  it("hides non-conversational kinds (title)", () => {
    render(<EventLog events={[ev(1, "title", { title: "My task" }), ev(2, "status", { status: "running" })]} />);
    // Only the status row renders; the title produces no row.
    const rows = screen.getAllByTestId("event-row");
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveAttribute("data-kind", "status");
  });

  it("renders an error with destructive styling naming the code", () => {
    render(<EventLog events={[ev(1, "error", { code: "deadline_exceeded", message: "ran too long" })]} />);
    const row = screen.getByTestId("event-row");
    expect(row).toHaveAttribute("data-kind", "error");
    expect(row).toHaveTextContent("deadline_exceeded");
  });

  it("falls back to compact JSON for an unknown kind and for a malformed plan", () => {
    render(
      <EventLog
        events={[ev(1, "mystery", { a: 1 }), ev(2, "plan", { notsteps: true })]}
      />,
    );
    const rows = screen.getAllByTestId("event-row");
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
