"""Execution dispatcher.

Given a parsed :class:`TaskExecuteMessage` and a prepared :class:`RunContext`,
resolves the agent registered for the message's ``task_type`` and runs it. When
no agent is registered, raises :class:`AgentNotImplementedError`; the consumer
turns that into a final ``task.events`` ``error`` event (``code=unimplemented``)
and ``nack(requeue=False)`` so the message lands on ``task.dlx`` (spec:
worker-execution-runtime → "Execution Dispatcher").

Agent exceptions (including ``asyncio.CancelledError``) propagate unchanged so
the consumer applies its requeue / error / DLX policy.
"""

from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from worker.agents.registry import AgentRegistry
    from worker.core.messages import TaskExecuteMessage
    from worker.core.run_context import RunContext


class AgentNotImplementedError(RuntimeError):
    """Raised when no agent is registered for the given task_type."""

    def __init__(self, task_type: str) -> None:
        super().__init__(f"no agent registered for task_type={task_type!r}")
        self.task_type = task_type


class ExecutionDispatcher:
    """Looks up agents by ``task_type`` in the registry and runs them."""

    def __init__(self, registry: AgentRegistry) -> None:
        self._registry = registry

    async def dispatch(
        self,
        ctx: RunContext,
        message: TaskExecuteMessage,
    ) -> None:
        agent = self._registry.get(message.task_type)
        if agent is None:
            raise AgentNotImplementedError(message.task_type)
        await agent.run(ctx, message)
