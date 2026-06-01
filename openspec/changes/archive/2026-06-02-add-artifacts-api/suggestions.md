# Review: add-artifacts-api

Critical review of the four artifacts, grounded against the actual codebase. Each finding cites what was verified.

---

## 1. [blocker] `kind` scenario values contradict the actual worker write path

- **Where**: `specs/artifacts-api/spec.md` — "List artifacts of an owned version" scenario; `design.md` Open Question #2.
- **Issue**: The list scenario asserts `kind = "report"` and `kind = "bundle"`, and the presign scenario uses `oss_key = "t/v/report/index.md"`. But the worker only ever writes `kind="file"`. Verified in `worker/worker/agents/base.py:142-150`: `insert_artifact(..., kind="file", ...)` is hard-coded for every produced file — there is no code path emitting `report`/`bundle`/`image` today. The proposal/`design.md` D-Open-Question #2 even says "`kind` is currently free-text from the worker" yet the spec scenario invents taxonomy values that do not exist. A reviewer/implementer writing the contract test against `report`/`bundle` would be encoding fiction.
- **Suggested change**: Change the scenario fixtures to `kind = "file"` (the only value the worker produces), or explicitly note the values are illustrative free-text and the endpoint returns `kind` verbatim regardless of value. Keep the ordering assertion (two rows, `created_at ASC, id ASC`) which is the real contract. The presign scenario's `oss_key` should also reflect the real layout `{tenant}/{task}/{version}/{type}/{filename}` (ARCHITECTURE §3.5 line 253) — e.g. `t/v/file/index.md` — though since `oss_key` is never serialized this is cosmetic.
- **Confidence**: high — verified against `agents/base.py` and `persistence.py::insert_artifact`.

## 2. [blocker] No SeaweedFS container fixture exists in `api/`; tasks 5.3/6.2 assume one that the worker tests do not even use

- **Where**: `tasks.md` 5.3, 6.2; `design.md` Risks ("an integration test against the SeaweedFS fixture container").
- **Issue**: Task 5.3 says "Integration test against the SeaweedFS fixture container" and 6.2 says "`make test-integration` (testcontainers: PG + SeaweedFS) green". Verified: the API module has **no** S3/OSS container fixture — `api/go.mod` only pulls `testcontainers-go/modules/postgres`, and the only API integration tests (`tasks_integration_test.go`, `task_cost_reads_integration_test.go`, etc.) stand up Postgres only. Worse, the **worker** tests do not use SeaweedFS either — they use `MinioContainer` (`worker/tests/integration/conftest.py:113-123`, `testcontainers.minio`). SeaweedFS in this repo is only a docker-compose dev service (`docker-compose.dev.yml:81`, image `chrislusf/seaweedfs:4.26`, run as `weed mini`, S3 on container :8333 → host :9000). There is no testcontainers SeaweedFS module (the worker `pyproject.toml:53-55` explicitly notes "no dedicated `seaweedfs` module" and starts it as a generic container — but only in compose, not in the Python fixtures).
- **Suggested change**: This is a real scoping decision the design must make explicitly. Either (a) add a task to build a generic-container SeaweedFS fixture for the API integration suite (new Go testcontainers wiring + healthcheck on :8333 + bucket pre-create via `S3_BUCKET` env, mirroring the compose service) and budget for it — this is non-trivial and pushes the PR well past 500 lines; or (b) use a MinIO testcontainers Go module for the round-trip test (MinIO and SeaweedFS both speak S3 path-style; the worker already proves MinIO works as the test double) and reserve SeaweedFS for compose/manual; or (c) make the round-trip an `httptest`-mocked presigner unit test and explicitly defer the real-S3 round-trip. Pick one and write it down. As written, 5.3/6.2 are not actionable.
- **Confidence**: high — verified `api/go.mod`, API integration tests, and `worker/tests/integration/conftest.py`.

## 3. [important] `OSS_*` env key names will collide / diverge from the worker's established names

