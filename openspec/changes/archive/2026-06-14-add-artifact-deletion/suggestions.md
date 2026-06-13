# Review: add-artifact-deletion

## Overall assessment

The proposal correctly diagnoses the root cause and the core design is sound: a separate `deletions` channel (not overloading `files`), an idempotent `oss_fs delete` op, a version-scoped hard delete, and a typed error instead of `internal` are all the right calls, and the resume/idempotency reasoning holds up against the actual code (inheritance is genuinely fresh-run-only, the pre-reservation seq scheme extends cleanly, and `f.get("content","")` returning `None` is accurately analysed). Two claims are wrong as written — "no web change needed" (the event log will render a raw `artifact_deleted` fallback row) and the implicit assumption that the worker loop can call `delete_artifact`/emit a typed error code through its current structure — and several tasks don't match the real dependency-injection seams. None of these are fatal, but the web claim and the error-code propagation mechanism are Blockers because the spec/tasks as written would ship a visible regression and an under-specified error path.

---

## Blockers

### B1 — "No web change needed" is wrong: `artifact_deleted` renders a raw fallback row in the event log
**Severity: Blocker**
**Applies to:** `proposal.md` (lines 14, 26, 37), `design.md` D8, `task-event-ingest/spec.md`, and the (claimed-absent) web scope.

The "no web change" claim is justified *only* for the terminal-gated products card — and that part is correct (`web-tasks-pages` refetches the artifact list on `status` frames only, ignoring `artifact`/`artifact_deleted` frames, and `GET /versions/{id}/artifacts` returns the already-hard-deleted set). But the per-turn **event log** is a second consumer the proposal never considers. In `web/src/components/tasks/EventLog.tsx`:

- `HIDDEN_KINDS = new Set(["title", "artifact"])` — `artifact` renders nothing.
- `processEvents = events.filter((e) => !HIDDEN_KINDS.has(e.kind) && e !== planEvent && e.kind !== "summary")` — `artifact_deleted` is **not** hidden, so it falls through to `ProcessRow`'s `default` case (line 127-135) and renders a literal `artifact_deleted` label plus a JSON-ish `preview(payload)` of `{path, version_id}`.

So every deletion produces a stray raw-JSON row in the historical and current turn event logs. This directly contradicts D8's "does not need a web handler for the feature to be correct." The minimum fix is a one-line web change: add `"artifact_deleted"` to `HIDDEN_KINDS` (symmetry with `artifact`, which is already hidden because products live in the aggregate card).

**Recommended change:** Drop the absolute "no web change required"; add a small web task + a `web-tasks-pages` scenario: an `artifact_deleted` event MUST NOT render a visible event-log row (added to `HIDDEN_KINDS`). Keep the (correct) products-card no-change justification separate from the event-log change.

### B2 — The typed-error code (`executor_output_invalid`) has no specified path from the agent to the emitted event
**Severity: Blocker**
**Applies to:** `design.md` D5; `worker-agent-orchestration/spec.md` "Executor Output Validation Is Not an Internal Fault"; `tasks.md` 5.2.

D5 says the agent should "surface it as a `kind="error"` event with a specific code … rather than letting a raw `ValueError` reach the consumer's catch-all as `code="internal"`," but neither design nor tasks specify the actual mechanism, and the current code has only one path that emits a dispatch `kind="error"`: the consumer catch-all at `consumer.py:323-328`, which **hardcodes** both the published `code="internal"` and `mark_run_terminal(error={"code":"internal"})`. `LoopAgent.run` catches `BaseException`, records a metric, and re-raises (base.py:170-172) — it does not emit the error event itself. So a raised `ExecutorOutputError` would still be flattened to `internal` unless the consumer is taught about it.

The established pattern is right there: `AgentNotImplementedError` is mapped via `isinstance(dispatch_exc, AgentNotImplementedError)` (consumer.py:312) to `code="unimplemented"` in both the event and `mark_run_terminal`. The deletion change must do the same.

