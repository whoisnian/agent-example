"""Execution dispatcher (placeholder).

The scaffold deliberately has no real agents. Every dispatch attempt raises
:class:`AgentNotImplementedError`; the consumer translates that into a final
``task.events`` ``error`` event (``code=unimplemented``) and ``nack(requeue=False)``
so the message lands on ``task.dlx`` (spec: worker-execution-runtime →
"Execution Dispatcher (Placeholder)").

Future agent proposals will replace this with a registry lookup; we keep the
exception type and signature stable so call sites do not change.
"""

from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from worker.core.messages import TaskExecuteMessage
    from worker.core.run_context import RunContext


class AgentNotImplementedError(RuntimeError):
    """Raised when no agent is registered for the given task_type."""

    def __init__(self, task_type: str) -> None:
        super().__init__(f"no agent registered for task_type={task_type!r}")
        self.task_type = task_type


class ExecutionDispatcher:
    """Looks up agents by ``task_type`` and runs them.

    The scaffold always raises ``AgentNotImplementedError``.
    """

    async def dispatch(
        self,
        ctx: RunContext,
        message: TaskExecuteMessage,
    ) -> None:
        raise AgentNotImplementedError(message.task_type)
