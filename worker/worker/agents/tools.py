"""Agent-side tool adapters.

Bridges the raw ``oss_fs`` plugin handler into the ``write_file`` callable the
orchestration loop expects, wrapping it with ``cost_metered_tool`` so every
file write emits a ``cost.tool`` event (design D6). Both the code-gen and
research agents share this adapter.
"""

from __future__ import annotations

import mimetypes
from typing import TYPE_CHECKING

from worker.agents.loop import ProducedArtifact
from worker.core.cost_meter import cost_metered_tool
from worker.plugins.tool.oss_fs.handler import oss_fs

if TYPE_CHECKING:
    from worker.core.run_context import RunContext

_metered_oss_fs = cost_metered_tool("oss_fs")(oss_fs)


async def oss_write_file(ctx: RunContext, path: str, content: str) -> ProducedArtifact:
    """Write ``content`` to ``path`` under the run's OSS prefix (cost-metered)."""
    res = await _metered_oss_fs(ctx, op="write", path=path, content=content)
    mime, _ = mimetypes.guess_type(path)
    return ProducedArtifact(
        path=res["path"],
        oss_key=res["oss_key"],
        bytes=res["bytes"],
        sha256=res["sha256"],
        mime=mime,
    )


async def oss_delete_file(ctx: RunContext, path: str) -> bool:
    """Delete ``path`` under the run's OSS prefix (cost-metered).

    Returns whether an object was removed; a missing object is an idempotent
    no-op (``False``). Routed through the same ``cost_metered_tool("oss_fs")``
    wrapper as the write so every deletion emits a ``cost.tool`` event
    (AGENTS.md §4.2; add-artifact-deletion).
    """
    res = await _metered_oss_fs(ctx, op="delete", path=path)
    return bool(res["deleted"])
