"""Subagent resolution for the agent step loop.

:class:`RoleInstructions` is the planner / executor / critic prompt triple the
loop runs with; :func:`resolve_subagents` turns declared subagent names into
their resolved :class:`SubagentDefinition`s via the plugin registry.

A missing subagent raises :class:`~worker.agents.registry.AgentValidationError`
— the same failure type as spec validation. Because resolution happens during
agent build (before ``AgentRegistry.register`` runs ``validate_spec``), this is
the real first site at which a misconfigured subagent aborts startup non-zero.
"""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import TYPE_CHECKING

from worker.agents.registry import AgentValidationError

if TYPE_CHECKING:
    from worker.plugins.registry import PluginRegistry
    from worker.plugins.subagent_spec import SubagentDefinition


@dataclass(frozen=True, slots=True)
class RoleInstructions:
    """The planner / executor / critic instructions the step loop runs with."""

    planner: str
    executor: str
    critic: str


def to_role_instructions(subagents: Mapping[str, SubagentDefinition]) -> RoleInstructions:
    """Project the resolved planner/executor/critic subagents into RoleInstructions.

    Expects all three role keys present (the agent declares them together, so
    :func:`resolve_subagents` returns them as a set).
    """
    return RoleInstructions(
        planner=subagents["planner"].instruction,
        executor=subagents["executor"].instruction,
        critic=subagents["critic"].instruction,
    )


def resolve_subagents(
    registry: PluginRegistry, names: Sequence[str]
) -> dict[str, SubagentDefinition]:
    """Resolve each subagent name to its :class:`SubagentDefinition`.

    Raises :class:`AgentValidationError` naming the first missing subagent rather
    than returning a partial mapping, so a misconfiguration fails the build.
    """
    out: dict[str, SubagentDefinition] = {}
    for name in names:
        record = registry.get_subagent(name)
        if record is None:
            raise AgentValidationError(f"declared subagent {name!r} is not a registered plugin")
        builder = record.resolve()
        out[name] = builder()
    return out
