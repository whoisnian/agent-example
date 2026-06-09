## Context

`branch` rollback (and `iterate`) enqueue an `execute` message with `parent_version_id` so the worker can build the new version on the parent's artifacts (`docs/ARCHITECTURE.md §6.4`). The worker has all the inputs but uses none of them:

- `TaskExecuteMessage` (`core/messages.py`) carries `parent_version_id: UUID | None` and `parent_artifact_root: str | None` — both parsed, neither read.
- `OssClient` (`core/storage.py`) already has `server_side_copy(src_prefix, dst_prefix, key)` whose docstring says "no caller wired yet — kept available for downstream proposals." It lacks only a list method.
- `RunContext` exposes `tenant_id`, `task_id`, `version_id`, `oss_prefix` (= `compute_oss_prefix(tenant, task, version)` = `{tenant}/{task}/{version}/`), `oss_client`, `checkpoint_store`, `logger`.
- `LoopAgent.run(ctx, message)` (`agents/base.py`) runs `run_agent_loop`, then inserts an `artifacts` row per produced file via `self._persistence.insert_artifact(...)`.

**Decisive finding:** no **production** code sets `task_versions.artifact_root` — the create-INSERT writes it `nil` (`api/.../service.go:390`) and there is no `UPDATE` (only a test helper sets it). So the `parent_artifact_root` the API forwards is null today. (Note: the column, the propagation through `resolveBase` → outbox payload, and API tests asserting `parent_artifact_root == "artifacts/v1/"` all exist — the field is *wired to carry a non-null value once a writer lands*, and that value is NOT the deterministic prefix. So "always null" is a statement about today, not the contract.) Inheritance therefore keys on `parent_version_id`: artifacts for version V always live under the deterministic `compute_oss_prefix(tenant, task, V)` (the `oss_prefix` the worker writes to), so the parent's objects are reliably under `compute_oss_prefix(ctx.tenant_id, ctx.task_id, parent_version_id)` — no DB read needed. **Contract guard:** if a non-null `parent_artifact_root` ever arrives, the worker logs a warning (a "dead column came alive" signal), because the deterministic-prefix assumption would then need revalidation against the populated value.

## Goals / Non-Goals

**Goals:**
- On a fresh execution carrying `parent_version_id`, copy the parent version's OSS objects into the new version's prefix and record `artifacts` rows, before the agent runs — so iterate and rollback-branch inherit the parent's files.
- Idempotent across redelivery/resume (no duplicate copies or rows).
- Reuse the existing `server_side_copy`; add only the missing `list_keys` primitive.

**Non-Goals:**
- Feeding inherited artifacts into the agent's reasoning context (true edit-in-place). The MVP planner/executor/critic loop has no read/context channel; this change is **copy-forward**, not incremental editing.
- Research-specific "parent report as prompt context".
- Populating `task_versions.artifact_root` (API/schema concern; unnecessary here).
- De-duplicating the `artifacts` row when the agent overwrites an inherited file (documented trade-off below).
- Any change to the `execute`/MQ contract or the API.

## Decisions

### Decision 1 — Key on `parent_version_id`, derive the parent prefix deterministically

`parent_prefix = compute_oss_prefix(ctx.tenant_id, ctx.task_id, parent_version_id)`. Inheritance triggers iff `message.parent_version_id is not None`. `parent_artifact_root` is ignored (always null); it stays in the message for the API contract but is documented as worker-unused.

- **Alternative considered — populate and use `task_versions.artifact_root`:** rejected. It would need the worker to write `task_versions` (forbidden by AGENTS §4.2) or a new API/event path, for no benefit over the deterministic prefix.

### Decision 2 — `inherit_parent_artifacts(ctx, persistence, parent_version_id) -> int`

A new `agents/inherit.py`:

