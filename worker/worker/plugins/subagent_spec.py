"""Subagent plugin contract: the definition a subagent plugin's entrypoint returns.

A subagent plugin lives at ``worker/plugins/subagent/<name>/`` as ``plugin.yaml``
(``kind: subagent``) + ``prompt.md`` + ``handler.py``; the manifest ``entrypoint``
resolves to a zero-argument builder returning a :class:`SubagentDefinition`. The
builder loads its role instruction from the sibling ``prompt.md`` via
:func:`load_prompt`, so the prompt is editable as data rather than code.

For MVP a subagent is prompt-only — it carries no model or tool set; the parent
agent's model and tools apply (design Non-Goals). Lives in the plugins layer so
handlers import only ``worker.plugins`` (never ``worker.agents``), keeping the
``agents -> plugins`` dependency direction one-way.
"""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True, slots=True)
class SubagentDefinition:
    """A resolved subagent: a role name plus its instruction prompt."""

    name: str
    instruction: str


def load_prompt(handler_file: str) -> str:
    """Read the ``prompt.md`` sibling of a subagent handler module.

    Pass ``__file__`` from the handler; returns the prompt text (trailing
    whitespace stripped so an editor's final newline does not alter the prompt).
    """
    return (Path(handler_file).resolve().parent / "prompt.md").read_text(encoding="utf-8").strip()
