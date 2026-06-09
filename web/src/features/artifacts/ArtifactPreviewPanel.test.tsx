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

beforeEach(() => {
  assignSpy = vi.fn();
  Object.defineProperty(window, "location", {
    configurable: true,
    value: { href: realLocation.href, assign: assignSpy },
  });
  useUiStore.setState({ toasts: [], selectedVersionId: "ver-1" });
});

afterEach(() => {
  Object.defineProperty(window, "location", {
    configurable: true,
    value: realLocation,
  });
  useUiStore.setState({ toasts: [], selectedVersionId: null });
});

describe("ArtifactPreviewPanel", () => {
  it("shows a placeholder when no version is selected", () => {
    useUiStore.setState({ selectedVersionId: null });
    render(wrap());
    expect(screen.getByTestId("preview-no-version")).toBeInTheDocument();
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

  it("Download re-mints a fresh URL and navigates to OSS", async () => {
    let presignCalls = 0;
    server.use(
      http.get("http://localhost/api/v1/artifacts/:id/presign", ({ params }) => {
        presignCalls += 1;
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            url: `https://oss.test/download/${String(params["id"])}?n=${presignCalls}`,
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
    expect(assignSpy).toHaveBeenLastCalledWith("https://oss.test/download/art-1?n=1");

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
      http.get("https://oss.test/download/:id", () =>
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
      http.get("https://oss.test/download/:id", () => HttpResponse.text(big)),
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

  it("degrades to a single inline error when the text fetch fails (CORS/network)", async () => {
    server.use(
      http.get("https://oss.test/download/:id", () =>
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
            url: `https://oss.test/download/${String(params["id"])}?img=1`,
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
    expect(img).toHaveAttribute("src", "https://oss.test/download/art-img?img=1");
  });
});
