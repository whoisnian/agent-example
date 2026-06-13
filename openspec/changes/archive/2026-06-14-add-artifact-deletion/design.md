## Context

Versions are immutable snapshots. When a user iterates (or rollback-branches) on an existing version, the worker's `inherit_parent_artifacts` (gated on `parent_version_id` present AND no prior checkpoint — fresh runs only) server-side-copies the parent's OSS objects into the new version's prefix and inserts an `artifacts` row + emits a `kind="artifact"` event per object, **before** the agent runs. From there the planner/executor/critic loop produces new files via `write_file` (which wraps `oss_fs(op="write")`).

There is no path to *remove* a file. The executor's JSON contract is `{summary, files:[{path, content}]}` and `oss_fs` exposes only `read`/`write`. When asked to "删除 styles.css" the model emits `{"path":"styles.css","content":null}`; because `f.get("content","")` returns `None` (the key is present with a null value, so the default never applies), `oss_fs(op="write", content=None)` raises `ValueError("oss_fs write requires content")`. That exception is uncaught through `_run_step` → `run_agent_loop` → dispatcher and is caught by `consumer.py`'s catch-all as a generic `internal` `dispatch_error`, marking the run `failed`.

Two defects compound: (1) **feature gap** — deletion is inexpressible; (2) **robustness gap** — a malformed executor output (an LLM mistake) is indistinguishable from a real infrastructure fault and nukes the whole run.

Constraints:
- Worker DB writes go through `persistence.py`'s `ALLOWED_WRITE_TABLES`; `artifacts` is currently "INSERT only". The code comment already mandates an OpenSpec change to alter this set (worker-execution-runtime). AGENTS.md §4.2 forbids the worker writing `tasks`/`task_versions`, but `artifacts` is worker-owned.
- `artifacts` enforces UNIQUE `(version_id, oss_key)` and partial UNIQUE `(version_id, path) WHERE path IS NOT NULL`.
- Event seq is resume-safe: every emitted event reserves a monotonic `seq` before the step checkpoint (loop.py "Resume-Safe Event Sequencing").
- The realtime fan-out binding is `event.#` (kind-agnostic), so a new event kind reaches the gateway and ingest with no topology change.

## Goals / Non-Goals

**Goals:**
- Let the executor delete a file (inherited or produced-this-run) and have it disappear from the version's persisted artifact set, OSS, and the live UI.
- Make deletion idempotent and resume-safe under at-least-once delivery and crash-resume.
- Stop a malformed executor file entry from being recorded as a generic `internal` run failure.
- Keep the existing write path (`files`) byte-for-byte unchanged (executor-input/output compatibility invariant).

**Non-Goals:**
- No deletion of *another* version's artifacts, no cross-version cascade. History is preserved at version granularity.
- No new public REST verb (no `DELETE /artifacts/...`). Deletion is an agent-loop operation, not a user-facing API.
- No DB migration. No soft-delete column.
- No directory/glob deletion semantics (`deletions` is an explicit list of exact relative paths). Wildcards are out of scope.
- No retry/repair loop that re-prompts the executor to fix malformed output (a clear typed failure is enough for MVP).

## Decisions

### D1 — Express deletion via a separate `deletions: [path,...]` list, not via `files`
The executor returns `{"summary": "...", "files": [...], "deletions": ["styles.css", ...]}`. `deletions` is an optional list of version-relative paths.

- **Why not overload `files` with `content: null` / `op: "delete"`?** Overloading would change how every `files` entry is interpreted and risk altering the write path's serialized form. A parallel list keeps `files` handling identical (compatibility invariant in loop.py `_execute`) and makes the two intents unambiguous.
- A `files` entry whose `content` is not a string remains **invalid** (see D5) — deletions must use `deletions`, documented in `code_system.md`.

### D2 — `oss_fs` gains an idempotent `delete` op
`oss_fs(op="delete", path=...)` removes the object under `ctx.oss_prefix`; deleting a missing key is a **success/no-op** (returns `{"path", "deleted": bool}`), mirroring the write-upsert's at-least-once tolerance. `content` is not consulted. The `plugin.yaml` input schema enum becomes `[write, read, delete]`.

- **Why idempotent?** A redelivered/resumed run may re-attempt the deletion; a no-op on a missing object keeps the operation safe to repeat.

### D3 — Hard-delete the new version's artifact row; no soft-delete, no migration
For each deletion the loop calls a new scoped `persistence.delete_artifact(version_id, path)` that issues `DELETE FROM artifacts WHERE version_id = $1 AND path = $2` and reports whether a row was removed.