- **Where**: `proposal.md` Impact ("new `OSS_*` env keys"); `design.md` D7; `tasks.md` 1.2; `spec.md` "API OSS Client Configuration".
- **Issue**: The worker already defines a settled `OSS_*` env contract: `OSS_ENDPOINT`, `OSS_BUCKET`, `OSS_ACCESS_KEY_ID`, `OSS_ACCESS_KEY_SECRET` (verified `worker/worker/core/config.py:27-28,114-117` and `worker/README.md:24-27`, and `docker-compose.dev.yml` provisions `dev-access-key`/`dev-secret-key`). The proposal's D7/1.2 list a struct field `SecretAccessKey` and say "`OSS_*` env tags" without pinning the key name. If the API uses `OSS_SECRET_ACCESS_KEY` it silently diverges from the worker's `OSS_ACCESS_KEY_SECRET`, and an operator who set the worker's env will get an unconfigured API. Also the worker has no `OSS_REGION` / `OSS_USE_PATH_STYLE` / `OSS_PRESIGN_TTL`, so those are genuinely new — but the four shared keys must match exactly.
- **Suggested change**: In the spec's config requirement and D7/1.2, pin the exact env names and reuse the worker's: `OSS_ENDPOINT`, `OSS_BUCKET`, `OSS_ACCESS_KEY_ID`, `OSS_ACCESS_KEY_SECRET` (note: `..._KEY_SECRET`, not `..._SECRET_ACCESS_KEY`), plus new `OSS_REGION` (default `us-east-1`, matching `storage.py:68`), `OSS_USE_PATH_STYLE` (default `true`), `OSS_PRESIGN_TTL` (default `5m`). Add a scenario or task note asserting key-name parity with the worker.
- **Confidence**: high — verified worker config, README, and compose.

## 4. [important] Fail-closed `oss_unconfigured` error code lives only in design/tasks, not in the spec — and its altitude is unclear

- **Where**: `design.md` Migration Plan ("clear `500 oss_unconfigured`-style error"); `tasks.md` 1.4; `spec.md` (absent).
- **Issue**: The spec is the durable contract; it defines `version_not_found` / `artifact_not_found` / `invalid_input` but says nothing about what happens when OSS is unconfigured or the presign SDK call fails at request time. Design says "fail closed, logged once at startup" but tasks 1.4 says presign "return a clear `oss_unconfigured` error" at request time — these are two different failure points (startup-incomplete-config vs. request-time SDK error) and the spec captures neither. AGENTS.md §4.1 mandates the unified `{code, message, trace_id}` envelope for all errors, so any new error code belongs in the spec with a scenario.
- **Suggested change**: Add a spec requirement + scenario to the Presign endpoint: "WHEN OSS is not configured (or the presign operation fails) THEN the response MUST be HTTP `500` with `code = "oss_unconfigured"` (config-missing) or `code = "internal_error"` (transient SDK failure), credentials never echoed." Decide and document whether missing config is a hard startup failure (config `required:"true"`, like `DATABASE_URL`/`RABBITMQ_URL` in `config.go:34,43`) or a soft startup with per-request 500. The current `config.Config` uses `required:"true"` for hard deps; if OSS is optional-at-startup you are introducing a new pattern that should be justified in D7.
- **Confidence**: high — verified `config.go` required-field pattern and spec contents.

## 5. [important] No presign metric — violates AGENTS.md §7 ("every external call adds a metric/log field")

- **Where**: `tasks.md` 4.3 (slog only); `design.md` (no observability section).
- **Issue**: Presign mints a URL via an external SDK/OSS interaction. AGENTS.md §7 requires "each new external call adds at least one metric/log field." The API already has a clear metrics convention for outbound interactions: `MQPublishDuration` (HistogramVec) + `MQPublishFailures` (CounterVec) on the `Metrics` struct (verified `api/internal/infrastructure/observability/metrics.go:22-23`). Tasks only mention slog, no metric. A leaked-URL / OSS-down incident would be invisible.
- **Suggested change**: Add a task: introduce `OSSPresignTotal` (CounterVec by outcome) and/or `OSSPresignDuration` to the `Metrics` struct, increment on each presign (success/`oss_unconfigured`/error), and wire the unconfigured-at-startup gauge or a one-time warn log. Reflect it in the design Risks/observability note.
- **Confidence**: high — verified existing metrics conventions and AGENTS.md §7.

## 6. [important] `expires_at` semantics vs. SDK: the spec mandates `T + TTL` but `PresignGetObject` does not return the instant

