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
) -> int:
    """Copy the parent version's artifacts into this run's prefix; return the count.

    Lists the parent prefix, skips reserved run-internal objects, server-side-
    copies each artifact into ``ctx.oss_prefix``, and records an ``artifacts``
    row keyed on the absolute key the copy returns. A missing/empty parent
    prefix yields ``0`` (no error); real OSS errors propagate so the run fails
    and is retried.
    """
    parent_prefix = compute_oss_prefix(ctx.tenant_id, ctx.task_id, parent_version_id)
    count = 0
    for key, size in await ctx.oss_client.list_keys(parent_prefix):
        if key.startswith(_RESERVED_PREFIXES):
            continue
        abs_key = await ctx.oss_client.server_side_copy(parent_prefix, ctx.oss_prefix, key)
        await persistence.insert_artifact(
            version_id=ctx.version_id,
            kind="file",
            oss_key=abs_key,
            mime=mimetypes.guess_type(key)[0],
            bytes_size=size,
            sha256=None,
        )
        count += 1
    return count
