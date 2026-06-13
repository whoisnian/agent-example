"""Parent-artifact inheritance for the execute path (add-worker-rollback-handling).

When an ``execute`` message carries ``parent_version_id``, the worker copies the
parent version's artifact objects into the new version's prefix *before* the
agent runs, so iterate / rollback-branch build on the parent rather than from
scratch. The parent's objects live under the deterministic
``compute_oss_prefix(tenant, task, parent_version)``; run-internal objects (the
reserved ``checkpoints/`` sub-prefix) are excluded; recording is idempotent on
``(version_id, oss_key)`` via the persistence upsert.
"""

from __future__ import annotations

import mimetypes
from typing import TYPE_CHECKING
from uuid import UUID

from worker.core.run_context import compute_oss_prefix

if TYPE_CHECKING:
    from worker.core.persistence import Persistence
    from worker.core.run_context import RunContext

#: Sub-prefixes under a version prefix that are run-internal, NOT artifacts —
#: the CheckpointStore offloads large checkpoints to ``checkpoints/<n>.bin``.
_RESERVED_PREFIXES: tuple[str, ...] = ("checkpoints/",)


async def inherit_parent_artifacts(
    ctx: RunContext, persistence: Persistence, parent_version_id: UUID
) -> list[tuple[str, int]]:
    """Copy the parent version's artifacts into this run's prefix.

    Lists the parent prefix, skips reserved run-internal objects, server-side-
    copies each artifact into ``ctx.oss_prefix``, and records an ``artifacts``
    row keyed on the absolute key the copy returns. Returns the copied
    ``(relative key, size)`` pairs — the authoritative inventory for the
    conversation-context block (a fresh listing of the run prefix would
    misattribute this run's own outputs as inherited). A missing/empty parent
    prefix yields ``[]`` (no error); real OSS errors propagate so the run
    fails and is retried.
    """
    parent_prefix = compute_oss_prefix(ctx.tenant_id, ctx.task_id, parent_version_id)
    copied: list[tuple[str, int]] = []
    for key, size in await ctx.oss_client.list_keys(parent_prefix):
        if key.startswith(_RESERVED_PREFIXES):
            continue
        abs_key = await ctx.oss_client.server_side_copy(parent_prefix, ctx.oss_prefix, key)
        mime = mimetypes.guess_type(key)[0]
        # `key` is the parent-relative path; inheritance preserves relative
        # paths, so it is also the new version's `path` (improve-artifact-
        # conversation-ux).
        artifact_id = await persistence.insert_artifact(
            version_id=ctx.version_id,
            kind="file",
            oss_key=abs_key,
            path=key,
            mime=mime,
            bytes_size=size,
            sha256=None,
        )
        # Announce the inherited file so the new version's turn shows its
        # starting artifact set immediately (insert-then-publish). These run
        # before the plan checkpoint, whose event_seq high-water then covers
        # their seqs (spec: "Resume-Safe Event Sequencing").
        await ctx.event_publisher.publish_event(
            task_id=str(ctx.task_id),
            version_id=str(ctx.version_id),
            run_id=str(ctx.run_id),
            task_type=ctx.task_type,
            kind="artifact",
            payload={
                "artifact_id": str(artifact_id),
                "path": key,
                "mime": mime,
                "bytes": size,
                "sha256": None,
            },
            seq=ctx.next_event_seq(),
            traceparent=ctx.traceparent,
        )
        copied.append((key, size))
    return copied