- **Where**: `spec.md` "Presign TTL drives expires_at" scenario ("MUST equal `T + 300s`"); `design.md` Risks (clock skew); `tasks.md` 1.3 (`expiresAt = now + TTL`).
- **Issue**: `s3.PresignClient.PresignGetObject` returns only the URL (the expiry is encoded as `X-Amz-Expires` relative seconds inside the signed query, anchored to `X-Amz-Date`). The SDK does not hand back an absolute `expires_at`. So the handler must compute `expiresAt = now() + TTL` itself — which tasks 1.3 does — but the signature's own `X-Amz-Date` is set by the SDK at sign time, which may differ from the `now()` the handler captured by a few ms, and SeaweedFS validates against its own clock. The spec's `MUST equal T + 300s` to-the-second is therefore only true for the *handler-reported* value, not the actual OSS-enforced expiry. The design Risk acknowledges this ("advisory") but the spec scenario states it as an absolute MUST.
- **Suggested change**: Soften the spec scenario to "`data.expires_at` MUST equal the handler's mint instant + configured TTL (advisory; OSS is the authority on actual expiry — see design Clock-skew risk)." Add a task note that the handler captures one `now()` and uses it for both `expires_at` and (if the SDK allows injecting a signing time) the signature, to keep them aligned. Confirm whether the `s3.PresignClient` lets you pin the signing clock; if not, document the few-ms gap.
- **Confidence**: medium — high on the SDK behavior (PresignGetObject returns URL only), medium on SeaweedFS's exact `X-Amz-Expires` handling; verify against a real round-trip.

## 7. [important] `GetArtifactWithOwner` select-list is leaner than needed / inconsistent with `created_at` echo

