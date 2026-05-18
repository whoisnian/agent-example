"""Cost meter — LangChain callback + tool decorator.

All LLM / tool calls inside the Worker MUST route through this module so a
``cost.<kind>`` event is emitted for the Cost Service. We integrate with
LangChain via :class:`BaseCallbackHandler`; tool calls are wrapped via the
:func:`cost_metered_tool` decorator (spec: worker-execution-runtime → "Cost
Meter").

Token-usage extraction handles two upstream shapes:

- OpenAI-style: ``response.llm_output["token_usage"]`` carries
  ``prompt_tokens`` / ``completion_tokens``.
- Anthropic-style (langchain-anthropic): usage lives on
  ``generation.generation_info["usage"]`` as ``input_tokens`` /
  ``output_tokens``.

Wall-clock duration is captured via ``time.monotonic`` in
``on_llm_start`` / ``on_llm_end`` — we never trust provider-reported
latency.
"""

from __future__ import annotations

import asyncio
import functools
import time
from typing import TYPE_CHECKING, Any, ParamSpec, TypeVar
from uuid import UUID

import structlog
from langchain_core.callbacks.base import BaseCallbackHandler

if TYPE_CHECKING:
    from worker.core.run_context import RunContext


P = ParamSpec("P")
R = TypeVar("R")


class CostMeter(BaseCallbackHandler):
    """LangChain callback that emits ``cost.llm`` per LLM call.

    One ``CostMeter`` is attached per ``RunContext``. The handler captures
    start-time per LLM ``run_id`` (the LangChain ``run_id``, not our run id)
    so concurrent in-process LLM calls account independently.
    """

    raise_error: bool = False  # never break the agent if emission fails
    run_inline: bool = True  # safer in asyncio context

    def __init__(self, ctx: RunContext) -> None:
        self._ctx = ctx
        self._starts: dict[UUID, tuple[float, str]] = {}
        self._log = ctx.logger.bind(component="cost_meter")

    # ------------------------------------------------------------------
    # LangChain callback hooks
    # ------------------------------------------------------------------

    def on_llm_start(
        self,
        serialized: dict[str, Any],
        prompts: list[str],
        *,
        run_id: UUID,
        **kwargs: Any,
    ) -> None:
        model_name = _extract_model_name(serialized, kwargs)
        self._starts[run_id] = (time.monotonic(), model_name)

    def on_llm_end(self, response: Any, *, run_id: UUID, **kwargs: Any) -> None:
        start = self._starts.pop(run_id, None)
        if start is None:
            # No matching start — emit best-effort with unknown duration.
            started_at, model_name = time.monotonic(), "unknown"
        else:
            started_at, model_name = start
        duration_ms = int((time.monotonic() - started_at) * 1000)
        usage = _extract_token_usage(response)

        # Schedule the async publish on the running loop; we are called from
        # sync LangChain code paths so cannot await directly.
        coro = self._emit(model_name, usage, duration_ms)
        self._spawn(coro)

    def on_llm_error(
        self,
        error: BaseException,
        *,
        run_id: UUID,
        **kwargs: Any,
    ) -> None:
        # Still record the wall time so we don't lose cost info on transient
        # provider errors. Token counts unknown.
        start = self._starts.pop(run_id, None)
        if start is None:
            return
        started_at, model_name = start
        duration_ms = int((time.monotonic() - started_at) * 1000)
        self._spawn(self._emit(model_name, _TokenUsage(), duration_ms))

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    async def emit_tool(self, *, tool_name: str, duration_ms: int) -> None:
        seq = self._ctx.next_cost_seq("tool")
        try:
            await self._ctx.cost_publisher.publish_cost(
                task_id=str(self._ctx.task_id),
                version_id=str(self._ctx.version_id),
                run_id=str(self._ctx.run_id),
                kind="tool",
                resource_name=tool_name,
                seq=seq,
                calls=1,
                duration_ms=duration_ms,
                traceparent=self._ctx.traceparent,
            )
        except Exception as exc:  # noqa: BLE001 - never break the host call
            self._log.warning("cost_tool_emit_failed", tool=tool_name, error=str(exc))

    async def _emit(
        self,
        model_name: str,
        usage: _TokenUsage,
        duration_ms: int,
    ) -> None:
        seq = self._ctx.next_cost_seq("llm")
        try:
            await self._ctx.cost_publisher.publish_cost(
                task_id=str(self._ctx.task_id),
                version_id=str(self._ctx.version_id),
                run_id=str(self._ctx.run_id),
                kind="llm",
                resource_name=model_name,
                seq=seq,
                input_tokens=usage.input_tokens,
                output_tokens=usage.output_tokens,
                cached_tokens=usage.cached_tokens,
                duration_ms=duration_ms,
                traceparent=self._ctx.traceparent,
            )
        except Exception as exc:  # noqa: BLE001
            # CostMeter NEVER fails a host LLM call. Spec: "Failure to emit
            # a cost event MUST be logged at WARN level and MUST NOT fail the
            # host LLM/tool call."
            self._log.warning("cost_llm_emit_failed", model=model_name, error=str(exc))

    def _spawn(self, coro: Any) -> None:
        try:
            loop = asyncio.get_running_loop()
        except RuntimeError:
            # No running loop (e.g. called from sync test); run synchronously.
            asyncio.run(coro)
            return
        loop.create_task(coro)


