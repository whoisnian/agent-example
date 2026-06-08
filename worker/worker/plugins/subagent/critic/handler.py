"""Critic subagent: judges a step result and decides advance / retry / finish.

The role instruction lives in the sibling ``prompt.md``; ``build`` returns the
:class:`SubagentDefinition` the step loop uses for the critic phase.
"""

from __future__ import annotations

from worker.plugins.subagent_spec import SubagentDefinition, load_prompt


def build() -> SubagentDefinition:
    return SubagentDefinition(name="critic", instruction=load_prompt(__file__))
