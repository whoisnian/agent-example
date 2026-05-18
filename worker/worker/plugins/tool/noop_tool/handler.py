"""Stub tool used by tests; always returns ``{"ok": True}``.

This is the single MVP plugin included by the scaffold (spec:
worker-execution-runtime → "Plugin Loader"). Real tools are added by their
own OpenSpec proposals.
"""

from __future__ import annotations

from typing import Any


async def noop(_ctx: Any = None, **_kwargs: Any) -> dict[str, bool]:
    return {"ok": True}
