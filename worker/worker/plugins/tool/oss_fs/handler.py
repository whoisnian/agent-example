"""OSS-backed filesystem tool, scoped to the run's OSS prefix.

Reads and writes objects under ``ctx.oss_prefix`` via ``ctx.oss_client`` (the
client enforces prefix safety, so a relative ``path`` can never escape the
run's namespace — ARCHITECTURE §8.4). A ``write`` returns the object metadata
(absolute key, byte length, sha256) so the orchestration loop can record an
``artifacts`` row on success.

This handler is the raw plugin entrypoint; the agent wraps it with
``cost_metered_tool("oss_fs")`` so every call emits a ``cost.tool`` event.
"""

from __future__ import annotations

import hashlib
from typing import Any


async def oss_fs(ctx: Any, *, op: str, path: str, content: str | None = None) -> dict[str, Any]:
    if op == "write":
        if content is None:
            raise ValueError("oss_fs write requires `content`")
        body = content.encode("utf-8")
        oss_key = await ctx.oss_client.put(ctx.oss_prefix, path, body)
        return {
            "path": path,
            "oss_key": oss_key,
            "bytes": len(body),
            "sha256": hashlib.sha256(body).hexdigest(),
        }
    if op == "read":
        data = await ctx.oss_client.get(ctx.oss_prefix, path)
        return {"path": path, "content": data.decode("utf-8")}
    raise ValueError(f"unknown oss_fs op: {op!r}")
