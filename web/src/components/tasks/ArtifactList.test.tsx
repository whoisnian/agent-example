import type { JSX } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { server } from "@/test/mocks/server";
import { useUiStore } from "@/features/ui/store";
import { ArtifactList } from "./ArtifactList";

function wrap(versionId = "ver-1"): JSX.Element {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <ArtifactList versionId={versionId} />
    </QueryClientProvider>
  );
}

// jsdom's window.location.assign is non-configurable (can't be spied), and real
// navigation is unimplemented — so swap window.location for a stub carrying a
// mock `assign`, then restore it. The stub is the observable for Download.
const realLocation = window.location;
let assignSpy: ReturnType<typeof vi.fn>;

beforeEach(() => {
  assignSpy = vi.fn();
  Object.defineProperty(window, "location", {
    configurable: true,
    value: { href: realLocation.href, assign: assignSpy },
  });
});

afterEach(() => {
  Object.defineProperty(window, "location", { configurable: true, value: realLocation });
  useUiStore.setState({ toasts: [] });
});

describe("ArtifactList", () => {
  it("renders the list (server order) once the lazy query resolves", async () => {
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    const rows = within(list).getAllByTestId("artifact-row");
    expect(rows.map((r) => r.getAttribute("data-artifact-id"))).toEqual(["art-1", "art-2"]);
  });

  it("renders the empty state for an owned-but-empty version (not an error)", async () => {
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
    expect(screen.queryByTestId("artifact-list-error")).not.toBeInTheDocument();
  });

  it("renders an error (no crash) on a list 404 and does not toast", async () => {
    server.use(
      http.get("http://localhost/api/v1/versions/:id/artifacts", () =>
        HttpResponse.json(
          { code: "version_not_found", message: "nope", data: null, trace_id: "t" },
          { status: 404 },
        ),
      ),
    );
    render(wrap());
    expect(await screen.findByTestId("artifact-list-error")).toBeInTheDocument();
    // 404 is meta.silent: no cache toast.
    await waitFor(() => expect(useUiStore.getState().toasts).toHaveLength(0));
  });

  it("renders placeholders for null mime / bytes", async () => {
    render(wrap());
    const list = await screen.findByTestId("artifact-list");
    const second = within(list).getAllByTestId("artifact-row")[1]!;
    // art-2 has mime:null, bytes:null → both shown as the neutral placeholder.
    expect(within(second).getAllByText("—")).toHaveLength(2);
  });

  it("Download mints a fresh URL and navigates to OSS; a second click re-mints", async () => {
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
    // Re-minted, not reused.
    expect(presignCalls).toBe(2);
    expect(assignSpy).toHaveBeenLastCalledWith("https://oss.test/download/art-1?n=2");
  });

  it.each([
    ["artifact_not_found", 404],
    ["internal_error", 500],
  ] as const)(
    "a presign %s surfaces exactly one error and does not navigate",
    async (code, status) => {
      server.use(
        http.get("http://localhost/api/v1/artifacts/:id/presign", () =>
          HttpResponse.json({ code, message: "boom", data: null, trace_id: "t" }, { status }),
        ),
      );
      render(wrap());
      const list = await screen.findByTestId("artifact-list");
      const download = within(within(list).getAllByTestId("artifact-row")[0]!).getByTestId(
        "artifact-download",
      );

      await userEvent.click(download);

      // Exactly one toast (component onError) — no transport/mutationCache double-toast.
      await waitFor(() => expect(useUiStore.getState().toasts).toHaveLength(1));
      expect(assignSpy).not.toHaveBeenCalled();
    },
  );
});
