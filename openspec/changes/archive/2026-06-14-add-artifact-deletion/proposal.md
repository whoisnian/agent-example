## Why

When a user iterates on an existing version with a request like "删除 styles.css", the worker crashes: the executor has no way to express a deletion (its output contract is `{path, content}` only, and the `oss_fs` tool offers only `read`/`write`). The model improvises by emitting `{"path": "styles.css", "content": null}`, which reaches `oss_fs(op="write", content=None)` and raises `ValueError("oss_fs write requires content")`. That error is uncaught through the agent loop and is recorded by the consumer as a generic `internal` `dispatch_error`, failing the whole run. Deleting an inherited file is a basic, expected iteration operation; today it is both impossible to express and fatal to attempt.

## What Changes

- **New file-deletion primitive in the agent loop.** The executor's JSON output gains an optional `deletions: [path, ...]` list alongside the existing `files`. The write path (`files`) stays byte-identical, preserving the executor-input/output compatibility invariant.
- **`oss_fs` gains a `delete` op.** `op: "delete"` removes the object at `path` under the run's OSS prefix and is idempotent (deleting a missing key is a success), mirroring the write-upsert's at-least-once tolerance. `content` is not required for `delete`.
- **The loop applies deletions per step.** For each declared deletion it removes the OSS object, removes the new version's `artifacts` row for that `(version_id, path)`, and emits a `kind="artifact_deleted"` event so the UI retracts the file. Deletion is idempotent and resume-safe (inheritance is fresh-run-only; a redelivered run re-inherits then re-deletes to the same end state).
- **Hardened executor-output handling.** A malformed `files` entry (e.g. non-string `content`) MUST NO LONGER surface as a generic `internal` `dispatch_error` that kills the run; it raises a typed `ExecutorOutputError` that the consumer maps to `code="executor_output_invalid"` (in both the `kind="error"` event and the terminal mark) — mirroring the existing `AgentNotImplementedError → "unimplemented"` branch — so an LLM misuse is distinguishable from a real infrastructure fault.
- **Worker write-allowlist relaxed for artifacts.** `artifacts` changes from "INSERT only" to "INSERT + scoped DELETE by `(version_id, path)` for the running version" — a controlled, OpenSpec-gated relaxation (the code already flags any allowlist change as requiring this). History is preserved at version granularity: the parent version's rows live under a different `version_id` and are never touched.
- **Executor prompt documents deletion.** `code_system.md` tells the executor to express file removals via `deletions`, never via empty/null content.

No new public API route, MQ topic, or DB migration is required. `ListArtifactsByVersion` needs no change because the row is hard-deleted from the new version (a different `version_id` than the parent's preserved copy). The **products card** needs no change either — it is withheld until the version is terminal and refetches the artifact list from the DB at completion (ignoring mid-run `artifact` frames), so a hard-deleted row never appears. The one web change is in the **event log**: `artifact_deleted` is a new kind that would otherwise render as a stray raw-JSON fallback row, so it must be hidden (one line, symmetric with `artifact`).

## Capabilities

### New Capabilities
<!-- none — this change modifies existing capabilities only -->

### Modified Capabilities
- `worker-agent-orchestration`: executor output gains `deletions`; the loop applies deletions (OSS delete + artifact-row delete + `artifact_deleted` event) per step, idempotently and resume-safe, with deterministic within-step write-then-delete ordering; malformed executor file entries raise a typed error the consumer maps to `executor_output_invalid` instead of generic `internal`.
- `worker-execution-runtime`: the worker write-allowlist for `artifacts` permits scoped, idempotent DELETE by `(version_id, path)` for the current run's version, in addition to INSERT/upsert.
- `task-event-ingest`: the new `kind="artifact_deleted"` event is persisted to `task_events` (for replay/event-log completeness) and triggers no business-table state transition (the worker already removed the artifact row) — this rides the existing generic persist-only path with no new dispatch branch.
- `web-tasks-pages`: the conversation event log hides `artifact_deleted` (like `artifact`), so a deletion never renders a raw-JSON event row; the terminal products card is unchanged.

## Impact

- **Worker** (`worker/`):
  - `worker/plugins/tool/oss_fs/handler.py`, `plugin.yaml` — `delete` op + schema enum.
  - `worker/agents/storage.py` — add a prefix-safe `OssClient.delete(prefix, key)` (none exists today).
  - `worker/agents/loop.py` (`_run_step`, `_execute` result handling) — parse `deletions`, apply writes-then-deletes per step via injected `delete_file` / `delete_artifact` callables (mirroring `write_file` / `persist_artifact`; the loop holds no `Persistence` handle), emit `artifact_deleted`, guard malformed `files`.
  - `worker/agents/tools.py` + `code_agent.py` / `research_agent.py` / `base.py` — a cost-metered delete adapter parallel to `oss_write_file`, wired through the agent constructors.
  - `worker/agents/prompts/code_system.md` (and the executor subagent `prompt.md`) — document `deletions`.
  - `worker/core/persistence.py` — add a scoped `delete_artifact(version_id, path)`; extend the `ALLOWED_WRITE_TABLES` discipline for `artifacts`.
  - `worker/core/consumer.py` — add an `isinstance(dispatch_exc, ExecutorOutputError)` branch mapping to `code="executor_output_invalid"` (both the event and `mark_run_terminal`, which currently hardcode `internal`).
- **API** (`api/`): `task-event-ingest` already persists any kind generically and transitions only on `status`/`error`, so `kind="artifact_deleted"` needs **no new Go dispatch branch** — only a confirming test (the events-ingested metric is already auto-labelled by kind).
- **Web** (`web/`): one line — add `"artifact_deleted"` to `EventLog.tsx`'s `HIDDEN_KINDS`. Products card unchanged.
- **Tests**: worker loop test for delete-an-inherited-file (row gone, event emitted, idempotent on resume, parent untouched); same-step write+delete-of-same-path nets to absent; malformed-content maps to `executor_output_invalid` (not `internal`); `oss_fs` delete idempotency + prefix-escape rejection; web test that `artifact_deleted` renders no log row.
- **Docs**: `docs/ARCHITECTURE.md` artifact/iteration section notes deletion semantics if it currently implies write-only.
