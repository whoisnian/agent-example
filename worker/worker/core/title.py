"""Semantic task-title generation (spec: worker-execution-runtime →
"Semantic Title Generation").

When the consumed execute message carries ``gen_title=true`` and the run is
fresh, the :class:`TitleGenerator` makes one small LLM call to produce a
human-readable title from the prompt and emits it as a ``kind="title"`` task
event. Generation is strictly best-effort: any failure is logged + counted and
the run proceeds with the API-derived placeholder title.
"""

from __future__ import annotations

import asyncio
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from worker.agents.model import ModelFactory
    from worker.agents.registry import AgentRegistry
    from worker.core.messages import TaskExecuteMessage
    from worker.core.metrics import Metrics
    from worker.core.run_context import RunContext

# 净化与截断口径：最终串（含截断追加的 …）≤64 rune 且 ≤200 字节，与 ingest 侧一致。
_MAX_TITLE_RUNES = 64
_MAX_TITLE_BYTES = 200
_ELLIPSIS = "…"
_PROMPT_INPUT_LIMIT = 2000
_DEFAULT_TIMEOUT_SECONDS = 10.0

_QUOTE_PAIRS = {
    '"': '"',
    "'": "'",
    "“": "”",
    "‘": "’",
    "「": "」",
    "『": "』",
    "《": "》",
}

_SYSTEM_INSTRUCTION = (
    "You generate a title for a task based on the user's prompt. "
    "Reply with exactly one concise line (at most 15 words) in the same "
    "language as the prompt. No quotes, no trailing punctuation, no explanation."
)


def sanitize_title(raw: str) -> str:
    """Normalize raw model output into a title, or ``""`` when unusable.

    First non-empty line → strip wrapping quote pairs → collapse whitespace →
    truncate on a rune boundary so the result, including the appended
    ``…``, stays within 64 runes and 200 bytes.
    """
    line = next((ln.strip() for ln in raw.splitlines() if ln.strip()), "")
    while len(line) >= 2 and _QUOTE_PAIRS.get(line[0]) == line[-1]:
        line = line[1:-1].strip()
    line = " ".join(line.split())
    return _truncate(line)


def _truncate(text: str) -> str:
    if len(text) <= _MAX_TITLE_RUNES and len(text.encode("utf-8")) <= _MAX_TITLE_BYTES:
        return text
    max_runes = _MAX_TITLE_RUNES - 1
    max_bytes = _MAX_TITLE_BYTES - len(_ELLIPSIS.encode("utf-8"))
    prefix = text[:max_runes]
    while prefix and len(prefix.encode("utf-8")) > max_bytes:
        prefix = prefix[:-1]
    prefix = prefix.rstrip()
    if not prefix:
        return ""
    return prefix + _ELLIPSIS


class TitleGenerator:
    """One-shot semantic title generation, gated and best-effort.

    Guards (all must hold, otherwise skip with zero LLM calls):
    ``gen_title=true`` on the message; fresh run (no prior checkpoint);
    cancel token not set; an agent registered for the ``task_type``.
    """

    def __init__(
        self,
        *,
        model_factory: ModelFactory,
        agent_registry: AgentRegistry,
        title_model_key: str | None,
        metrics: Metrics,
        timeout_seconds: float = _DEFAULT_TIMEOUT_SECONDS,
    ) -> None:
        self._factory = model_factory
        self._registry = agent_registry
        self._title_model_key = title_model_key
        self._metrics = metrics
        self._timeout = timeout_seconds

    async def maybe_generate(self, ctx: RunContext, msg: TaskExecuteMessage) -> None:
        """Generate + publish the title event, or skip/fail without raising."""
        if not msg.gen_title:
            return
        agent = self._registry.get(msg.task_type)
        if agent is None:
            ctx.logger.info("title_generation_skipped", reason="unregistered_task_type")
            return
        if ctx.cancel_token.is_set():
            ctx.logger.info("title_generation_skipped", reason="cancelled")
            return
        if await ctx.checkpoint_store.latest() is not None:
            # 重投 / stale-heartbeat 接管：占位或上次生成的标题保留，避免重复
            # 生成（fresh-run 守卫；残余窗口由 ingest 的 last-write-wins 兜底）。
            ctx.logger.info("title_generation_skipped", reason="not_fresh")
            return

        try:
            await asyncio.wait_for(
                self._generate(ctx, msg, model_key=self._title_model_key or agent.spec.model_key),
                timeout=self._timeout,
            )
        except Exception as exc:  # noqa: BLE001 - best-effort by spec
            ctx.logger.warning("title_generation_failed", error=str(exc))
            self._metrics.title_generation_failures_total.inc()

    async def _generate(self, ctx: RunContext, msg: TaskExecuteMessage, *, model_key: str) -> None:
        from langchain_core.messages import HumanMessage, SystemMessage

        model = self._factory.get(model_key)
        response = await model.ainvoke(
            [
                SystemMessage(content=_SYSTEM_INSTRUCTION),
                HumanMessage(content=msg.prompt[:_PROMPT_INPUT_LIMIT]),
            ],
            config={"callbacks": [ctx.cost_meter]},
        )
        title = sanitize_title(_as_text(response.content))
        if not title:
            raise ValueError("model output sanitized to empty title")
        await ctx.event_publisher.publish_event(
            task_id=str(msg.task_id),
            version_id=str(msg.version_id),
            run_id=str(msg.run_id),
            task_type=msg.task_type,
            kind="title",
            payload={"title": title},
            seq=ctx.next_event_seq(),
            traceparent=ctx.traceparent,
        )
        ctx.logger.info("title_generated", title=title)


def _as_text(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(
            block.get("text", "") if isinstance(block, dict) else str(block) for block in content
        )
    return str(content)