- **Where**: `tasks.md` 2.1 vs. `design.md` D4 vs. `spec.md` presign response.
- **Issue**: D4 says the new query returns `oss_key, bytes, mime, sha256, tenant_id, user_id`. Task 2.1 says it selects `id, kind, oss_key, mime, bytes, sha256, created_at, tenant_id, user_id`. The presign response (`spec.md` line 39) is `{url, expires_at, bytes, mime, sha256}` — it does NOT include `kind`, `created_at`, or `id`. So task 2.1's select list is wider than both D4 and the response contract. Minor, but D4 and 2.1 should agree, and sqlc-generated structs should not carry unused columns into the DTO assembly (invites the very `oss_key`-leak struct-embedding risk D6/Risks warn about).
- **Suggested change**: Reconcile: have the query select exactly `oss_key, bytes, mime, sha256, tenant_id, user_id` (D4's list). Drop `id`/`kind`/`created_at` unless a scenario consumes them. Update task 2.1 to match D4.
- **Confidence**: high — cross-read D4, task 2.1, spec response shape.

## 8. [nice-to-have] Presign of an artifact whose OSS object was deleted/missing is unspecified

- **Where**: `spec.md` Presign endpoint (no scenario); `design.md` (not addressed).
- **Issue**: Presigning is a pure signing operation — it does NOT verify the object exists. A presigned URL for a row whose OSS object was lifecycle-expired or never uploaded (e.g. worker crashed between insert and upload — though `agents/base.py` inserts only after upload, the DB row could outlive the object via OSS lifecycle rules; ARCHITECTURE §3.5 line 254 sets `checkpoints/` cleanup but `artifacts/` has no documented TTL, so this is low-risk today). The API returns a 200 + URL; the browser then gets a 404/403 from OSS. The contract is silent on this.
- **Suggested change**: Add a one-line note to the Presign requirement: "The presign endpoint does not verify object existence; a URL for an artifact whose underlying object is missing returns 200 here and a 404 from OSS at download time. The client should surface the OSS error." Optionally add to design Risks.
- **Confidence**: medium — verified presign-doesn't-check-existence (SDK fact) and the absence of an `artifacts/` lifecycle rule.

## 9. [nice-to-have] Download UX: Content-Disposition / filename, HEAD, and Range not addressed

- **Where**: `spec.md` Presign endpoint; `design.md` Non-Goals.
- **Issue**: The presigned GET hands the browser a URL whose `Content-Disposition` and `Content-Type` come from what the worker set at upload time (or defaults). The worker's `insert_artifact` records `mime` but the upload (`storage.py::put`) may not set `ContentType`/`ContentDisposition` on the S3 object — meaning a downloaded report opens inline as `octet-stream` with a UUID-ish filename. The list/presign responses echo `mime` but there's no filename field. For an MVP "retrieve result" loop this matters for usability.
- **Suggested change**: Either (a) accept it as MVP and add an explicit Non-Goal "download filename/Content-Disposition come from the stored object metadata; nice filenames are post-MVP"; or (b) add `response-content-disposition` / `response-content-type` query-param overrides to the presigned GET (S3 supports these as signed query params) so the API can force `attachment; filename=...`. HEAD/Range are correctly out of scope (presigned GET supports Range natively at the OSS layer) — no action needed beyond a Non-Goal note.
- **Confidence**: medium — `storage.py` ContentType behavior not fully read; verify whether the worker sets object Content-Type on PUT before relying on (a).

## 10. [nice-to-have] D5 defer of `/uploads/sts` is well-bounded — keep, but tighten the "extends not rewires" claim

- **Where**: `design.md` D5; `proposal.md` deferred bullet.
- **Issue**: The deferral is justified and clearly scoped (STS assume-role vs. request-presigning is genuinely a different mechanism; no upload consumer exists in the MVP loop — confirmed: task creation is `prompt + params`, no file ingestion). This is good scoping. The only soft spot: D5 claims the new `infrastructure/oss` client is "structured so the STS change extends it rather than rewires it" without saying how. STS needs `aws-sdk-go-v2/service/sts` + `stscreds`, which is a different client than `s3.PresignClient`. Don't over-promise reuse.
- **Suggested change**: Soften D5's last sentence to "the `infrastructure/oss` package is the natural home for the future STS issuer; sharing endpoint/region/credential config, though STS uses a distinct SDK client." No structural over-design now.
- **Confidence**: high — verified no upload flow exists; SDK package knowledge.

## 11. [nice-to-have] PR-size budget is at real risk; suggest an explicit split plan

- **Where**: `tasks.md` overall; `design.md` D5 ("under the ~500-line PR budget").
- **Issue**: AGENTS.md §7 caps PRs at ~500 lines (excluding generated/test). This change adds: a new `infrastructure/oss` package (client + config wiring + fail-closed), config struct + tests, a new sqlc query (generated code excluded), DTOs, two read-service methods, application orchestration, two HTTP handlers + error-code wiring, ServerDeps + main.go wiring, **plus** (per finding #2) potentially a brand-new SeaweedFS/MinIO testcontainers fixture for Go. The non-test, non-generated surface alone (oss client + config + domain + http + wiring) is plausibly 400-500 lines before the new container fixture. With the fixture it exceeds budget.
- **Suggested change**: Add a note to `tasks.md` / proposal: if the SeaweedFS-fixture work (finding #2) is non-trivial, split it — land the read endpoints + unit/contract tests with a mocked presigner first, and the real-OSS round-trip integration test as a fast follow. Or confirm the fixture is reused from an existing helper (it is not, per finding #2).
- **Confidence**: medium — line estimate is inference; the fixture-cost driver is verified.

## 12. [nice-to-have] 400-before-404 ordering should be stated to match read-api

- **Where**: `spec.md` "Malformed version_id returns 400" / "Malformed artifact_id returns 400".
- **Issue**: The scenarios cover 400 (bad UUID) and 404 (unowned/unknown) independently but don't state ordering. The codebase's `parseUUIDParam` (verified `task_reads.go:179-186`) runs first in every handler and short-circuits with 400 before the service/ownership probe runs, so 400 always precedes 404. The cost handlers (`task_cost_reads.go:39-42`) follow the same parse-first pattern. The spec should make this explicit so the contract test asserts it, matching `add-task-read-api`.
- **Suggested change**: Add a sentence to both endpoints: "Path-param validation runs before ownership resolution; a malformed UUID returns `400 invalid_input` regardless of whether the resource exists or is owned." This matches the existing `parseUUIDParam` short-circuit.
- **Confidence**: high — verified handler ordering in `task_reads.go` / `task_cost_reads.go`.

## 13. [nice-to-have] Confirm the 400 code string: spec says `invalid_input`, which is correct

- **Where**: `spec.md` 400 scenarios; `tasks.md` 4.2.
- **Issue/positive**: The spec uses `code = "invalid_input"` for malformed UUIDs. Verified this is the correct, existing code — `writeInvalidInputField` (`task_reads.go:168-175`) emits `Code: "invalid_input"`, and `parseUUIDParam` calls it. Note: this is distinct from the generic-catalogue `invalid_argument` in `errors.go:96`. Task 4.2's phrasing "invalid UUID → `400 invalid_input`" is right. Just flagging to keep the implementer from reaching for `MapError`'s `KindInvalidArgument` (which yields `invalid_argument`) — the handler should use `parseUUIDParam`, not a domain error, for path-param validation.
- **Suggested change**: Add a one-line note to task 4.3: "UUID path validation uses the shared `parseUUIDParam` helper (emits `invalid_input`), not a domain error through `MapError`." Prevents code drift toward `invalid_argument`.
- **Confidence**: high — verified both code paths.

---

## Overall assessment

The proposal is **architecturally sound and well-scoped** at the design level: presigned-GET (not byte-proxy), single-object scope, owner-resolution-in-SQL-before-presign, never-serialize-`oss_key`, and the `/uploads/sts` deferral are all the right calls and faithfully reuse the `add-task-read-api` / `add-task-cost-api` plumbing (Owner value type, owner-scoped 404, unified envelope). The decisions section is genuinely strong.

The weaknesses are in **grounding against the real codebase**, where three things must be fixed before implementation:

1. **The integration-test fixture does not exist** (finding #2): there is no SeaweedFS testcontainers fixture in `api/`, and the worker uses MinIO, not SeaweedFS, in its tests. Tasks 5.3/6.2 are not actionable as written and are the main PR-budget risk.
2. **The `kind` taxonomy in the spec scenarios is fictional** (finding #1): the worker only ever writes `kind="file"`. The contract test would encode values that never occur.
3. **The `OSS_*` env contract must match the worker's existing names** (finding #3) — specifically `OSS_ACCESS_KEY_SECRET`, not `OSS_SECRET_ACCESS_KEY` — and the `oss_unconfigured` failure mode + a presign metric (findings #4, #5) belong in the spec, not just the design/tasks, to satisfy the envelope contract and AGENTS.md §7.

Everything else is tightening (expires_at semantics, query select-list parity, 400/404 ordering, download filename). Fix the top three and this is ready to implement.

---

## Resolution (applied 2026-06-02)

All 13 findings were verified against the code and **all accepted**. Two required explicit decisions:

- **#2 (test fixture)** → integration round-trip uses the Go testcontainers **MinIO** module (worker-proven S3 double; no SeaweedFS Go module exists). SeaweedFS stays compose-only. Split plan added (D5a): if the fixture is heavy, ship mocked-presigner coverage first and the round-trip as a fast follow. Updated: design D2a/D5a/Risks, tasks 5.3/6.2, proposal Impact.
- **#4 (unconfigured OSS)** → chose **required-at-startup** (`required:"true"`, like `DATABASE_URL`) over the soft per-request `oss_unconfigured` path — fail-fast, matches house pattern, removes a never-exercised branch. Request-time SDK failure → `500 internal_error`. Updated: spec config requirement (3 scenarios), design D7, Migration Plan, tasks 1.2/1.4/4.2.

Other applied changes:
- **#1** spec scenarios now use `kind = "file"` (the only value the worker writes); design Open Question reworded.
- **#3** env names pinned to the worker's exact contract (`OSS_ACCESS_KEY_SECRET`, not `..._SECRET_ACCESS_KEY`) across spec/design D7/proposal/tasks 1.2.
- **#5** added `OSSPresignTotal{outcome}` metric (design D8, tasks 4.5/5.4, proposal).
- **#6** `expires_at` softened to advisory in spec + design D2.
- **#7** `GetArtifactWithOwner` select-list trimmed to `oss_key, bytes, mime, sha256, tenant_id, user_id` (design D4, tasks 2.1).
- **#8** presign-doesn't-verify-existence note added to spec + design Risks.
- **#9** download filename / `Content-Disposition` added as an explicit Non-Goal with the post-MVP path.
- **#10** D5 "extends not rewires" softened to config-reuse-convenience.
- **#11** PR-size split plan captured as D5a + tasks 5.3 note.
- **#12** 400-before-404 ordering stated in both spec endpoints.
- **#13** task 4.3 notes `parseUUIDParam` (`invalid_input`), not `MapError`/`invalid_argument`.

Re-validated: `openspec validate add-artifacts-api --strict` → valid.
