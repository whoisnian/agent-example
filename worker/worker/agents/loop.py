"""Planner / executor / critic orchestration loop (design D4).

The worker — not the agent framework — owns step sequencing, checkpoint writes,
event emission, and pause/cancel/deadline checks at step boundaries. Each role
is a single, deterministic chat-model invocation with a role-specific prompt:

- **planner** → ordered step list (one ``plan`` event + ``step_seq=0`` checkpoint)
- **executor** → per step: a summary + optional files to write (via ``write_file``)
- **critic**   → per step: ``advance`` | ``retry`` | ``finish``

Driving the model directly per role (rather than handing the whole task to one
opaque ``create_deep_agent`` graph) is what makes the per-step checkpoint /
event cadence and the fake-model tests deterministic — see the slice-B note in
the proposal/design about this deviation from "Deep Agent Assembly".

Cost events are emitted by attaching ``ctx.cost_meter`` to every model call and
by routing file writes through the ``cost_metered_tool``-wrapped ``write_file``.
"""

from __future__ import annotations

import asyncio
import json
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

from langchain_core.messages import HumanMessage, SystemMessage

from worker.agents.context_block import assemble_context_block
from worker.agents.subagent import RoleInstructions
from worker.core.persistence import CheckpointConflictError

if TYPE_CHECKING:
    from langchain_core.language_models.chat_models import BaseChatModel

    from worker.core.checkpoint import CheckpointStore
    from worker.core.messages import TaskExecuteMessage
    from worker.core.run_context import RunContext


@dataclass(frozen=True, slots=True)
class ProducedArtifact:
    """A file the executor wrote to OSS during the run."""

    path: str
    oss_key: str
    bytes: int
    sha256: str
    mime: str | None = None


WriteFile = Callable[["RunContext", str, str], Awaitable[ProducedArtifact]]


@dataclass(frozen=True, slots=True)
class LoopResult:
    """What a completed loop hands back to the agent.

    ``step_summaries`` is indexed by plan position and includes summaries
    restored from checkpoints (steps executed by earlier attempts), so the
    run-summary event covers the whole run, not just this attempt
    (spec: worker-agent-orchestration → "Run Summary Event").
    """

    artifacts: list[ProducedArtifact]
    step_summaries: list[str | None]


class StepRetryBudgetExceeded(RuntimeError):
    """Raised when a step exhausts its critic-retry budget (design D13)."""

    def __init__(self, step_idx: int) -> None:
        super().__init__(f"step {step_idx} exceeded retry budget")
        self.step_idx = step_idx


class DeadlineExceededError(RuntimeError):
    """Raised at a step boundary when the run's deadline has passed (design D14)."""


_PLANNER_INSTRUCTION = (
    "You are the PLANNER. Decompose the task into an ordered list of concrete steps. "
    'Respond with JSON only: {"steps": ["step title", ...]}.'
)
_EXECUTOR_INSTRUCTION = (
    "You are the EXECUTOR. Perform the given step. If you produce files, include them. "
    'Respond with JSON only: {"summary": "...", "files": [{"path": "...", "content": "..."}]}.'
)
_CRITIC_INSTRUCTION = (
    "You are the CRITIC. Judge the step result and decide what to do next. "
    'Respond with JSON only: {"verdict": "advance" | "retry" | "finish"}.'
)

#: Fallback role instructions for direct callers (e.g. tests) that supply no
#: ``roles``. In production the agent passes the instructions resolved from the
#: planner / executor / critic subagent plugins (whose ``prompt.md`` files carry
#: this same text); see add-worker-subagent-plugin.
DEFAULT_ROLE_INSTRUCTIONS = RoleInstructions(
    planner=_PLANNER_INSTRUCTION,
    executor=_EXECUTOR_INSTRUCTION,
    critic=_CRITIC_INSTRUCTION,
)

_SUMMARY_CAP = 500

#: Byte cap (incl. the appended ellipsis) for the run-summary event payload —
#: mirrors the API-side ApplyVersionSummary sanitizer so a worker-clean
#: summary is never re-truncated on ingest.
_RUN_SUMMARY_MAX_BYTES = 2048


