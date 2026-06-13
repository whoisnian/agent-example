"""Agent contract and deep-agent assembly (design D1/D2).

An :class:`Agent` is what the :class:`~worker.core.dispatcher.ExecutionDispatcher`
looks up by ``task_type`` and runs. Concrete agents are built from a static
:class:`AgentSpec` (system prompt, tools, subagents, model key, limits) that is
validated once at startup; the underlying ``deepagents`` graph is constructed
per run via :func:`build_deep_agent` so per-run state (the ``CostMeter``
callback, the OSS-scoped filesystem, cancel/pause tokens) binds to that run's
``RunContext`` rather than to a process-global singleton.
"""

from __future__ import annotations

import asyncio
from collections.abc import Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import TYPE_CHECKING, Any, Protocol, runtime_checkable

from worker.agents.inherit import inherit_parent_artifacts
from worker.agents.loop import DEFAULT_ROLE_INSTRUCTIONS, assemble_run_summary, run_agent_loop

if TYPE_CHECKING:
    from langchain_core.tools import BaseTool

    from worker.agents.loop import DeleteFile, ProducedArtifact, WriteFile
    from worker.agents.model import ModelFactory
    from worker.agents.subagent import RoleInstructions
    from worker.core.messages import TaskExecuteMessage
    from worker.core.run_context import RunContext


@dataclass(frozen=True, slots=True)
class AgentSpec:
    """Static, validatable description of an agent.

    ``model_key`` is resolved through the :class:`ModelFactory` at run time.
    ``tool_names`` must each resolve to a registered ``tool`` plugin.
    ``subagent_names`` must each resolve to a registered ``subagent`` plugin;
    the loop sources its planner / executor / critic instructions from them.
    ``subagents`` are ``deepagents``-compatible subagent definitions passed
    through to ``create_deep_agent`` on the optional :func:`build_deep_agent`
    path (distinct from ``subagent_names``, which feeds the MVP loop).
    """

    task_type: str
    model_key: str
    system_prompt_path: Path
    tool_names: tuple[str, ...] = ()
    subagent_names: tuple[str, ...] = ()
    subagents: tuple[Any, ...] = ()
    limits: dict[str, Any] = field(default_factory=dict)

    def read_system_prompt(self) -> str:
        return self.system_prompt_path.read_text(encoding="utf-8")


@runtime_checkable
class Agent(Protocol):
    """Runnable agent resolved by the dispatcher.

    Implementations MUST reach infrastructure only through ``ctx`` (no direct
    MQ/DB/OSS imports) and MUST return normally on success so the consumer
    marks the run ``succeeded``.
    """

    @property
    def task_type(self) -> str: ...

    @property
    def spec(self) -> AgentSpec: ...

    async def run(self, ctx: RunContext, message: TaskExecuteMessage) -> None: ...


def build_deep_agent(
    spec: AgentSpec,
    ctx: RunContext,
    model_factory: ModelFactory,
    tools: Sequence[BaseTool],
) -> Any:
    """Construct a per-run ``deepagents`` graph for ``spec``.

    Built per consumed message (design D2). This is the richer-reasoning
    assembly path; the MVP :class:`LoopAgent` instead drives the model directly
    per role for deterministic, fake-model-testable per-step control (see the
    slice-B note in design.md re: "Deep Agent Assembly"). Kept available for
    agents/steps that want the framework's internal tool-calling loop.
    """
    from deepagents import create_deep_agent

    return create_deep_agent(
        model=model_factory.get(spec.model_key),
        tools=list(tools),
        system_prompt=spec.read_system_prompt(),
        subagents=list(spec.subagents) or None,
    )


