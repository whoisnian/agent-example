import { describe, expect, it, beforeEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PreviewColumn } from "@/components/layout/PreviewColumn";
import { useUiStore } from "@/features/ui/store";

describe("PreviewColumn (right column shell)", () => {
  beforeEach(() => {
    useUiStore.setState({ previewCollapsed: false });
  });

  it("renders its children and an aria-hidden=false aside when expanded", () => {
    render(
      <PreviewColumn>
        <div data-testid="content">body</div>
      </PreviewColumn>,
    );
    expect(screen.getByTestId("content")).toBeInTheDocument();
    const aside = screen.getByTestId("preview-column");
    expect(aside).toHaveAttribute("aria-hidden", "false");
    // A backdrop exists for the small-screen drawer; no re-open button yet.
    expect(screen.getByTestId("preview-backdrop")).toBeInTheDocument();
    expect(screen.queryByTestId("preview-open")).toBeNull();
  });

  // The close control now lives in the panel's header toolbar (see
  // ArtifactPreviewPanel.test); the column only reacts to the store flag.
  it("collapsing via the store hides the column and exposes a re-open affordance", () => {
    render(
      <PreviewColumn>
        <div>body</div>
      </PreviewColumn>,
    );
    act(() => useUiStore.getState().togglePreview());
    expect(useUiStore.getState().previewCollapsed).toBe(true);
    expect(screen.getByTestId("preview-column")).toHaveAttribute(
      "aria-hidden",
      "true",
    );
    expect(screen.getByTestId("preview-open")).toBeInTheDocument();
    expect(screen.queryByTestId("preview-backdrop")).toBeNull();
  });

  it("re-open affordance expands the column again", async () => {
    useUiStore.setState({ previewCollapsed: true });
    render(
      <PreviewColumn>
        <div>body</div>
      </PreviewColumn>,
    );
    await userEvent.click(screen.getByTestId("preview-open"));
    expect(useUiStore.getState().previewCollapsed).toBe(false);
  });
});