def assemble_run_summary(step_summaries: list[str | None]) -> str:
    """Deterministic run summary: one ``<step_seq>. <summary>`` line per
    completed step (prior attempts included via checkpoint restore), truncated
    on a rune boundary to ≤ 2048 bytes. No LLM call (spec: "Run Summary Event").
    """
    lines = [f"{i + 1}. {s}" for i, s in enumerate(step_summaries) if s]
    text = "\n".join(lines)
    if len(text.encode("utf-8")) <= _RUN_SUMMARY_MAX_BYTES:
        return text
    ellipsis = "…"
    budget = _RUN_SUMMARY_MAX_BYTES - len(ellipsis.encode("utf-8"))
    encoded = text.encode("utf-8")[:budget]
    # A byte-slice may split a rune; decoding with errors="ignore" drops the
    # partial trailing rune, landing exactly on a rune boundary.
    return encoded.decode("utf-8", errors="ignore").rstrip() + ellipsis


async def run_agent_loop(
    ctx: RunContext,
    message: TaskExecuteMessage,
    *,
    model: BaseChatModel,
    system_prompt: str,
    write_file: WriteFile,
    max_step_retries: int,
    deadline_ts: int | None = None,
    metrics: Any | None = None,
    roles: RoleInstructions = DEFAULT_ROLE_INSTRUCTIONS,
    inherited: list[tuple[str, int]] | None = None,
) -> LoopResult:
    """Run the plan→execute→critic loop, returning artifacts + step summaries.

    Resumes from the latest checkpoint when present (plan restored without
    re-planning). Raises on cancel (``asyncio.CancelledError``), deadline, or
    retry-budget exhaustion so the consumer applies its requeue / error policy.

    ``roles`` supplies the planner / executor / critic instructions; production
    passes those resolved from the subagent plugins, and direct callers default
    to :data:`DEFAULT_ROLE_INSTRUCTIONS`.

    ``inherited`` is the (relative key, size) inventory the inheritance copy
    produced on a fresh run; together with ``message.history`` it feeds the
    conversation-context block prepended to planner / executor inputs.
    """
    cp = ctx.checkpoint_store
    log = ctx.logger.bind(component="agent_loop")

    plan, resume_from, summaries, context_block = await _load_or_create_plan(
        ctx, message, model, system_prompt, cp, log, roles, inherited or []
    )

    produced: list[ProducedArtifact] = []
    for idx in range(resume_from, len(plan)):
        await _check_boundary(ctx, deadline_ts)
        title = plan[idx]
        step_start = time.monotonic()
        verdict, summary, step_artifacts = await _run_step(
            ctx,
            model,
            system_prompt,
            message,
            title,
            write_file,
            max_step_retries,
            log,
            roles,
            context_block,
        )
        produced.extend(step_artifacts)
        summaries[idx] = summary[:_SUMMARY_CAP]

        ctx.step = idx + 1
        # Reserve the step event's seq BEFORE the checkpoint so the persisted
        # high-water mark covers it: a crash between checkpoint and emit then
        # leaves a harmless gap on resume instead of a (run_id, seq) collision
        # (spec: "Resume-Safe Event Sequencing"). Concurrent control acks may
        # still slip a seq in after the snapshot — max() on restore plus
        # at-most-one lost ack bounds that rare race.
        step_event_seq = ctx.next_event_seq()
        step_state: dict[str, Any] = {
            "plan": _plan_state(plan, completed_through=idx, summaries=summaries),
            "step_count": len(plan),
            "event_seq": ctx.event_seq,
            "current": {
                "idx": idx,
                "title": title,
                "verdict": verdict,
                "result_summary": summary[:_SUMMARY_CAP],
            },
        }
        # Carried forward on every checkpoint: resume reads only latest(), so
        # the block must ride along or a resume from step >= 1 would lose it.
        if context_block:
            step_state["context_block"] = context_block
        await _safe_checkpoint(cp, step_seq=ctx.step, step_name=title, state=step_state, log=log)
        await _emit(
            ctx,
            kind="step",
            payload={
                "step_seq": ctx.step,
                "title": title,
                "verdict": verdict,
                "summary": summary[:_SUMMARY_CAP],
            },
            seq=step_event_seq,
        )
        if metrics is not None:
            metrics.agent_steps_total.labels(ctx.task_type).inc()
            metrics.agent_step_duration_seconds.observe(time.monotonic() - step_start)
        log.info(
            "agent_step_done",
            task_id=str(ctx.task_id),
            run_id=str(ctx.run_id),
            version_id=str(ctx.version_id),
            step=ctx.step,
            verdict=verdict,
        )
        if verdict == "finish":
            break

    return LoopResult(artifacts=produced, step_summaries=summaries)