class LoopAgent:
    """Concrete :class:`Agent` that runs the planner/executor/critic loop.

    Generic over task type — the code-gen and research agents are just this
    class with different :class:`AgentSpec`s. On success it records an
    ``artifacts`` row (the only business table the worker may write) for each
    file the executor produced; a failure to persist or upload propagates so
    the run is reported failed rather than falsely ``succeeded`` (design D7).
    """

    def __init__(
        self,
        *,
        spec: AgentSpec,
        model_factory: ModelFactory,
        persistence: Any,
        write_file: WriteFile,
        max_step_retries: int,
        metrics: Any | None = None,
        roles: RoleInstructions = DEFAULT_ROLE_INSTRUCTIONS,
        delete_file: DeleteFile | None = None,
    ) -> None:
        self._spec = spec
        self._model_factory = model_factory
        self._persistence = persistence
        self._write_file = write_file
        self._delete_file = delete_file
        self._max_step_retries = max_step_retries
        self._metrics = metrics
        self._roles = roles

    @property
    def task_type(self) -> str:
        return self._spec.task_type

    @property
    def spec(self) -> AgentSpec:
        return self._spec

    @property
    def roles(self) -> RoleInstructions:
        """The planner/executor/critic instructions this agent runs the loop with."""
        return self._roles

    async def run(self, ctx: RunContext, message: TaskExecuteMessage) -> None:
        model = self._model_factory.get(self._spec.model_key)
        inherited = await self._maybe_inherit_parent_artifacts(ctx, message)
        try:
            result = await run_agent_loop(
                ctx,
                message,
                model=model,
                system_prompt=self._spec.read_system_prompt(),
                write_file=self._write_file,
                max_step_retries=self._max_step_retries,
                deadline_ts=message.deadline_ts,
                metrics=self._metrics,
                roles=self._roles,
                inherited=inherited,
                persist_artifact=self._persist_artifact,
                delete_file=self._delete_file,
                delete_artifact=self._delete_artifact,
            )
            # Artifact rows + `kind="artifact"` events are written per-step
            # inside the loop (improve-artifact-conversation-ux), not batched
            # here — so a produced file is visible mid-run and a failed run
            # keeps its partial outputs. Run summary: after the final step's
            # artifacts, before returning — failed / cancelled runs raise above
            # and never reach this (spec: "Run Summary Event"). The ingest side
            # persists the version summary.
            await self._emit_summary(ctx, result.step_summaries)
        except asyncio.CancelledError:
            self._record_run("cancelled")
            raise
        except BaseException:
            self._record_run("error")
            raise
        self._record_run("success")

    async def _persist_artifact(self, ctx: RunContext, art: ProducedArtifact) -> str:
        """Upsert one produced artifact's row, returning its id for the event
        payload. The loop calls this at each step boundary BEFORE the step
        checkpoint (improve-artifact-conversation-ux); `artifacts` is the only
        business table the worker writes."""
        artifact_id = await self._persistence.insert_artifact(
            version_id=ctx.version_id,
            kind="file",
            oss_key=art.oss_key,
            path=art.path,
            mime=art.mime,
            bytes_size=art.bytes,
            sha256=art.sha256,
        )
        return str(artifact_id)

    async def _delete_artifact(self, ctx: RunContext, path: str) -> bool:
        """Delete the running version's ``(version_id, path)`` artifact row,
        returning whether a row was removed. Scoped to the run's own version —
        the only delete the worker is permitted on the `artifacts` table
        (add-artifact-deletion)."""
        return await self._persistence.delete_artifact(version_id=ctx.version_id, path=path)

    async def _emit_summary(self, ctx: RunContext, step_summaries: list[str | None]) -> None:
        summary = assemble_run_summary(step_summaries)
        await ctx.event_publisher.publish_event(
            task_id=str(ctx.task_id),
            version_id=str(ctx.version_id),
            run_id=str(ctx.run_id),
            task_type=ctx.task_type,
            kind="summary",
            payload={"summary": summary},
            seq=ctx.next_event_seq(),
            traceparent=ctx.traceparent,
        )
        if self._metrics is not None:
            self._metrics.summary_events_total.inc()
        ctx.logger.info("summary_emitted", summary_bytes=len(summary.encode("utf-8")))

    async def _maybe_inherit_parent_artifacts(
        self, ctx: RunContext, message: TaskExecuteMessage
    ) -> list[tuple[str, int]]:
        """Seed the new version from the parent's artifacts on a fresh run.

        Gated on ``parent_version_id`` present AND no prior checkpoint (the run
        is fresh, not a resume). The gate is a performance optimization; row-level
        idempotency (the ``insert_artifact`` upsert) is the correctness backstop
        for the crash-before-first-checkpoint window. The worker keys on
        ``parent_version_id``; a non-null ``parent_artifact_root`` is unexpected
        (the API leaves it null) and is logged as a contract warning.

        Returns the copied ``(relative key, size)`` inventory for the
        conversation-context block; empty on skip (no parent) and on resume —
        the resumed loop restores its context block from the plan checkpoint
        instead of re-deriving it.
        """
        if message.parent_artifact_root is not None:
            ctx.logger.warning(
                "parent_artifact_root_unexpected",
                parent_artifact_root=message.parent_artifact_root,
            )
        if message.parent_version_id is None:
            return []
        if await ctx.checkpoint_store.latest() is not None:
            return []  # resume — a prior attempt already inherited
        copied = await inherit_parent_artifacts(ctx, self._persistence, message.parent_version_id)
        ctx.logger.info(
            "artifacts_inherited",
            count=len(copied),
            parent_version_id=str(message.parent_version_id),
        )
        return copied

    def _record_run(self, outcome: str) -> None:
        if self._metrics is not None:
            self._metrics.agent_runs_total.labels(self._spec.task_type, outcome).inc()