**Recommended change:** In tasks 5.2 and D5, name the mechanism explicitly: define `ExecutorOutputError` (carrying `path`), add an `isinstance(dispatch_exc, ExecutorOutputError)` branch in `consumer.py` that publishes `kind="error"` with `code="executor_output_invalid"` (message naming the path) **and** passes `error={"code":"executor_output_invalid"}` to `mark_run_terminal` (both currently hardcode `internal`). Add a spec scenario asserting the consumer maps the typed error (not just "the run fails typed"). Also register `executor_output_invalid` as a known worker error code wherever `unimplemented`/`internal`/`deadline_exceeded` are documented, so it isn't a magic string.

---

## Important

### I1 — Tasks 4.1/4.2 don't match the loop's dependency-injection seams (no LLM tool, no `Persistence` handle in the loop)
**Severity: Important**
**Applies to:** `tasks.md` 4.1, 4.2; `design.md` D4; `proposal.md` Impact (`tools.py`).

The loop does **not** apply files via LLM tool-calling. `_run_step` iterates `result.get("files", [])` and calls the injected `write_file` callable directly (loop.py:347-348); `write_file=oss_write_file` is passed into `LoopAgent` at construction (code_agent.py:50, research_agent.py:50) and threaded to `run_agent_loop(write_file=...)`. The `deepagents` `build_deep_agent` tool path is unused in the MVP loop. So:

- Task 4.1 ("add a delete adapter … wire it into the agent alongside `write_file`") is ambiguous and risks implying a `deepagents` tool registration. What's actually needed is a **`delete_file` callable parameter on `run_agent_loop`/`LoopAgent`**, symmetric to `write_file`, wired in both agent constructors.
- Task 4.2 says the loop calls `delete_artifact(version_id, path)` directly — but the loop has **no `Persistence` handle**. Persistence reaches the loop only as the injected `persist_artifact` callable (loop.py:149, base.py:157). The row delete must be threaded the same way (e.g. a `delete_artifact` callable parameter, mirroring `persist_artifact`), so the loop stays infrastructure-free per the `Agent` protocol contract (base.py:63-65).

**Recommended change:** Rewrite 4.1/4.2 to mirror the existing seams: a `delete_file` callable (cost-metered, via the new `oss_fs delete`) and a `delete_artifact` callable, both injected through `LoopAgent` → `run_agent_loop` → `_run_step`, exactly as `write_file`/`persist_artifact` are today. Don't touch `build_deep_agent`.

### I2 — Same-step write+delete and delete-then-rewrite ordering is unspecified (real unique-index interaction)
**Severity: Important**
**Applies to:** `worker-agent-orchestration/spec.md` (deletion requirement); `design.md` D3.

D3 mentions "delete-then-rewrite within one run" frees the unique slots, but no requirement or scenario pins the **within-one-step** ordering when `files` and `deletions` name the same path, or when a later step rewrites a path an earlier step deleted. Given `insert_artifact` upserts on `(version_id, oss_key)` and the partial unique on `(version_id, path)`, the apply order matters: if a step both writes `x` and deletes `x`, applying writes-then-deletes yields "absent" while deletes-then-writes yields "present." The persisted artifact rows are written **before** the checkpoint (loop.py:199-202), and the proposal puts deletions in the same step boundary — the interleave with that persistence pass is undefined.