async def _load_or_create_plan(
    ctx: RunContext,
    message: TaskExecuteMessage,
    model: BaseChatModel,
    system_prompt: str,
    cp: CheckpointStore,
    log: Any,
    roles: RoleInstructions,
    inherited: list[tuple[str, int]],
) -> tuple[list[str], int, list[str | None], str]:
    """Restore plan / summaries / context block from the latest checkpoint, or start afresh."""
    latest = await cp.latest()
    if latest is not None and "plan" in latest.state:
        plan = [entry["title"] for entry in latest.state["plan"]]
        summaries: list[str | None] = [
            entry.get("result_summary") for entry in latest.state["plan"]
        ]
        ctx.step = latest.step_seq
        # Defense in depth for direct-loop callers: the consumer already
        # restored the high-water mark before any emit; re-applying is a no-op.
        ctx.restore_event_seq(int(latest.state.get("event_seq", 0)))
        # Resume reuses the checkpointed context block verbatim — never
        # re-listing OSS or re-reading contents (spec: "Resume restores the
        # context block from the checkpoint").
        context_block = str(latest.state.get("context_block", ""))
        log.info("agent_resumed", resume_from=latest.step_seq, step_count=len(plan))
        return plan, latest.step_seq, summaries, context_block

    # Fresh run: assemble the conversation-context block exactly once, from
    # the message history + the inheritance copy inventory (no LLM call).
    context_block = await assemble_context_block(ctx, list(message.history), inherited)
    if context_block:
        log.info(
            "context_block_assembled",
            context_bytes=len(context_block.encode("utf-8")),
            history_turns=len(message.history),
            inherited_files=len(inherited),
        )

    planner_input = f"{context_block}\n\n{message.prompt}" if context_block else message.prompt
    plan = await _plan(ctx, model, system_prompt, planner_input, roles.planner)
    await _emit(ctx, kind="plan", payload={"steps": plan, "step_count": len(plan)})
    state: dict[str, Any] = {
        "plan": _plan_state(plan, completed_through=-1),
        "step_count": len(plan),
        "event_seq": ctx.event_seq,
    }
    if context_block:
        state["context_block"] = context_block
    await _safe_checkpoint(cp, step_seq=0, step_name="plan", state=state, log=log)
    ctx.step = 0
    return plan, 0, [None] * len(plan), context_block


async def _run_step(
    ctx: RunContext,
    model: BaseChatModel,
    system_prompt: str,
    message: TaskExecuteMessage,
    title: str,
    write_file: WriteFile,
    max_step_retries: int,
    log: Any,
    roles: RoleInstructions,
    context_block: str = "",
) -> tuple[str, str, list[ProducedArtifact]]:
    """Execute + critique one step with bounded retries."""
    attempt = 0
    while True:
        result = await _execute(
            ctx, model, system_prompt, title, message.prompt, roles.executor, context_block
        )
        summary = str(result.get("summary", ""))
        attempt_artifacts: list[ProducedArtifact] = []
        for f in result.get("files", []) or []:
            attempt_artifacts.append(await write_file(ctx, f["path"], f.get("content", "")))

        verdict = await _critic(ctx, model, system_prompt, title, summary, roles.critic)
        if verdict == "retry":
            if attempt < max_step_retries:
                attempt += 1
                log.info("agent_step_retry", title=title, attempt=attempt)
                continue
            raise StepRetryBudgetExceeded(message.attempt_no)
        return verdict, summary, attempt_artifacts


# --- role invocations ------------------------------------------------------


async def _plan(
    ctx: RunContext, model: BaseChatModel, system_prompt: str, prompt: str, instruction: str
) -> list[str]:
    data = await _invoke_json(ctx, model, system_prompt, instruction, prompt)
    steps = data.get("steps", [])
    out = [s["title"] if isinstance(s, dict) else str(s) for s in steps]
    if not out:
        raise ValueError("planner returned no steps")
    return out


async def _execute(
    ctx: RunContext,
    model: BaseChatModel,
    system_prompt: str,
    title: str,
    prompt: str,
    instruction: str,
    context_block: str = "",
) -> dict[str, Any]:
    # Context block precedes the task framing; with no block the input stays
    # byte-identical to the pre-change composition (compatibility invariant).
    content = f"Overall task: {prompt}\nCurrent step: {title}"
    if context_block:
        content = f"{context_block}\n\n{content}"
    return await _invoke_json(ctx, model, system_prompt, instruction, content)


