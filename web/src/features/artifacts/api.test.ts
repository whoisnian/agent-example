import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/mocks/server";
import { artifactFixture } from "@/test/mocks/handlers";
import { getArtifactPresign, getVersionArtifacts } from "./api";

describe("getVersionArtifacts", () => {
  it("parses the list in server order and preserves present-and-null metadata", async () => {
    server.use(
      http.get("http://localhost/api/v1/versions/:id/artifacts", ({ params }) =>
        HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            version_id: String(params["id"]),
            artifacts: [
              artifactFixture("art-1", { mime: "text/markdown", bytes: 10 }),
              artifactFixture("art-2", { mime: null, bytes: null, sha256: null }),
            ],
          },
          trace_id: "t",
        }),
      ),
    );
    const res = await getVersionArtifacts("ver-1");
    expect(res.version_id).toBe("ver-1");
    expect(res.artifacts.map((a) => a.id)).toEqual(["art-1", "art-2"]);
    // Nulls are present, not omitted.
    const second = res.artifacts[1]!;
    expect(second.mime).toBeNull();
    expect(second.bytes).toBeNull();
    expect(second.sha256).toBeNull();
  });

  it("parses the owned-but-empty version as an empty array (not 404)", async () => {
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
    const res = await getVersionArtifacts("ver-empty");
    expect(res.artifacts).toEqual([]);
  });
});

describe("getArtifactPresign", () => {
  it("parses the presign result with echoed metadata", async () => {
    const res = await getArtifactPresign("art-1");
    expect(res.url).toMatch(/^https:\/\/oss\.test\/download\/art-1/);
    expect(res.expires_at).toBe("2026-05-26T00:05:00Z");
    expect(res.bytes).toBe(12_288);
    expect(res.mime).toBe("text/markdown");
  });
});
