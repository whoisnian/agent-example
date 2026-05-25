"""Web-search tool — MVP stub (deterministic, offline).

Returns deterministic placeholder results so the research agent and CI run with
no network dependency (§7 MVP boundary). A real search backend is a separate
later change; do NOT add a network SDK here.
"""

from __future__ import annotations

from typing import Any


async def web_search(_ctx: Any = None, *, query: str, top_k: int = 5) -> dict[str, Any]:
    results = [
        {
            "title": f"Result {i + 1} for {query}",
            "url": f"https://example.invalid/{i + 1}",
            "snippet": f"Deterministic stub snippet {i + 1} about {query}.",
        }
        for i in range(max(0, min(top_k, 5)))
    ]
    return {"query": query, "results": results}
