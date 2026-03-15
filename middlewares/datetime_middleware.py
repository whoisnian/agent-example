from typing import Awaitable, Callable

from deepagents.graph import AgentMiddleware
from deepagents.middleware._utils import append_to_system_message
from langchain.agents.middleware.types import ModelRequest, ModelResponse


class DatetimeMiddleware(AgentMiddleware):
    """Middleware that injects the task start time into the agent system prompt.

    Reads ``start_time`` from the typed runtime context (a :class:`CustomContext`
    instance) and appends ``"Task started at: <RFC3339>"`` to the system prompt
    before each model call.  If context is absent or lacks ``start_time`` the
    prompt is left unchanged.
    """

    def _inject(self, request: ModelRequest) -> ModelRequest:
        ctx = request.runtime.context
        if ctx is None or not hasattr(ctx, "start_time"):
            return request
        ts = ctx.start_time.astimezone().isoformat(timespec="seconds")
        new_sys = append_to_system_message(
            request.system_message,
            f"Task started at: {ts}",
        )
        return request.override(system_message=new_sys)

    def wrap_model_call(
        self,
        request: ModelRequest,
        handler: Callable[[ModelRequest], ModelResponse],
    ) -> ModelResponse:
        return handler(self._inject(request))

    async def awrap_model_call(
        self,
        request: ModelRequest,
        handler: Callable[[ModelRequest], Awaitable[ModelResponse]],
    ) -> ModelResponse:
        return await handler(self._inject(request))
