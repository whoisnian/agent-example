"""Planner subagent: decomposes the task into an ordered list of concrete steps.

The role instruction lives in the sibling ``prompt.md``; ``build`` returns the
:class:`SubagentDefinition` the step loop uses for the planner phase.
"""

from __future__ import annotations

from worker.plugins.subagent_spec import SubagentDefinition, load_prompt


def build() -> SubagentDefinition:
    return SubagentDefinition(name="planner", instruction=load_prompt(__file__))