**Recommended change:** Specify a deterministic order (recommend: apply `files` writes + row upserts first, then `deletions` — so an in-step write+delete of the same path nets to deleted, matching the user's stated "delete" intent), and add a scenario for "a step whose `files` and `deletions` both name `p` ends with `p` absent." Also decide and state whether a path appearing in both is even valid input or a no-op-after-write.

### I3 — `oss_key` is needed for the OSS delete but must never reach the event payload — spec should make the asymmetry explicit
**Severity: Important**
**Applies to:** `oss_fs/handler.py` + `storage.py` task 1.1; `design.md` D6; `worker-agent-orchestration/spec.md`.

The spec correctly forbids `oss_key` in the `artifact_deleted` payload (good — matches the never-serialize-oss_key discipline and the `artifact` event). But the delete still needs the absolute key internally, and the new `OssClient.delete(prefix, key)` must run the **same** `_normalize_key` prefix-safety guard as `put`/`get`/`server_side_copy` (storage.py:35-55) so a relative `path` from executor JSON can't escape the run namespace via `..`. Task 1.1 says "applying the same prefix-safety guard" but the spec text (worker-execution-runtime) only covers the DB delete, not the OSS prefix guard.

**Recommended change:** Add an explicit clause/scenario (worker-execution-runtime or the orchestration spec) that the OSS delete MUST reject a `path` escaping `ctx.oss_prefix` identically to `put`/`get`, and that the absolute key is used only for the S3 call and never serialized.

### I4 — Cost metering of the delete op is asserted in tasks but absent from the spec
**Severity: Important**
**Applies to:** `tasks.md` 4.1 ("cost-metered like the write adapter"); `design.md`; AGENTS.md §4.2 ("all LLM/tool calls must go through `cost_meter`").

`oss_write_file` is wrapped with `cost_metered_tool("oss_fs")` (tools.py:21) so every write emits `cost.tool`. AGENTS.md §4.2 mandates the same for *all* tool calls. Task 4.1 mentions cost-metering, but no spec requirement captures it, so it could be dropped during implementation without violating the spec. A delete that bypasses the meter is a (small) cost-accounting hole and a §4.2 red-line nick.

**Recommended change:** Add a one-line spec requirement (worker-agent-orchestration or worker-execution-runtime) that the delete op is emitted through the same `cost_metered_tool("oss_fs")` wrapper as the write, so it produces a `cost.tool` event.

---

## Nice-to-have

### N1 — Event-log consistency: the inherited file's `artifact` event remains, now matched by `artifact_deleted`
**Severity: Nice-to-have**
**Applies to:** `design.md` D8 (the "honest replay" justification).

D8's stated reason for emitting the event at all (keeping the `task_events` replay honest, so an `artifact` add has a matching removal) is sound and worth keeping — but note the inherited file's `artifact` event is *hidden* in the web log (`HIDDEN_KINDS`), so the "honest replay" benefit is purely at the persisted-event-log level, not the UI. That's fine, but it weakens the "emit it for the UI's future live view" framing. Once B1 hides `artifact_deleted` too, the event is purely for `task_events`/replay completeness. State that plainly so a future reader doesn't re-add UI rendering expecting it to show.

### N2 — Ingest task 7.1 over-states the work: the no-transition behavior is already automatic
**Severity: Nice-to-have**
**Applies to:** `tasks.md` 7.1; `task-event-ingest/spec.md`.

Verified in Go: `versionTargetStatus` (status.go:81-94) returns `("", false)` for any kind other than `status`/`error`, so `artifact_deleted` already commits the event row and transitions nothing via the existing generic path (event_sync.go:130-136). The events-ingested metric is already labelled by kind automatically (`EventsIngestedTotal.WithLabelValues(in.Kind)`, event_ingest.go:84). So 7.1 needs **no new Go branch** — only the spec scenario (already present) and possibly a test. Reword 7.1 to "confirm (with a test) that `artifact_deleted` flows through the existing generic persist-only path with no new dispatch branch," rather than implying new handling/labels to add. This is a small de-scoping that keeps the change honest about its true Go footprint.

### N3 — Decide the no-op-delete summary wording now, not "at implementation"
**Severity: Nice-to-have**
**Applies to:** `design.md` Open Questions (step-summary wording).

Leaving the no-op-delete summary note as an open question invites inconsistent behavior. Since the run summary is deterministic and assembled from step summaries (loop.py:120-134), a non-deterministic note risks churn. Recommend deciding: stay silent (simplest, and the user's "delete" intent is already satisfied by absence) rather than injecting a synthetic line. Close the open question before apply.