```python
_RESERVED_PREFIXES = ("checkpoints/",)  # run-internal, NOT artifacts

async def inherit_parent_artifacts(ctx, persistence, parent_version_id) -> int:
    parent_prefix = compute_oss_prefix(ctx.tenant_id, ctx.task_id, parent_version_id)
    n = 0
    for key, size in await ctx.oss_client.list_keys(parent_prefix):   # relative key + size
        if key.startswith(_RESERVED_PREFIXES):                        # skip checkpoint blobs (B1)
            continue
        abs_key = await ctx.oss_client.server_side_copy(parent_prefix, ctx.oss_prefix, key)
        await persistence.insert_artifact(                            # upsert on (version_id, oss_key)
            version_id=ctx.version_id, kind="file",
            oss_key=abs_key,                                          # the copy's returned absolute key (S3)
            mime=mimetypes.guess_type(key)[0], bytes_size=size, sha256=None,
        )
        n += 1
    return n
```

- **Skip `checkpoints/` (B1):** the CheckpointStore offloads large checkpoints to `checkpoints/<step_seq>.bin` under the **same** version prefix (`core/checkpoint.py:71-78`). `list_keys(parent_prefix)` returns those alongside artifacts, so inheritance MUST skip the reserved `checkpoints/` sub-prefix or it would copy checkpoint internals into the new version and list them as artifacts.
- **Record the copy's returned key (S3):** `server_side_copy` returns the normalized absolute destination key (`dst_absolute = _normalize_key(...)`); inheritance records *that*, not `ctx.oss_prefix + key`, since `normpath` could rewrite an odd key and a mismatched `oss_key` would presign-404. This mirrors how `LoopAgent` records the value `put` returned.
- `sha256=None`: recomputing requires downloading every object; the `artifacts.sha256` column is nullable, and MVP omits it for inherited (carried-forward) files. The original producing version retains the true hash.
- `kind="file"` matches what `LoopAgent` records for produced files (no DB CHECK on `artifacts.kind`).
- An empty/missing parent prefix → `list_keys` returns `[]` → `0` copied → new version starts empty (today's behavior). Real OSS errors propagate so the run fails/retries; a missing parent is not an error.

### Decision 3 — Seed in `LoopAgent.run`, gated on a fresh run

```python
async def run(self, ctx, message):
    if message.parent_version_id is not None and await ctx.checkpoint_store.latest() is None:
        n = await inherit_parent_artifacts(ctx, self._persistence, message.parent_version_id)
        ctx.logger.info("artifacts_inherited", count=n, parent_version_id=str(message.parent_version_id))
    produced = await run_agent_loop(...)
    ...
```

Gating on `checkpoint_store.latest() is None` ties inheritance to the same "fresh vs resume" signal the loop itself uses (`_load_or_create_plan` resumes when a checkpoint exists). On a normal redelivery a checkpoint exists, so the seed is skipped — avoiding a redundant re-copy of the whole parent set.

**The gate is a performance optimization, NOT the idempotency guarantee.** The first checkpoint (`step_seq=0`) is only written *after* the planner LLM call (`loop.py:194` then `:196`), so a crash between inheritance and checkpoint-0 leaves `latest()` still `None`, and a redelivery re-inherits the entire set. Correctness under that window comes from **row-level idempotency**, not the gate: `insert_artifact` is an upsert on `(version_id, oss_key)` (Decision 5), so re-inheritance (and any overwrite) collapses to one row. `server_side_copy` is overwrite-idempotent for the objects. So the seed is safe to re-run; the gate just avoids doing so needlessly.

- **Placement rationale:** `LoopAgent.run` already owns `insert_artifact` and the persistence handle; `run_agent_loop` does not. Seeding here keeps the artifact-DB writes in one place and avoids threading persistence into the loop.

### Decision 4 — `OssClient.list_keys(prefix) -> list[tuple[str, int]]`

The one new primitive over `aioboto3`. It validates `prefix` via the existing `_validate_prefix`, then **paginates** — the aioboto3 async paginator (`async for page in s3.get_paginator("list_objects_v2").paginate(Bucket=..., Prefix=prefix)`) or an explicit `ContinuationToken`/`IsTruncated` loop — so it does not silently truncate at 1000 objects. It MUST tolerate a page with no `Contents` key (empty result → `[]`, which drives the empty-parent no-op). For each object it returns the **relative** key (with `prefix` stripped) and `Size`; it skips any returned key not starting with `prefix` (defensive). Relative keys feed straight back into `server_side_copy`/`_normalize_key`, preserving the prefix-escape guards.

### Decision 5 — Idempotent artifact recording via a `(version_id, oss_key)` unique index + upsert

A new migration adds `CREATE UNIQUE INDEX artifacts_version_oss_key_key ON artifacts (version_id, oss_key)` (the data model never asserted uniqueness, so this is additive — `task-data-model` is MODIFIED to state it). The worker's `insert_artifact` becomes an upsert:

```sql
INSERT INTO artifacts (id, version_id, kind, oss_key, mime, bytes, sha256)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (version_id, oss_key) DO UPDATE
  SET kind = EXCLUDED.kind, mime = EXCLUDED.mime,
      bytes = EXCLUDED.bytes, sha256 = EXCLUDED.sha256
RETURNING id;
```

This makes the whole feature robust rather than relying on a fragile crash window:
- **Re-inheritance** (crash before checkpoint-0 → redelivery) upserts the same rows → no duplicates.
- **Overwrite** (agent writes a path an inherited object already occupies) updates the one row with the produced file's metadata (correct: latest object's size/hash), instead of leaving two rows.
- The `RETURNING id` yields the existing row's id on conflict, so the caller's contract is unchanged.