class _TokenUsage:
    __slots__ = ("input_tokens", "output_tokens", "cached_tokens")

    def __init__(
        self,
        input_tokens: int | None = None,
        output_tokens: int | None = None,
        cached_tokens: int | None = None,
    ) -> None:
        self.input_tokens = input_tokens
        self.output_tokens = output_tokens
        self.cached_tokens = cached_tokens


def _extract_model_name(serialized: dict[str, Any], kwargs: dict[str, Any]) -> str:
    invocation_params = kwargs.get("invocation_params") or {}
    for key in ("model_name", "model", "model_id"):
        if value := invocation_params.get(key):
            return str(value)
    # ``serialized`` may have a more reliable name on some providers.
    for key in ("name", "id"):
        if value := serialized.get(key):
            return str(value)
    return "unknown"


def _extract_token_usage(response: Any) -> _TokenUsage:
    """Best-effort token extraction across OpenAI / Anthropic shapes.

    Returns a :class:`_TokenUsage` whose fields may be ``None`` when the
    provider did not report numbers (e.g. streaming completions). Spec: when
    tokens are missing the event still emits with ``input_tokens=null``.
    """
    # OpenAI-shape: response.llm_output["token_usage"]
    llm_output = getattr(response, "llm_output", None) or {}
    token_usage = llm_output.get("token_usage") if isinstance(llm_output, dict) else None
    if isinstance(token_usage, dict):
        return _TokenUsage(
            input_tokens=_int_or_none(token_usage.get("prompt_tokens")),
            output_tokens=_int_or_none(token_usage.get("completion_tokens")),
            cached_tokens=_int_or_none(token_usage.get("cached_tokens")),
        )

    # Anthropic-shape: generations[0][0].generation_info["usage"]
    generations = getattr(response, "generations", None)
    if generations:
        first_batch = generations[0] if generations else None
        if first_batch:
            gen = first_batch[0] if isinstance(first_batch, list) else first_batch
            gen_info = getattr(gen, "generation_info", None) or {}
            usage = gen_info.get("usage") if isinstance(gen_info, dict) else None
            if isinstance(usage, dict):
                return _TokenUsage(
                    input_tokens=_int_or_none(usage.get("input_tokens")),
                    output_tokens=_int_or_none(usage.get("output_tokens")),
                    cached_tokens=_int_or_none(usage.get("cache_read_input_tokens")),
                )

    return _TokenUsage()


def _int_or_none(value: Any) -> int | None:
    if value is None:
        return None
    try:
        return int(value)
    except TypeError, ValueError:
        return None


def cost_metered_tool(tool_name: str):  # type: ignore[no-untyped-def]
    """Decorator that wraps an async tool to emit ``cost.tool`` events.

    The wrapped callable's first positional argument MUST be a ``RunContext``
    (so the meter can find the cost publisher). Failure to emit is logged at
    WARN and does NOT fail the host tool call (spec).
    """

    def decorator(func):  # type: ignore[no-untyped-def]
        @functools.wraps(func)
        async def wrapper(ctx, *args, **kwargs):  # type: ignore[no-untyped-def]
            started = time.monotonic()
            try:
                return await func(ctx, *args, **kwargs)
            finally:
                duration_ms = int((time.monotonic() - started) * 1000)
                meter: CostMeter | None = getattr(ctx, "cost_meter", None)
                if meter is not None:
                    try:
                        await meter.emit_tool(tool_name=tool_name, duration_ms=duration_ms)
                    except Exception as exc:  # noqa: BLE001
                        structlog.get_logger().warning(
                            "cost_tool_decorator_emit_failed",
                            tool=tool_name,
                            error=str(exc),
                        )

        return wrapper

    return decorator
