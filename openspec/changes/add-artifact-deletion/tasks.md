## 1. OSS delete primitive

- [x] 1.1 Add `async def delete(self, prefix: str, key: str) -> bool` to the OSS client (`worker/worker/core/storage.py`), applying the same prefix-safety guard as `put`/`get`; return whether an object was removed, and treat a missing object as a successful no-op (return `False`, do not raise).
- [x] 1.2 Unit-test the client delete: deleting an existing key removes it and returns `True`; deleting a missing key returns `False` without raising; a path that would escape the prefix is rejected exactly as `put`/`get` reject it.

## 2. `oss_fs` tool gains `delete` op

- [x] 2.1 In `worker/worker/plugins/tool/oss_fs/handler.py`, add `op == "delete"`: call `ctx.oss_client.delete(ctx.oss_prefix, path)` and return `{"path": path, "deleted": bool}`; `content` is not consulted. Keep the existing `write` `content`-required guard unchanged.
- [x] 2.2 In `worker/worker/plugins/tool/oss_fs/plugin.yaml`, extend the `op` enum to `[write, read, delete]`.
- [x] 2.3 Tests: `oss_fs(op="delete")` on an existing key returns `{"deleted": true}` and removes it; on a missing key returns `{"deleted": false}` (idempotent, no raise).

## 3. Scoped artifact-row deletion in persistence

- [x] 3.1 Add `async def delete_artifact(self, *, version_id, path) -> bool` to `worker/worker/core/persistence.py` issuing `DELETE FROM artifacts WHERE version_id = $1 AND path = $2`; return whether a row was removed; idempotent (zero rows → `False`, no raise).
- [x] 3.2 Update the `ALLOWED_WRITE_TABLES` discipline comment for `artifacts` from "INSERT only" to "INSERT/upsert + scoped DELETE by `(version_id, path)`", referencing this change; ensure no code path can DELETE another table or an unscoped `artifacts` delete.
- [x] 3.3 Tests: `delete_artifact` removes exactly the `(version_id, path)` row and returns `True`; a non-existent `(version_id, path)` returns `False` without raising; a row under a different `version_id` is untouched.

## 4. Loop applies deletions + emits `artifact_deleted`

- [x] 4.1 In `worker/worker/agents/tools.py`, add a cost-metered delete adapter parallel to `oss_write_file` (e.g. `oss_delete_file(ctx, path) -> bool`) wrapping `oss_fs(op="delete")` via `cost_metered_tool("oss_fs")` so it emits `cost.tool`.
- [x] 4.2 Thread two new callables through the existing DI seams — **the loop holds no `Persistence`/`OssClient` handle**: add a `delete_file: DeleteFile` parameter and a `delete_artifact: DeleteArtifact | None` parameter to `run_agent_loop`/`LoopAgent` (symmetric to `write_file` / `persist_artifact`), wire `oss_delete_file` and a `LoopAgent._delete_artifact` (wrapping `persistence.delete_artifact`) in both `code_agent.py` and `research_agent.py`. Do NOT touch `build_deep_agent`.
- [x] 4.3 In `_run_step`/`_execute` result handling, parse the optional `deletions: [path,...]`. Apply `files` writes + row upserts **first**, then `deletions`: for each deletion path call `delete_file` then `delete_artifact(version_id, path)`; collect the paths whose row was actually removed. (Same-path in both → ends absent.)
- [x] 4.4 Reserve one `seq` per actually-removed deletion **before** the step checkpoint (same pre-reservation scheme as artifact events), and **after** the checkpoint emit one `kind="artifact_deleted"` event per removed path with payload `{path, version_id}` (no `oss_key`).
- [x] 4.5 Ensure resume-safety: deletions applied within the step boundary, idempotent on re-run (incl. crash after OSS-delete but before row-delete); a deletion of an absent path emits nothing and does not fail the step.

## 5. Robustness: malformed executor output maps to a typed code, not `internal`

- [x] 5.1 Define a typed `ExecutorOutputError(path)` and validate each `files` entry at the loop boundary: a missing/non-string `content` raises it (carrying the path) instead of reaching `oss_fs(op="write", content=None)`.
- [x] 5.2 Register `executor_output_invalid` as a known worker error code (alongside `internal`/`unimplemented`/`deadline_exceeded`). In `worker/core/consumer.py`, add an `isinstance(dispatch_exc, ExecutorOutputError)` branch **mirroring the `AgentNotImplementedError` branch** that publishes `kind="error"` with `code="executor_output_invalid"` (message naming the path) AND passes `error={"code":"executor_output_invalid"}` to `mark_run_terminal` — both currently hardcode `internal`. `LoopAgent.run` keeps re-raising; it does not emit the event itself.
- [x] 5.3 Tests: a `files` entry with `content: null` produces a `kind="error"` event AND a terminal record both coded `executor_output_invalid` (assert NOT `internal`); well-formed writes still pass and are byte-identical.

## 6. Executor prompt

- [x] 6.1 Update `worker/worker/agents/prompts/code_system.md`: document that file removals MUST be expressed via `deletions: [path,...]`, never by emitting empty/null `content`.
- [x] 6.2 If the executor subagent plugin carries its own `prompt.md` (worker-subagent-plugins), mirror the `deletions` guidance there so the resolved instruction matches.

## 7. API event ingest (verify, don't re-implement)

- [x] 7.1 Confirm `kind="artifact_deleted"` flows through the existing generic persist-only path: `versionTargetStatus` returns no transition for non-`status`/`error` kinds, and the events-ingested metric is already auto-labelled by kind (`EventsIngestedTotal.WithLabelValues(in.Kind)`). No new Go dispatch branch is expected — only confirm with a test.
- [x] 7.2 Tests: an `artifact_deleted` event inserts a `task_events` row and mutates no `artifacts`/`task_versions`/`tasks` row; a redelivered `(run_id, seq)` is a no-op; the kind-labelled metric increments.

## 7b. Web event log

- [x] 7b.1 In `web/src/components/tasks/EventLog.tsx`, add `"artifact_deleted"` to `HIDDEN_KINDS` (symmetric with `"artifact"`), so a deletion never renders a raw-JSON fallback row.
- [x] 7b.2 Test (Vitest/RTL): a turn whose events include an `artifact_deleted` event renders no event-log row for it (no compact payload preview).

## 8. Integration & docs

- [x] 8.1 Worker integration test: a fresh run that inherits `styles.css` + `index.html` and whose executor declares `deletions: ["styles.css"]` ends with the `styles.css` object and `(version_id, path)` row gone, an `artifact_deleted` event emitted, `index.html` intact, and the parent version's `styles.css` untouched.
- [ ] 8.2 Resume test: crash before the deletion step's checkpoint, redeliver, and assert convergence (object + row absent, no `(run_id, seq)` collision).
- [x] 8.2b Ordering test: a single step whose `files` and `deletions` both name `x` ends with `x` absent (write applied first, delete second).
- [x] 8.3 Update `docs/ARCHITECTURE.md` artifact/iteration section to note deletion semantics (version-scoped hard delete; parent preserved) if it currently implies write-only artifacts.
- [ ] 8.4 Manual verification: on an existing version, submit "删除 styles.css" and confirm the run succeeds, the terminal-time products card no longer lists `styles.css`, and no `internal` failure is recorded.