This deliberately pulls forward what the first draft deferred, because the review showed re-inheritance can duplicate the **whole** parent set (not "a few files"), and the index fixes that, the overwrite case, and the produced-file dup in one move. **Risk:** the unique index fails to create if the `artifacts` table already holds duplicate `(version_id, oss_key)` rows; on fresh MVP/dev DBs there are none, and the migration documents the precondition.

## Risks / Trade-offs

- **[Duplicate `artifacts` row on overwrite]** → documented (Decision 5); bounded, non-corrupting; de-dup deferred to a schema-touching change.
- **[`sha256` null for inherited files]** → acceptable; the column is nullable and the producing version keeps the true hash. A reader needing the hash can follow the lineage.
- **[Large parent → slow, non-interruptible copy before the loop]** → server-side copies avoid data transfer through the worker; the loop's cancel/deadline checks resume at the first step boundary after seeding. MVP parents are small; chunked/cancellable copy is a later optimization.
- **[OSS unavailable at seed]** → the run fails and is retried by the existing nack/requeue path, same as any infra error; a fresh attempt re-seeds (idempotent copy; the dup-row window is the same tiny crash-before-checkpoint window the loop already has).
- **[Keying on `parent_version_id` while `parent_artifact_root` stays null]** → documented; the message field is left in place (no cross-service contract churn) but called out as worker-unused, with a warn-log if it ever arrives non-null so a future reader is alerted before the deterministic-prefix assumption silently diverges.
- **[Tenant skew between parent and child prefix]** → the parent prefix uses `ctx.tenant_id`, which the consumer resolves as `msg.tenant_id or <default>`. Parent and child must resolve the same tenant for the prefix to match; in practice tenant rides the message consistently, so this is low-risk, but a mismatch degrades to the no-op path (inherits nothing) rather than copying wrong data. Noted, not guarded for MVP.

## Migration Plan

Pure worker-internal, additive: one new OSS method + one new seed step. No schema/MQ/API change. Rollback = revert the worker commits; with no inheritance the worker reverts to from-scratch runs (today's behavior), and already-copied objects/rows are harmless.

## Open Questions

- None blocking. True incremental editing (agent reads the inherited files) is a deliberate Post-MVP follow-up gated on the loop gaining a read/context channel.