async def _critic(
    ctx: RunContext,
    model: BaseChatModel,
    system_prompt: str,
    title: str,
    summary: str,
    instruction: str,
) -> str:
    content = f"Step: {title}\nResult summary: {summary}"
    data = await _invoke_json(ctx, model, system_prompt, instruction, content)
    verdict = str(data.get("verdict", "advance")).lower()
    if verdict not in {"advance", "retry", "finish"}:
        verdict = "advance"
    return verdict


async def _invoke_json(
    ctx: RunContext,
    model: BaseChatModel,
    system_prompt: str,
    role_instruction: str,
    content: str,
) -> dict[str, Any]:
    messages = [
        SystemMessage(content=f"{system_prompt}\n\n{role_instruction}"),
        HumanMessage(content=content),
    ]
    # Attaching the CostMeter here is what emits cost.llm for every model call.
    response = await model.ainvoke(messages, config={"callbacks": [ctx.cost_meter]})
    return _extract_json(_as_text(response.content))


# --- helpers ---------------------------------------------------------------


def _as_text(content: Any) -> str:
    if isinstance(content, str):
        return content
    # Some chat models return a list of content blocks.
    if isinstance(content, list):
        return "".join(
            block.get("text", "") if isinstance(block, dict) else str(block) for block in content
        )
    return str(content)


def _extract_json(text: str) -> dict[str, Any]:
    """Parse a JSON object from model output, tolerating surrounding prose."""
    try:
        parsed = json.loads(text)
        if isinstance(parsed, dict):
            return parsed
    except json.JSONDecodeError:
        pass
    start = text.find("{")
    end = text.rfind("}")
    if start != -1 and end != -1 and end > start:
        try:
            parsed = json.loads(text[start : end + 1])
            if isinstance(parsed, dict):
                return parsed
        except json.JSONDecodeError:
            pass
    raise ValueError(f"could not parse JSON from model output: {text[:120]!r}")


def _plan_state(
    plan: list[str],
    *,
    completed_through: int,
    summaries: list[str | None] | None = None,
) -> list[dict[str, Any]]:
    """Plan entries for checkpoint state.

    Completed entries carry their executor ``result_summary`` (≤ _SUMMARY_CAP)
    so a resumed attempt can reassemble the full run summary — including steps
    a prior attempt executed (spec: "Run Summary Event").
    """
    out: list[dict[str, Any]] = []
    for i, t in enumerate(plan):
        entry: dict[str, Any] = {"idx": i, "title": t, "done": i <= completed_through}
        if summaries is not None and i <= completed_through and summaries[i] is not None:
            entry["result_summary"] = summaries[i]
        out.append(entry)
    return out


async def _check_boundary(ctx: RunContext, deadline_ts: int | None) -> None:
    """Cooperative cancel / pause / deadline check at a step boundary.

    Reached only after the previous step's checkpoint is durable, so blocking
    on pause here cannot lose progress (design D14 / spec "Cooperative Pause").
    """
    if ctx.cancel_token.is_set():
        raise asyncio.CancelledError
    await ctx.pause_token.wait_if_paused()
    if deadline_ts is not None and time.time() > deadline_ts:
        raise DeadlineExceededError(f"run deadline {deadline_ts} exceeded")


async def _safe_checkpoint(
    cp: CheckpointStore,
    *,
    step_seq: int,
    step_name: str,
    state: dict[str, Any],
    log: Any,
) -> None:
    """Write a checkpoint, treating a replay conflict as 'already persisted'."""
    try:
        await cp.write(step_seq=step_seq, step_name=step_name, state=state)
    except CheckpointConflictError:
        log.info("checkpoint_replay_skip", step_seq=step_seq)


async def _emit(
    ctx: RunContext, *, kind: str, payload: dict[str, Any], seq: int | None = None
) -> None:
    """Publish a task event. ``seq`` lets the step path pre-reserve its seq
    before the checkpoint write (see "Resume-Safe Event Sequencing")."""
    await ctx.event_publisher.publish_event(
        task_id=str(ctx.task_id),
        version_id=str(ctx.version_id),
        run_id=str(ctx.run_id),
        task_type=ctx.task_type,
        kind=kind,
        payload=payload,
        seq=seq if seq is not None else ctx.next_event_seq(),
        traceparent=ctx.traceparent,
    )
