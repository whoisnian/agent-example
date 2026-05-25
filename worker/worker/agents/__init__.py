"""Per-task agents and the registry factory.

Concrete agents (``code-gen``, ``research``) are assembled from an
:class:`~worker.agents.base.AgentSpec` and registered here. The
:class:`~worker.core.dispatcher.ExecutionDispatcher` resolves them by
``task_type``; an unregistered type raises ``AgentNotImplementedError``.
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Any

from worker.agents.code_agent import build_code_agent
from worker.agents.registry import AgentRegistry
from worker.agents.research_agent import build_research_agent

if TYPE_CHECKING:
    from worker.agents.model import ModelFactory
    from worker.core.config import Settings
    from worker.core.persistence import Persistence
    from worker.plugins.registry import PluginRegistry


def build_agent_registry(
    plugins: PluginRegistry,
    model_factory: ModelFactory,
    persistence: Persistence,
    settings: Settings,
    metrics: Any | None = None,
) -> AgentRegistry:
    """Construct the populated agent registry for the worker process.

    Each spec is validated against ``plugins`` at registration, so a bad prompt
    path or unknown tool fails startup rather than the first message.
    """
    registry = AgentRegistry(plugins)
    registry.register(build_code_agent(model_factory, persistence, settings, metrics))
    registry.register(build_research_agent(model_factory, persistence, settings, metrics))
    return registry
