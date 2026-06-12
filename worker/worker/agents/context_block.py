"""Conversation-context block assembly (refactor-task-conversation-continuity).

Renders, deterministically and without any LLM call, the context the loop
prepends to planner / executor inputs:

1. the conversation history carried by the execute message (oldest→newest,
   failed turns explicitly marked), and
2. the inherited-artifact inventory (every copied object's path + size, plus
   the content of small text files within a fixed byte budget).

The block is assembled once per run, on the fresh path after inheritance, and
persisted in the ``step_seq=0`` checkpoint so a resumed attempt reuses it
verbatim (spec: worker-agent-orchestration → "Conversation Context Injection").
"""

from __future__ import annotations

import mimetypes
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from worker.core.messages import HistoryTurn
    from worker.core.run_context import RunContext

#: Single-file ceiling for content excerpts; bigger text files are listed only.
TEXT_EXCERPT_MAX_BYTES = 8 * 1024
#: Total content budget across all excerpts.
EXCERPT_TOTAL_BUDGET_BYTES = 24 * 1024

#: Extensions treated as text when MIME guessing is inconclusive.
_TEXT_EXTENSIONS = {
    ".md",
    ".txt",
    ".py",
    ".js",
    ".ts",
    ".tsx",
    ".jsx",
    ".json",
    ".yaml",
    ".yml",
    ".toml",
    ".html",
    ".css",
    ".csv",
    ".sql",
    ".sh",
    ".go",
    ".rs",
}


def is_text_artifact(key: str) -> bool:
    """MIME/extension heuristic deciding excerpt eligibility."""
    mime, _ = mimetypes.guess_type(key)
    if mime is not None:
        return mime.startswith("text/") or mime in {"application/json", "application/xml"}
    dot = key.rfind(".")
    return dot != -1 and key[dot:].lower() in _TEXT_EXTENSIONS


def render_history_section(history: list[HistoryTurn]) -> str:
    """Oldest→newest turn rendering; non-succeeded turns carry an explicit marker."""
    if not history:
        return ""
    lines = ["Conversation so far (oldest first):"]
    for turn in history:
        lines.append(f"[v{turn.version_no}] user: {turn.prompt}")
        if turn.status != "succeeded":
            marker = f"(this attempt ended {turn.status.upper()})"
            result = f"{marker} {turn.summary}" if turn.summary else marker
        else:
            result = turn.summary if turn.summary else "(no summary recorded)"
        lines.append(f"[v{turn.version_no}] result: {result}")
    return "\n".join(lines)


async def assemble_context_block(
    ctx: RunContext,
    history: list[HistoryTurn],
    inherited: list[tuple[str, int]],
) -> str:
    """Build the full context block; empty string when there is nothing to say.

    ``inherited`` is the exact (relative key, size) list the inheritance copy
    produced — NOT a fresh listing of the run prefix, which would misattribute
    this run's own outputs as inherited. Excerpts are chosen deterministically:
    eligible text files ≤ TEXT_EXCERPT_MAX_BYTES, ascending size then
    lexicographic path, until EXCERPT_TOTAL_BUDGET_BYTES is exhausted.
    """
    sections: list[str] = []
    if history:
        sections.append(render_history_section(history))

    if inherited:
        inventory = sorted(inherited, key=lambda e: e[0])
        lines = ["Files inherited from the previous version:"]
        lines.extend(f"- {key} ({size} bytes)" for key, size in inventory)
        sections.append("\n".join(lines))

        excerpts: list[str] = []
        budget = EXCERPT_TOTAL_BUDGET_BYTES
        for key, size in sorted(inherited, key=lambda e: (e[1], e[0])):
            if size > TEXT_EXCERPT_MAX_BYTES or size > budget or not is_text_artifact(key):
                continue
            body = await ctx.oss_client.get(ctx.oss_prefix, key)
            try:
                text = body.decode("utf-8")
            except UnicodeDecodeError:
                continue  # mislabeled binary: list-only
            excerpts.append(f"--- {key} ---\n{text}")
            budget -= size
        if excerpts:
            sections.append("Inherited file contents:\n" + "\n".join(excerpts))

    return "\n\n".join(sections)
