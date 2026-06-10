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

describe("ui store — three-column layout state", () => {
  beforeEach(() => {
    useUiStore.setState({
      navCollapsed: false,
      previewCollapsed: false,
      selectedVersionId: null,
    });
  });

  it("defaults: both columns expanded, no version selected", () => {
    const s = useUiStore.getState();
    expect(s.navCollapsed).toBe(false);
    expect(s.previewCollapsed).toBe(false);
    expect(s.selectedVersionId).toBeNull();
  });

  it("toggleNav / togglePreview flip their flags", () => {
    useUiStore.getState().toggleNav();
    expect(useUiStore.getState().navCollapsed).toBe(true);
    useUiStore.getState().togglePreview();
    expect(useUiStore.getState().previewCollapsed).toBe(true);
    useUiStore.getState().toggleNav();
    expect(useUiStore.getState().navCollapsed).toBe(false);
  });

  it("setNavCollapsed / setPreviewCollapsed set explicit values", () => {
    useUiStore.getState().setNavCollapsed(true);
    useUiStore.getState().setPreviewCollapsed(true);
    expect(useUiStore.getState().navCollapsed).toBe(true);
    expect(useUiStore.getState().previewCollapsed).toBe(true);
  });

  it("setSelectedVersionId anchors and clears the preview", () => {
    useUiStore.getState().setSelectedVersionId("v-1");
    expect(useUiStore.getState().selectedVersionId).toBe("v-1");
    useUiStore.getState().setSelectedVersionId(null);
    expect(useUiStore.getState().selectedVersionId).toBeNull();
  });
});

describe("ui store — artifact selection invariant", () => {
  beforeEach(() => {
    useUiStore.setState({
      previewCollapsed: false,
      selectedVersionId: null,
      selectedArtifactId: null,
    });
  });

  it("selectArtifact writes the pair atomically and expands the preview", () => {
    useUiStore.setState({ previewCollapsed: true });
    useUiStore.getState().selectArtifact("v-1", "a-1");
    const s = useUiStore.getState();
    expect(s.selectedVersionId).toBe("v-1");
    expect(s.selectedArtifactId).toBe("a-1");
    expect(s.previewCollapsed).toBe(false);
  });

  it("a lone version change clears the artifact selection (no dangling)", () => {
    useUiStore.getState().selectArtifact("v-1", "a-1");
    // e.g. the detail page re-anchoring to a new current_version after iterate.
    useUiStore.getState().setSelectedVersionId("v-2");
    expect(useUiStore.getState().selectedVersionId).toBe("v-2");
    expect(useUiStore.getState().selectedArtifactId).toBeNull();
  });

  it("re-setting the same version keeps the artifact selection", () => {
    useUiStore.getState().selectArtifact("v-1", "a-1");
    useUiStore.getState().setSelectedVersionId("v-1");
    expect(useUiStore.getState().selectedArtifactId).toBe("a-1");
  });

  it("setSelectedArtifactId switches the artifact within the version", () => {
    useUiStore.getState().selectArtifact("v-1", "a-1");
    useUiStore.getState().setSelectedArtifactId("a-2");
    expect(useUiStore.getState().selectedVersionId).toBe("v-1");
    expect(useUiStore.getState().selectedArtifactId).toBe("a-2");
  });
});
