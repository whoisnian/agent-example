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
) -> list[ProducedArtifact]:
    """Run the plan→execute→critic loop, returning the artifacts produced.

    Resumes from the latest checkpoint when present (plan restored without
    re-planning). Raises on cancel (``asyncio.CancelledError``), deadline, or
    retry-budget exhaustion so the consumer applies its requeue / error policy.

    ``roles`` supplies the planner / executor / critic instructions; production
    passes those resolved from the subagent plugins, and direct callers default
    to :data:`DEFAULT_ROLE_INSTRUCTIONS`.
    """
    cp = ctx.checkpoint_store
    log = ctx.logger.bind(component="agent_loop")

    plan, resume_from = await _load_or_create_plan(
        ctx, message, model, system_prompt, cp, log, roles
    )

    produced: list[ProducedArtifact] = []
    for idx in range(resume_from, len(plan)):
        await _check_boundary(ctx, deadline_ts)
        title = plan[idx]
        step_start = time.monotonic()
        verdict, summary, step_artifacts = await _run_step(
            ctx, model, system_prompt, message, title, write_file, max_step_retries, log, roles
        )
        produced.extend(step_artifacts)

        ctx.step = idx + 1
        await _safe_checkpoint(
            cp,
            step_seq=ctx.step,
            step_name=title,
            state={
                "plan": _plan_state(plan, completed_through=idx),
                "step_count": len(plan),
                "current": {
                    "idx": idx,
                    "title": title,
                    "verdict": verdict,
                    "result_summary": summary[:_SUMMARY_CAP],
                },
            },
            log=log,
        )
        await _emit(
            ctx,
            kind="step",
            payload={
                "step_seq": ctx.step,
                "title": title,
                "verdict": verdict,
                "summary": summary[:_SUMMARY_CAP],
            },
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

    return produced


async def _load_or_create_plan(
    ctx: RunContext,
    message: TaskExecuteMessage,
    model: BaseChatModel,
    system_prompt: str,
    cp: CheckpointStore,
    log: Any,
    roles: RoleInstructions,
) -> tuple[list[str], int]:
    """Restore the plan from the latest checkpoint, or plan afresh."""
    latest = await cp.latest()
    if latest is not None and "plan" in latest.state:
        plan = [entry["title"] for entry in latest.state["plan"]]
        ctx.step = latest.step_seq
        log.info("agent_resumed", resume_from=latest.step_seq, step_count=len(plan))
        return plan, latest.step_seq

    plan = await _plan(ctx, model, system_prompt, message.prompt, roles.planner)
    await _emit(ctx, kind="plan", payload={"steps": plan, "step_count": len(plan)})
    await _safe_checkpoint(
        cp,
        step_seq=0,
        step_name="plan",
        state={"plan": _plan_state(plan, completed_through=-1), "step_count": len(plan)},
        log=log,
    )
    ctx.step = 0
    return plan, 0


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
) -> tuple[str, str, list[ProducedArtifact]]:
    """Execute + critique one step with bounded retries."""
    attempt = 0
    while True:
        result = await _execute(ctx, model, system_prompt, title, message.prompt, roles.executor)
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
) -> dict[str, Any]:
    content = f"Overall task: {prompt}\nCurrent step: {title}"
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


def _plan_state(plan: list[str], *, completed_through: int) -> list[dict[str, Any]]:
    return [{"idx": i, "title": t, "done": i <= completed_through} for i, t in enumerate(plan)]


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


async def _emit(ctx: RunContext, *, kind: str, payload: dict[str, Any]) -> None:
    await ctx.event_publisher.publish_event(
        task_id=str(ctx.task_id),
        version_id=str(ctx.version_id),
        run_id=str(ctx.run_id),
        task_type=ctx.task_type,
        kind=kind,
        payload=payload,
        seq=ctx.next_event_seq(),
        traceparent=ctx.traceparent,
    )