- **Why hard delete is safe for history:** the new version owns its own rows under its own `version_id`; the parent's copy (a different `version_id`) is never touched. Rolling back to the parent still finds `styles.css`.
- **Why not a `deleted_at` soft-delete column?** It would force a migration, a filter (`WHERE deleted_at IS NULL`) in every artifact read (`ListArtifactsByVersion`, archive, preview), and a partial-index revision — all to retain a tombstone that has no consumer (history already lives in the parent version). Hard delete leaves `ListArtifactsByVersion` and the preview/archive routes unchanged.
- **Freeing the unique slots** (`(version_id, oss_key)`, `(version_id, path)`) lets the same run re-create the path later if it chooses (delete-then-rewrite within one run).

### D4 — Worker performs the deletion directly (symmetry with insert), gated by the allowlist
Artifact-row **inserts** are already done by the worker (`insert_artifact`), not by the API ingest. Keeping **deletes** worker-side preserves that symmetry and avoids a cross-service race (event-ordering vs. the direct insert). This requires relaxing `ALLOWED_WRITE_TABLES`'s `artifacts` discipline from "INSERT only" to "INSERT/upsert + DELETE scoped to `(version_id, path)`".

**Wiring respects the existing DI seams (no `Persistence` handle in the loop).** The loop is infrastructure-free: it applies file writes via an injected `write_file` callable (loop.py:143) and persists rows via an injected `persist_artifact` callable (loop.py:149) — it holds no `Persistence` or `OssClient` reference, and the MVP loop does **not** use `deepagents` LLM tool-calling for file ops. Deletion threads through the **same** seams: a `delete_file` callable (cost-metered, wrapping the new `oss_fs` `delete`) and a `delete_artifact` callable (wrapping the scoped persistence delete), both injected `LoopAgent` → `run_agent_loop` → `_run_step`, exactly as `write_file` / `persist_artifact` are today. `build_deep_agent` is not touched.

- **Alternative considered — ingest-driven delete:** have the worker only emit `artifact_deleted` and let the API ingest delete the row. Rejected: creation is direct-write while deletion would be event-driven — asymmetric, and the event could be processed before/independently of the row state, opening a window where the row exists but the file is gone. Worker-side keeps OSS-delete, row-delete, and event-emit in one ordered step.

### D5 — Malformed executor file entry fails distinctly, never as generic `internal`
At the loop boundary, a `files` entry whose `content` is missing or non-string is rejected with a typed `ExecutorOutputError` (carrying the offending path). **Mechanism (this is the part the first draft left implicit):** the consumer is the only place that emits a dispatch `kind="error"`, and today it hardcodes `code="internal"` in both the published event and `mark_run_terminal` (consumer.py:323-328). The established escape hatch is the `isinstance(dispatch_exc, AgentNotImplementedError)` branch that maps a typed exception to `code="unimplemented"` (consumer.py:312). So `ExecutorOutputError` gets an analogous `isinstance` branch in the consumer that publishes `kind="error"` with `code="executor_output_invalid"` (message naming the path) **and** passes that code to `mark_run_terminal`. `LoopAgent.run` keeps re-raising (base.py); it does not emit the error itself. `executor_output_invalid` is registered as a known worker error code, not a magic literal. `oss_fs(op="write")` keeps its own `content`-required guard as defense-in-depth, but the loop no longer lets that fire on a normal LLM mistake.

- **Why not coerce null content to `""`?** Writing an empty file silently does the wrong thing (the user asked to delete). Failing loud-but-typed is more honest and, combined with D1's `deletions` channel + the prompt update, the model has the correct path so this is a safety net, not the happy path.

### D6 — Emit `kind="artifact_deleted"` only when a row was actually removed
Ordering within a step mirrors the insert path: OSS delete → row delete → (if a row existed) emit `artifact_deleted` with a pre-reserved `seq`, **after** the step checkpoint. Payload: `{path, version_id}` (no `oss_key` — same never-serialize-oss_key discipline as the `artifact` event). If no row existed (already absent / never present in this version), no event is emitted — there is nothing for the UI to retract.

- The event's `seq` participates in the same pre-reservation scheme as the step + artifact events so resume leaves a harmless gap, never a `(run_id, seq)` collision.

### D7 — Ingest persists `artifact_deleted` with no state transition
`task-event-ingest` already inserts a `task_events` row for any `kind` (replay timeline). `artifact_deleted` adds no business-table transition (the worker already removed the row) — it is persisted and counted under the events-ingested metric labelled by `kind`. The realtime gateway forwards it unchanged (`event.#`).

