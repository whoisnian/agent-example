## Context

The pipeline currently tracks wall-clock timing via `time.time()` in `main.py` for event streaming diagnostics, but no structured datetime is ever passed into agent context. The html-report agent already has a "Generated on \<timestamp>" placeholder in its system prompt — but relies on the LLM making up a timestamp, not on the actual task start time. The deepagents framework supports typed `context_schema` on a compiled agent and exposes context through `request.runtime.context` in every `wrap_model_call` invocation.

Affected modules:
- `main.py` — entry point; creates the agent and calls `astream()`
- `agents/html_report.py` — html-report subagent builder
- `agents/datetime_middleware.py` — new module (to be created)

## Goals / Non-Goals

**Goals:**
- Inject a real task start datetime into agent system prompts via a reusable middleware.
- Allow the html-report agent to insert an accurate, human-readable timestamp into the generated HTML report.
- Keep datetime logic in one place (the middleware).

**Non-Goals:**
- Locale-specific formatting or custom format configurability.
- Passing datetime to the web-research subagent (it has no use for it).
- Persisting or logging the start time beyond its use in the system prompt.

## Decisions

### 1. Implement `DatetimeMiddleware` using `wrap_model_call`

`AgentMiddleware.wrap_model_call` (and its async counterpart) is the standard extension point for modifying the system prompt before each model call. It receives a `ModelRequest` whose `runtime.context` field holds the typed context. This is the same pattern used by `SkillsMiddleware` for injecting skills documentation.

`before_agent` was considered but it fires once per session, whereas `wrap_model_call` fires before every model call and is the idiomatic place to modify the system prompt — consistent with the rest of the middleware stack.

**Implementation:**
```python
# agents/datetime_middleware.py
from dataclasses import dataclass
from datetime import datetime
from typing import Any, Callable, Awaitable
from langchain.agents.middleware.types import AgentMiddleware, ModelRequest, ModelResponse, ContextT, ResponseT
from deepagents.middleware._utils import append_to_system_message


@dataclass
class DatetimeContext:
    start_time: datetime


class DatetimeMiddleware(AgentMiddleware):
    def _inject(self, request: ModelRequest) -> ModelRequest:
        ctx = request.runtime.context
        if ctx is None or not hasattr(ctx, "start_time"):
            return request
        ts = ctx.start_time.astimezone().isoformat(timespec="seconds")
        new_sys = append_to_system_message(
            request.system_message,
            f"Task started at: {ts}"
        )
        return request.override(system_message=new_sys)

    def wrap_model_call(self, request, handler):
        return handler(self._inject(request))

    async def awrap_model_call(self, request, handler):
        return await handler(self._inject(request))
```

### 2. Define `context_schema` on the main agent and pass `start_time` at runtime

`create_deep_agent` accepts a `context_schema` parameter (a Pydantic/dataclass type). The compiled graph's `astream()` then accepts context values as keyword arguments. This makes `runtime.context` typed and available to all middleware in the stack.

`main.py` will:
1. Record `start_time = datetime.now()` at pipeline start.
2. Pass `context_schema=DatetimeContext` to `create_deep_agent`.
3. Pass `context=DatetimeContext(start_time=start_time)` to `astream()`.

The existing `time.time()` start time is kept for the event-streaming timing display — `datetime.now()` is stored separately for middleware use.

### 3. Apply `DatetimeMiddleware` only to the html-report agent

The middleware is thread-safe, stateless, and reusable, but only the html-report agent has a use case for the timestamp today. It is passed via the `middleware` parameter of `create_agent()` in `build_html_report_subagent()`. The subagent inherits its context from the parent agent's `runtime.context` automatically; no extra wiring is needed.

## Risks / Trade-offs

- **Context not set**: If `astream()` is called without a `DatetimeContext`, `runtime.context` will be `None`. The middleware guards with `if ctx is None or not hasattr(ctx, "start_time")` and silently skips injection — no crash.
- **LLM may ignore the injected text**: The middleware appends the timestamp string to the system prompt; the html-report agent's existing instruction says "Include a 'Generated on \<timestamp>' footer." — updating that instruction to reference the injected time ensures the LLM uses it.
- **Formatting is fixed**: `astimezone().isoformat(timespec="seconds")` produces an unambiguous RFC3339 string (e.g., `2026-03-15T10:30:00+08:00`). Changing this later requires a code edit, but there is currently no requirement for custom format configurability.
