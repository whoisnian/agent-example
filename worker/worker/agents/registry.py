"""Agent registry keyed by ``task_type`` (design D1).

Populated once at startup. ``register`` validates each agent's :class:`AgentSpec`
against the :class:`~worker.plugins.registry.PluginRegistry` (system-prompt path
resolvable, every declared tool present) so a misconfigured agent fails startup
rather than at first message. The dispatcher uses :meth:`get`; a miss means the
``task_type`` is unimplemented and the dispatcher raises ``AgentNotImplementedError``.
"""

from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from worker.agents.base import Agent, AgentSpec
    from worker.plugins.registry import PluginRegistry


class AgentValidationError(RuntimeError):
    """Raised when an agent spec is invalid (bad prompt path / unknown tool)."""


class AgentRegistrationError(RuntimeError):
    """Raised when two agents claim the same ``task_type``."""


def validate_spec(spec: AgentSpec, plugins: PluginRegistry) -> None:
    """Validate ``spec`` against available plugins; raise on any problem."""
    if not spec.system_prompt_path.is_file():
        raise AgentValidationError(
            f"agent {spec.task_type!r}: system prompt not found at {spec.system_prompt_path}"
        )
    for tool_name in spec.tool_names:
        if plugins.get_tool(tool_name) is None:
            raise AgentValidationError(
                f"agent {spec.task_type!r}: declared tool {tool_name!r} is not a registered plugin"
            )
    for subagent_name in spec.subagent_names:
        if plugins.get_subagent(subagent_name) is None:
            raise AgentValidationError(
                f"agent {spec.task_type!r}: declared subagent {subagent_name!r} "
                f"is not a registered plugin"
            )


class AgentRegistry:
    """Maps ``task_type`` to a validated :class:`Agent`."""

    def __init__(self, plugins: PluginRegistry) -> None:
        self._plugins = plugins
        self._agents: dict[str, Agent] = {}

    def register(self, agent: Agent) -> None:
        validate_spec(agent.spec, self._plugins)
        if agent.task_type in self._agents:
            raise AgentRegistrationError(f"duplicate agent for task_type={agent.task_type!r}")
        self._agents[agent.task_type] = agent

    def get(self, task_type: str) -> Agent | None:
        return self._agents.get(task_type)

    def __len__(self) -> int:
        return len(self._agents)

    def __contains__(self, task_type: object) -> bool:
        return task_type in self._agents
