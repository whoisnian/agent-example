## 1. Artifact idempotency (schema + worker upsert)

- [x] 1.1 Add migration `api/migrations/0008_artifacts_unique_oss_key.up.sql`: `CREATE UNIQUE INDEX artifacts_version_oss_key_key ON artifacts (version_id, oss_key);` (+ `0008_..._down.sql` dropping it). Note the precondition in a comment: fails if pre-existing duplicate `(version_id, oss_key)` rows exist (none on fresh MVP DBs).
- [x] 1.2 Change `Persistence.insert_artifact` (`worker/worker/core/persistence.py`) to upsert: `INSERT ... ON CONFLICT (version_id, oss_key) DO UPDATE SET kind=EXCLUDED.kind, mime=EXCLUDED.mime, bytes=EXCLUDED.bytes, sha256=EXCLUDED.sha256 RETURNING id` and return the (possibly existing) row id.

## 2. OSS list primitive

- [x] 2.1 Add `OssClient.list_keys(prefix) -> list[tuple[str, int]]` to `worker/worker/core/storage.py`: validate `prefix` via `_validate_prefix`; **paginate** (aioboto3 `get_paginator("list_objects_v2").paginate(Bucket, Prefix=prefix)` with `async for`, OR an explicit `ContinuationToken`/`IsTruncated` loop) — do NOT ship a single-page lister; tolerate a page with no `Contents` (→ contributes nothing); return each object's **relative** key (with `prefix` stripped) + `Size`; defensively skip any key not starting with `prefix`.
- [x] 2.2 Storage integration test (testcontainers/minio, mirroring `tests/integration/test_storage.py`; uses `pytest.importorskip("testcontainers.minio")` so it skips cleanly): put objects under a prefix (enough to span >1 page if feasible, else assert the pagination path is taken), assert `list_keys` returns relative keys + sizes, and that `server_side_copy` then `get` round-trips bytes.

## 3. Inheritance function

- [x] 3.1 Add `worker/worker/agents/inherit.py` with `async def inherit_parent_artifacts(ctx, persistence, parent_version_id) -> int`: derive `parent_prefix = compute_oss_prefix(ctx.tenant_id, ctx.task_id, parent_version_id)`; for each `(key, size)` from `list_keys(parent_prefix)`, **skip keys starting with `checkpoints/`** (reserved run-internal blobs), else `abs_key = await server_side_copy(parent_prefix, ctx.oss_prefix, key)` then `persistence.insert_artifact(version_id=ctx.version_id, kind="file", oss_key=abs_key, mime=mimetypes.guess_type(key)[0], bytes_size=size, sha256=None)`; return the count. Missing/empty parent → 0 (no error); real OSS errors propagate.
- [x] 3.2 Unit test (`tests/unit/test_inherit.py`) with a fake OSS client (records `list_keys`/`server_side_copy`) + fake persistence (records `insert_artifact`): asserts it copies each artifact object into `ctx.oss_prefix`, records one row per object with `oss_key` = the copy's returned absolute key + correct `bytes_size`, returns the count; **a `checkpoints/0.bin` entry is NOT copied/recorded**; empty parent → 0/0.

## 4. Wire into LoopAgent

- [x] 4.1 In `worker/worker/agents/base.py` `LoopAgent.run`: before `run_agent_loop`, when `message.parent_version_id is not None` AND `await ctx.checkpoint_store.latest() is None`, call `inherit_parent_artifacts(...)` and log `artifacts_inherited{count, parent_version_id}`. Also: if `message.parent_artifact_root is not None`, log a warning (`parent_artifact_root_unexpected`) — the worker keys on `parent_version_id`, and a populated value signals the deterministic-prefix assumption needs revalidation.
- [x] 4.2 Test (extend `tests/integration/test_code_agent.py` fake-model harness or a focused unit test): a fresh run WITH `parent_version_id` inherits (objects copied + rows recorded) then the loop produces its own files; a run WITHOUT `parent_version_id` does no inheritance; a resume (a checkpoint pre-seeded so `latest()` is non-None) skips inheritance; the upsert makes a re-run with the same objects not duplicate rows.

## 5. Docs

- [x] 5.1 Reconcile `docs/ARCHITECTURE.md §6.4`: the worker inherits parent artifacts via the deterministic `parent_version_id` prefix + server-side copy (not via the still-null `parent_artifact_root`), excludes `checkpoints/`, records idempotently on `(version_id, oss_key)`, and is copy-forward (no agent-context read) for MVP. Short, truthful note (AGENTS §1).

## 6. Gates

- [x] 6.1 From `worker/`: `make lint`, `make type` (`mypy --strict worker/`), `make test` (unit), `make test-int` (integration; Docker — pre-existing mq_topology/persistence failures are unrelated; minio-dependent storage tests skip where the module is absent).
- [x] 6.2 From `api/`: `go test -race ./...` + `make test-integration` (migration `0008` applies, up/down/up clean; the worker integration suite applies api migrations so the new index is present).
- [x] 6.3 `openspec validate add-worker-rollback-handling --strict` from repo root passes.
