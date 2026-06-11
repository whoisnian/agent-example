import type { JSX } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { server } from "@/test/mocks/server";
import { useUiStore } from "@/features/ui/store";
import {
  ArtifactPreviewPanel,
  TEXT_PREVIEW_CAP_BYTES,
} from "./ArtifactPreviewPanel";

function wrap(): JSX.Element {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <ArtifactPreviewPanel />
    </QueryClientProvider>
  );
}

const realLocation = window.location;
let assignSpy: ReturnType<typeof vi.fn>;

/** Install a clipboard stub (jsdom has none); returns the writeText spy. */
function stubClipboard(): ReturnType<typeof vi.fn> {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  return writeText;
}

beforeEach(() => {
  assignSpy = vi.fn();
  Object.defineProperty(window, "location", {
    configurable: true,
    value: { href: realLocation.href, assign: assignSpy },
  });
  useUiStore.setState({
    toasts: [],
    selectedVersionId: "ver-1",
    selectedArtifactId: null,
    previewCollapsed: false,
  });
});

afterEach(() => {
  Object.defineProperty(window, "location", {
    configurable: true,
    value: realLocation,
  });
   
  delete (navigator as any).clipboard;
  useUiStore.setState({
    toasts: [],
    selectedVersionId: null,
    selectedArtifactId: null,
    previewCollapsed: false,
  });
});

/** MSW override: the selected version lists a single text/html artifact. */
function htmlArtifactList(): void {
  server.use(
    http.get("http://localhost/api/v1/versions/:id/artifacts", ({ params }) =>
      HttpResponse.json({
        code: 0,
        message: "ok",
        data: {
          version_id: String(params["id"]),
          artifacts: [
            {
              id: "art-html",
              kind: "file",
              mime: "text/html",
              bytes: 512,
              sha256: null,
              created_at: "2026-05-26T00:00:00Z",
            },
          ],
        },
        trace_id: "t",
      }),
    ),
  );
}