### D8 — Web: products card needs no change, but the event log MUST hide the new kind (one line)
Two distinct web consumers, two different conclusions:

- **Products card — no change (correct as first claimed).** Per `web-tasks-pages`, the per-turn products card is withheld until the version is terminal, and the artifact-list query is invalidated/refetched **only on `status` frames**, never on `artifact` frames. At completion the card refetches `GET /versions/{id}/artifacts` from the DB, where the deleted row is already gone, so `styles.css` simply never appears. No work here.
- **Event log — one-line change (the first draft missed this).** `EventLog.tsx` hides only `HIDDEN_KINDS = {"title","artifact"}` and renders every *other* kind — including unknown ones — as a bounded compact payload preview (the `default` case). So `artifact_deleted` would render a stray raw-`{path, version_id}` row in every turn's log. Fix: add `"artifact_deleted"` to `HIDDEN_KINDS`, symmetric with `artifact` (file lifecycle belongs to the products card, not the conversation log). The `web-tasks-pages` "Conversation-Style Event Rendering" requirement is updated to name `artifact_deleted` alongside `artifact` as never-rendered, with a scenario.

- **Why emit the event at all, then?** It is **not** for the UI (both web surfaces hide it). It exists purely for `task_events` replay/event-log completeness: the persisted timeline already records the inherited file's `artifact` add, so a matching `artifact_deleted` keeps that timeline honest. Ingest persists it generically at near-zero cost. A future live per-file view could consume it, but nothing does today — state that plainly so no one re-adds UI rendering expecting it.

## Risks / Trade-offs

- **[Resume after a deletion]** A redelivered run re-runs inheritance (re-copies `styles.css`, re-inserts the row) only when there is no checkpoint; if the deletion step was already checkpointed, the deletion step is skipped on resume but the file was already removed and the row already gone, so the end state holds. If the crash happened *before* any checkpoint, inheritance re-runs AND the deletion re-runs (idempotent OSS delete + idempotent row delete) → same end state. → Mitigation: D2/D3 idempotency + the existing fresh-run inheritance gate make every interleaving converge.
- **[Duplicate/again event on resume]** Re-emitting `artifact_deleted` on a pre-checkpoint resume reuses the reserved seq scheme; ingest is idempotent on `(run_id, seq)`. → Mitigation: same resume-safe seq path as the artifact event.
- **[Allowlist relaxation widens worker write surface]** Permitting `DELETE` on `artifacts` is a real expansion of worker authority. → Mitigation: the delete is scoped to `(version_id, path)` of the *current run's* version (never the parent, never `tasks`/`task_versions`), enforced in the single `delete_artifact` method, and gated by this OpenSpec change as the code comment requires.
- **[Executor uses the wrong channel anyway]** The model might still emit `content: null`. → Mitigation: D5 turns that into a typed, diagnosable `executor_output_invalid` error instead of a run-killing `internal`, and the prompt + `deletions` channel steer it to the correct path.
- **[Delete a path that doesn't exist in this version]** No-op (no OSS object, no row, no event). → Acceptable: the user's intent ("not present") is already satisfied; the step summary should note it.

## Migration Plan

No DB migration. Deploy order is worker-then-API-then-web tolerant in either direction:
- Old API ingest already persists unknown kinds generically, so a worker emitting `artifact_deleted` before the API update is harmless (row already deleted by the worker; event stored).
- Old web ignores unknown event kinds (no handler) → the deletion still shows correctly on next load; the new handler only adds live retraction.
- Rollback: reverting the worker restores write-only behavior; any `artifact_deleted` rows already in `task_events` remain valid history and need no cleanup.

## Open Questions

_(Both prior open questions are now resolved — recorded here as decisions.)_

- **OSS delete primitive — RESOLVED.** `ctx.oss_client` (`storage.py`) has `put`/`get`/`server_side_copy`/`list_keys` but **no** `delete`. Adding a prefix-safe `delete(prefix, key) -> bool` is part of this change (task 1.1); it MUST run the identical `_normalize_key` prefix-safety guard as `put`/`get` so a relative executor path can't escape the run namespace.
- **No-op delete summary wording — RESOLVED: stay silent.** The run summary is a deterministic concatenation of step summaries; injecting a synthetic "nothing to delete" line risks non-deterministic churn. A delete of an absent path is simply a no-op — the user's "removed" intent is already satisfied by absence — so the executor summary says nothing special about it.
