"""Executor subagent: performs a single planned step, optionally producing files.

The role instruction lives in the sibling ``prompt.md``; ``build`` returns the
:class:`SubagentDefinition` the step loop uses for the executor phase.
"""

from __future__ import annotations

from worker.plugins.subagent_spec import SubagentDefinition, load_prompt


def build() -> SubagentDefinition:
    return SubagentDefinition(name="executor", instruction=load_prompt(__file__))