describe("ArtifactPreviewPanel", () => {
  it("shows a placeholder when no version is selected", () => {
    useUiStore.setState({ selectedVersionId: null });
    render(wrap());
    expect(screen.getByTestId("preview-no-version")).toBeInTheDocument();
  });

  // --- header toolbar ---

  it("renders the toolbar in contentless states with a working close control", async () => {
    useUiStore.setState({ selectedVersionId: null });
    render(wrap());
    // Generic title fallback; Copy/Refresh disabled; close always reachable.
    expect(screen.getByTestId("preview-title")).toHaveTextContent("Artifact Preview");
    expect(screen.getByTestId("preview-copy")).toBeDisabled();
    expect(screen.getByTestId("preview-refresh")).toBeDisabled();

    await userEvent.click(screen.getByTestId("preview-close"));
    expect(useUiStore.getState().previewCollapsed).toBe(true);
  });

  it("shows the selected artifact's identity in the toolbar", async () => {
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId("artifact-select"),
    );
    // art-1 fixture: kind "file", mime "text/markdown".
    expect(screen.getByTestId("preview-title")).toHaveTextContent("text/markdown");
  });

  it("treats a dangling artifact selection as no-artifact-selected", async () => {
    useUiStore.setState({ selectedArtifactId: "art-gone" });
    render(wrap());
    await screen.findByTestId("artifact-list");
    // Not in this version's list → list-only rendering, generic title, no error.
    expect(screen.getByTestId("artifact-preview-hint")).toBeInTheDocument();
    expect(screen.getByTestId("preview-title")).toHaveTextContent("Artifact Preview");
  });

  it("panel rows write the shared store selection", async () => {
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId("artifact-select"),
    );
    expect(useUiStore.getState().selectedArtifactId).toBe("art-1");
  });

  // --- Copy ---

  it("copies the fully loaded text and confirms", async () => {
    const writeText = stubClipboard();
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/download", () => HttpResponse.text("copy me")),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId("artifact-select"),
    );
    await screen.findByTestId("artifact-preview-text");

    const copy = screen.getByTestId("preview-copy");
    expect(copy).toBeEnabled();
    await userEvent.click(copy);
    expect(writeText).toHaveBeenCalledWith("copy me");
    await waitFor(() =>
      expect(
        useUiStore.getState().toasts.some((t) => t.level === "success" && /copied/i.test(t.message)),
      ).toBe(true),
    );
  });

  it("refuses to copy truncated content (disabled with a reason)", async () => {
    stubClipboard();
    const big = "x".repeat(TEXT_PREVIEW_CAP_BYTES + 100);
    server.use(http.get("http://localhost/api/v1/artifacts/:id/download", () => HttpResponse.text(big)));
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId("artifact-select"),
    );
    await screen.findByTestId("artifact-preview-truncated");

    const copy = screen.getByTestId("preview-copy");
    expect(copy).toBeDisabled();
    expect(copy).toHaveAttribute("title", expect.stringContaining("download"));
  });

  it("disables Copy when the clipboard API is unavailable", async () => {
    // No stubClipboard() — jsdom has no navigator.clipboard.
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/download", () => HttpResponse.text("plain")),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId("artifact-select"),
    );
    await screen.findByTestId("artifact-preview-text");
    expect(screen.getByTestId("preview-copy")).toBeDisabled();
  });

  // --- Refresh ---

  it("Refresh re-mints the presigned URL and replays the preview", async () => {
    let presignCalls = 0;
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/presign", ({ params }) => {
        presignCalls += 1;
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            url: `/api/v1/artifacts/${String(params["id"])}/download?n=${presignCalls}`,
            expires_at: "2026-05-26T00:05:00Z",
            bytes: 10,
            mime: "text/markdown",
            sha256: null,
          },
          trace_id: "t",
        });
      }),
      http.get("http://localhost/api/v1/artifacts/:id/download", () => HttpResponse.text("v")),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId("artifact-select"),
    );
    await screen.findByTestId("artifact-preview-text");
    expect(presignCalls).toBe(1);

    await userEvent.click(screen.getByTestId("preview-refresh"));
    await screen.findByTestId("artifact-preview-text");
    await waitFor(() => expect(presignCalls).toBe(2));
  });

  // --- HTML rich preview ---

  it("renders an HTML artifact in a sandboxed iframe by default", async () => {
    htmlArtifactList();
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId("artifact-select"),
    );

    const frame = await screen.findByTestId("preview-html-frame");
    expect(frame.getAttribute("src")).toMatch(/^\/api\/v1\/artifacts\/art-html\/download/);
    // Scripts may run, but never with the app's origin.
    expect(frame).toHaveAttribute("sandbox", "allow-scripts");
    expect(frame.getAttribute("sandbox")).not.toContain("allow-same-origin");
  });

  it("toggles between rendered and source views without re-selecting", async () => {
    htmlArtifactList();
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/download", () =>
        HttpResponse.text("<html><body>hi</body></html>"),
      ),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId("artifact-select"),
    );
    await screen.findByTestId("preview-html-frame");

    // Prominent icon+label button: names the view it switches TO in each state.
    const toggle = screen.getByTestId("preview-view-toggle");
    expect(toggle).toHaveTextContent("Source");
    expect(toggle.querySelector("svg")).not.toBeNull();

    await userEvent.click(toggle);
    const text = await screen.findByTestId("artifact-preview-text");
    expect(text).toHaveTextContent("hi");
    expect(useUiStore.getState().selectedArtifactId).toBe("art-html");
    expect(screen.getByTestId("preview-view-toggle")).toHaveTextContent("Render");

    await userEvent.click(screen.getByTestId("preview-view-toggle"));
    expect(await screen.findByTestId("preview-html-frame")).toBeInTheDocument();
  });

  it("a rendered-view presign failure shows an inline error (no iframe, no toast)", async () => {
    htmlArtifactList();
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/presign", () =>
        HttpResponse.json(
          { code: "internal_error", message: "boom", data: null, trace_id: "t" },
          { status: 500 },
        ),
      ),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId("artifact-select"),
    );

    expect(await screen.findByTestId("preview-presign-error")).toBeInTheDocument();
    expect(screen.queryByTestId("preview-html-frame")).toBeNull();
    expect(useUiStore.getState().toasts).toHaveLength(0);
  });

  it("lists the selected version's artifacts in server order", async () => {
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    const rows = within(list).getAllByTestId("artifact-row");
    expect(rows.map((r) => r.getAttribute("data-artifact-id"))).toEqual([
      "art-1",
      "art-2",
    ]);
  });

  it("renders an empty state for an owned-but-empty version", async () => {
    server.use(
      http.get("http://localhost/api/v1/versions/:id/artifacts", ({ params }) =>
        HttpResponse.json({
          code: 0,
          message: "ok",
          data: { version_id: String(params["id"]), artifacts: [] },
          trace_id: "t",
        }),
      ),
    );
    render(wrap());
    expect(await screen.findByTestId("artifact-list-empty")).toBeInTheDocument();
  });

  it("Download re-mints a fresh URL and navigates to the same-origin download URL", async () => {
    let presignCalls = 0;
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/presign", ({ params }) => {
        presignCalls += 1;
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            url: `/api/v1/artifacts/${String(params["id"])}/download?n=${presignCalls}`,
            expires_at: "2026-05-26T00:05:00Z",
            bytes: 10,
            mime: "text/markdown",
            sha256: null,
          },
          trace_id: "t",
        });
      }),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    const firstRow = within(list).getAllByTestId("artifact-row")[0]!;
    const download = within(firstRow).getByTestId("artifact-download");

    await userEvent.click(download);
    await waitFor(() => expect(assignSpy).toHaveBeenCalledTimes(1));
    expect(assignSpy).toHaveBeenLastCalledWith("/api/v1/artifacts/art-1/download?n=1");

    await userEvent.click(download);
    await waitFor(() => expect(assignSpy).toHaveBeenCalledTimes(2));
    expect(presignCalls).toBe(2);
  });

  it("a presign failure surfaces exactly one error and does not navigate", async () => {
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/presign", () =>
        HttpResponse.json(
          { code: "artifact_not_found", message: "boom", data: null, trace_id: "t" },
          { status: 404 },
        ),
      ),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    const download = within(
      within(list).getAllByTestId("artifact-row")[0]!,
    ).getByTestId("artifact-download");

    await userEvent.click(download);
    await waitFor(() => expect(useUiStore.getState().toasts).toHaveLength(1));
    expect(assignSpy).not.toHaveBeenCalled();
  });

  it("previews a text artifact inline (fetched + rendered)", async () => {
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/download", () =>
        HttpResponse.text("# Hello from the artifact"),
      ),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    // art-1 is text/markdown → text-like preview.
    const selectBtn = within(
      within(list).getAllByTestId("artifact-row")[0]!,
    ).getByTestId("artifact-select");
    await userEvent.click(selectBtn);

    const body = await screen.findByTestId("artifact-preview-text");
    expect(body).toHaveTextContent("Hello from the artifact");
    expect(screen.queryByTestId("artifact-preview-truncated")).toBeNull();
  });

  it("truncates a text artifact larger than the byte cap", async () => {
    const big = "x".repeat(TEXT_PREVIEW_CAP_BYTES + 100);
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/download", () => HttpResponse.text(big)),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId(
        "artifact-select",
      ),
    );
    expect(
      await screen.findByTestId("artifact-preview-truncated"),
    ).toBeInTheDocument();
  });

  it("degrades to a single inline error when the text fetch fails (network)", async () => {
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/download", () =>
        HttpResponse.error(),
      ),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId(
        "artifact-select",
      ),
    );
    expect(
      await screen.findByTestId("artifact-preview-error"),
    ).toBeInTheDocument();
    // The degrade path is inline only — no toast.
    expect(useUiStore.getState().toasts).toHaveLength(0);
  });

  it("offers download-only for a binary (null-mime) artifact", async () => {
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    // art-2 has mime:null → binary branch, no inline preview, no presign needed.
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[1]!).getByTestId(
        "artifact-select",
      ),
    );
    expect(
      await screen.findByTestId("artifact-preview-binary"),
    ).toBeInTheDocument();
  });

  it("previews an image artifact via <img> from a presigned URL", async () => {
    server.use(
      http.get("http://localhost/api/v1/versions/:id/artifacts", ({ params }) =>
        HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            version_id: String(params["id"]),
            artifacts: [
              {
                id: "art-img",
                kind: "file",
                mime: "image/png",
                bytes: 2048,
                sha256: null,
                created_at: "2026-05-26T00:00:00Z",
              },
            ],
          },
          trace_id: "t",
        }),
      ),
      http.get("http://localhost/api/v1/artifacts/:id/presign", ({ params }) =>
        HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            url: `/api/v1/artifacts/${String(params["id"])}/download?img=1`,
            expires_at: "2026-05-26T00:05:00Z",
            bytes: 2048,
            mime: "image/png",
            sha256: null,
          },
          trace_id: "t",
        }),
      ),
    );
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    await userEvent.click(
      within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId(
        "artifact-select",
      ),
    );
    const imgWrap = await screen.findByTestId("artifact-preview-image");
    const img = within(imgWrap).getByRole("img");
    expect(img).toHaveAttribute("src", "/api/v1/artifacts/art-img/download?img=1");
  });
});
