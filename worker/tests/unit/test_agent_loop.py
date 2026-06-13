"""Unit tests for the planner/executor/critic loop (design D4)."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any
from uuid import uuid4

import pytest
import structlog
from langchain_core.callbacks import BaseCallbackHandler
from worker.agents.loop import (
    DeadlineExceededError,
    ExecutorOutputError,
    ProducedArtifact,
    StepRetryBudgetExceeded,
    run_agent_loop,
)
from worker.agents.subagent import RoleInstructions
from worker.core.persistence import CheckpointConflictError
from worker.core.run_context import CancelToken, PauseToken

from tests.support.fake_model import capturing_scripted_model, scripted_model


class FakeCheckpointStore:
    def __init__(self, *, conflict_on: set[int] | None = None) -> None:
        self.writes: list[tuple[int, str, dict[str, Any]]] = []
        self._records: dict[int, SimpleNamespace] = {}
        self._conflict_on = conflict_on or set()
        self.seeded_latest: SimpleNamespace | None = None

    async def write(self, *, step_seq: int, step_name: str, state: dict[str, Any]) -> Any:
        if step_seq in self._conflict_on:
            raise CheckpointConflictError(f"dup {step_seq}")
        self.writes.append((step_seq, step_name, state))
        self._records[step_seq] = SimpleNamespace(
            step_seq=step_seq, step_name=step_name, state=state
        )
        return self._records[step_seq]

    async def latest(self) -> SimpleNamespace | None:
        if self.seeded_latest is not None:
            return self.seeded_latest
        if not self._records:
            return None
        return self._records[max(self._records)]


class FakeEventPublisher:
    def __init__(self) -> None:
        self.events: list[dict[str, Any]] = []

    async def publish_event(self, **kwargs: Any) -> None:
        self.events.append(kwargs)


class _FakeCtx(SimpleNamespace):
    """SimpleNamespace plus the RunContext seq protocol the loop relies on."""

    def next_event_seq(self) -> int:
        self.event_seq += 1
        return self.event_seq

    def restore_event_seq(self, high_water: int) -> None:
        self.event_seq = max(self.event_seq, high_water)


def _make_ctx(cp: FakeCheckpointStore, events: FakeEventPublisher) -> Any:
    return _FakeCtx(
        task_id=uuid4(),
        version_id=uuid4(),
        run_id=uuid4(),
        task_type="code-gen",
        traceparent=None,
        step=0,
        checkpoint_store=cp,
        event_publisher=events,
        cost_meter=BaseCallbackHandler(),
        cancel_token=CancelToken(),
        pause_token=PauseToken(),
        logger=structlog.get_logger(),
        event_seq=0,
    )


def _msg(prompt: str = "build a thing", attempt_no: int = 1, deadline_ts: int | None = None) -> Any:
    return SimpleNamespace(
        prompt=prompt, attempt_no=attempt_no, deadline_ts=deadline_ts, history=[]
    )


async def test_happy_path_plan_steps_finish() -> None:
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    written: list[ProducedArtifact] = []
    model = scripted_model(
        [
            '{"steps": ["step one", "step two"]}',
            '{"summary": "did one", "files": [{"path": "a.py", "content": "print(1)"}]}',
            '{"verdict": "advance"}',
            '{"summary": "did two", "files": []}',
            '{"verdict": "finish"}',
        ]
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        art = ProducedArtifact(path=path, oss_key=f"k/{path}", bytes=len(content), sha256="h")
        written.append(art)
        return art

    produced = await run_agent_loop(
        ctx, _msg(), model=model, system_prompt="sys", write_file=write_file, max_step_retries=2
    )

    # Events: one plan then two steps, in order.
    kinds = [e["kind"] for e in events.events]
    assert kinds == ["plan", "step", "step"]
    assert events.events[0]["payload"]["step_count"] == 2
    assert [e["payload"]["verdict"] for e in events.events[1:]] == ["advance", "finish"]
    # Checkpoints: step_seq 0, 1, 2.
    assert [w[0] for w in cp.writes] == [0, 1, 2]
    # One artifact produced (a.py), ctx.step advanced to 2.
    assert [a.path for a in produced.artifacts] == ["a.py"]
    # Step summaries captured per plan position for the run-summary event.
    assert produced.step_summaries == ["did one", "did two"]
    assert ctx.step == 2


async def test_roles_supply_planner_executor_critic_instructions() -> None:
    """The supplied RoleInstructions reach the model in the per-role system message.

    Drift guard: proves the loop runs with the passed-in role instructions (the
    production path passes those resolved from the subagent plugins) rather than
    ignoring them in favour of a hardcoded default.
    """
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = capturing_scripted_model(
        [
            '{"steps": ["only step"]}',
            '{"summary": "done", "files": []}',
            '{"verdict": "finish"}',
        ]
    )
    roles = RoleInstructions(
        planner="PLANNER_MARKER_XYZ",
        executor="EXECUTOR_MARKER_XYZ",
        critic="CRITIC_MARKER_XYZ",
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    await run_agent_loop(
        ctx,
        _msg(),
        model=model,
        system_prompt="sys",
        write_file=write_file,
        max_step_retries=1,
        roles=roles,
    )

    # The three calls are planner, executor, critic in order; each role's marker
    # MUST appear in that call's SystemMessage (first message).
    system_texts = [str(call[0].content) for call in model.calls]
    assert "PLANNER_MARKER_XYZ" in system_texts[0]
    assert "EXECUTOR_MARKER_XYZ" in system_texts[1]
    assert "CRITIC_MARKER_XYZ" in system_texts[2]


async def test_retry_then_advance() -> None:
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = scripted_model(
        [
            '{"steps": ["only step"]}',
            '{"summary": "attempt 1", "files": []}',
            '{"verdict": "retry"}',
            '{"summary": "attempt 2", "files": []}',
            '{"verdict": "advance"}',
        ]
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    produced = await run_agent_loop(
        ctx, _msg(), model=model, system_prompt="sys", write_file=write_file, max_step_retries=1
    )
    assert produced.artifacts == []
    # Exactly one step checkpoint (step_seq=1) plus the plan (0).
    assert [w[0] for w in cp.writes] == [0, 1]
    assert ctx.step == 1


async def test_retry_budget_exceeded_raises() -> None:
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = scripted_model(
        [
            '{"steps": ["only step"]}',
            '{"summary": "attempt 1", "files": []}',
            '{"verdict": "retry"}',
        ]
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    with pytest.raises(StepRetryBudgetExceeded):
        await run_agent_loop(
            ctx, _msg(), model=model, system_prompt="sys", write_file=write_file, max_step_retries=0
        )


async def test_resume_skips_completed_steps() -> None:
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    # Seed a latest checkpoint at step_seq=1 of a 2-step plan.
    cp.seeded_latest = SimpleNamespace(
        step_seq=1,
        step_name="step one",
        state={
            "plan": [
                {"idx": 0, "title": "step one", "done": True},
                {"idx": 1, "title": "step two", "done": False},
            ],
            "step_count": 2,
        },
    )
    # Only the executor+critic for step two should be consumed (no planner call).
    model = scripted_model(['{"summary": "did two", "files": []}', '{"verdict": "finish"}'])

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    await run_agent_loop(
        ctx, _msg(), model=model, system_prompt="sys", write_file=write_file, max_step_retries=0
    )
    # No plan event (resumed); one step event for step two only.
    assert [e["kind"] for e in events.events] == ["step"]
    assert ctx.step == 2


async def test_cancel_at_boundary_raises() -> None:
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    ctx.cancel_token.set()
    model = scripted_model(['{"steps": ["only step"]}'])

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    import asyncio

    with pytest.raises(asyncio.CancelledError):
        await run_agent_loop(
            ctx, _msg(), model=model, system_prompt="sys", write_file=write_file, max_step_retries=0
        )


async def test_deadline_exceeded_raises() -> None:
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = scripted_model(['{"steps": ["only step"]}'])

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    with pytest.raises(DeadlineExceededError):
        await run_agent_loop(
            ctx,
            _msg(),
            model=model,
            system_prompt="sys",
            write_file=write_file,
            max_step_retries=0,
            deadline_ts=1,  # epoch 1 → long past
        )


async def test_duplicate_checkpoint_conflict_is_swallowed() -> None:
    # step_seq=1 write raises CheckpointConflictError → treated as already-done.
    cp = FakeCheckpointStore(conflict_on={1})
    events = FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = scripted_model(
        ['{"steps": ["only step"]}', '{"summary": "x", "files": []}', '{"verdict": "finish"}']
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    # Must not raise; the step event is still emitted and ctx.step advances.
    await run_agent_loop(
        ctx, _msg(), model=model, system_prompt="sys", write_file=write_file, max_step_retries=0
    )
    assert ctx.step == 1
    assert [e["kind"] for e in events.events] == ["plan", "step"]


async def test_pause_blocks_at_boundary_then_resumes() -> None:
    import asyncio

    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    ctx.pause_token.set_paused()
    model = scripted_model(
        ['{"steps": ["only step"]}', '{"summary": "x", "files": []}', '{"verdict": "finish"}']
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    task = asyncio.create_task(
        run_agent_loop(
            ctx, _msg(), model=model, system_prompt="sys", write_file=write_file, max_step_retries=0
        )
    )
    await asyncio.sleep(0.05)
    # Blocked at the first step boundary — but only AFTER the plan checkpoint is durable.
    assert not task.done()
    assert [w[0] for w in cp.writes] == [0]

    ctx.pause_token.resume()
    await task
    assert ctx.step == 1
    assert [w[0] for w in cp.writes] == [0, 1]


async def test_resume_continues_event_seq_and_restores_summaries() -> None:
    """Spec: Resume-Safe Event Sequencing + Run Summary Event (resume path).

    Attempt 1 checkpointed at step 2 with event_seq high-water 4. The resumed
    attempt must emit its next event ABOVE 4 (no (run_id, seq) collision) and
    surface the prior attempt's step summaries from the restored plan state.
    """
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    cp.seeded_latest = SimpleNamespace(
        step_seq=2,
        step_name="step two",
        state={
            "plan": [
                {"idx": 0, "title": "step one", "done": True, "result_summary": "did one"},
                {"idx": 1, "title": "step two", "done": True, "result_summary": "did two"},
                {"idx": 2, "title": "step three", "done": False},
            ],
            "step_count": 3,
            "event_seq": 4,
        },
    )
    model = scripted_model(['{"summary": "did three", "files": []}', '{"verdict": "finish"}'])

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    result = await run_agent_loop(
        ctx, _msg(), model=model, system_prompt="sys", write_file=write_file, max_step_retries=0
    )
    # One step event for step three only, sequenced past the high-water mark.
    assert [e["kind"] for e in events.events] == ["step"]
    assert events.events[0]["seq"] == 5
    # Full-run summaries: prior attempt's steps restored from the checkpoint.
    assert result.step_summaries == ["did one", "did two", "did three"]
    # The new step checkpoint carries the advanced high-water mark and the
    # accumulated summaries for any further redelivery.
    last_state = cp.writes[-1][2]
    assert last_state["event_seq"] == 5
    assert [e.get("result_summary") for e in last_state["plan"]] == [
        "did one",
        "did two",
        "did three",
    ]


async def test_fresh_run_checkpoints_event_seq_high_water() -> None:
    """Fresh runs persist the seq high-water from the first checkpoint on."""
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = scripted_model(
        [
            '{"steps": ["only step"]}',
            '{"summary": "done", "files": []}',
            '{"verdict": "finish"}',
        ]
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    await run_agent_loop(
        ctx, _msg(), model=model, system_prompt="sys", write_file=write_file, max_step_retries=0
    )
    # plan event seq=1 → plan checkpoint records 1; step event seq=2 reserved
    # before its checkpoint → step checkpoint records 2.
    assert [w[2]["event_seq"] for w in cp.writes] == [1, 2]
    assert [e["seq"] for e in events.events] == [1, 2]


# --- per-step artifact persistence + events (improve-artifact-conversation-ux) --


async def test_step_persists_before_checkpoint_then_emits_artifact_events() -> None:
    """Spec ordering: a step's artifact rows are upserted AND the step+artifact
    event seqs reserved BEFORE the step checkpoint; the artifact events are
    emitted AFTER it (insert-then-publish), one per produced file."""
    ops: list[str] = []

    class RecordingCp(FakeCheckpointStore):
        async def write(self, *, step_seq: int, step_name: str, state: dict[str, Any]) -> Any:
            ops.append(f"checkpoint:{step_seq}")
            return await super().write(step_seq=step_seq, step_name=step_name, state=state)

    class RecordingEvents(FakeEventPublisher):
        async def publish_event(self, **kwargs: Any) -> None:
            ops.append(f"emit:{kwargs['kind']}")
            await super().publish_event(**kwargs)

    cp, events = RecordingCp(), RecordingEvents()
    ctx = _make_ctx(cp, events)
    model = scripted_model(
        [
            '{"steps": ["only step"]}',
            '{"summary": "did one", "files": ['
            '{"path": "a.py", "content": "x"}, {"path": "b/c.css", "content": "yy"}]}',
            '{"verdict": "finish"}',
        ]
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(
            path=path, oss_key=f"k/{path}", bytes=len(content), sha256="h", mime="text/plain"
        )

    async def persist_artifact(ctx_in: Any, art: ProducedArtifact) -> str:
        ops.append(f"persist:{art.path}")
        return f"id-{art.path}"

    await run_agent_loop(
        ctx,
        _msg(),
        model=model,
        system_prompt="sys",
        write_file=write_file,
        max_step_retries=0,
        persist_artifact=persist_artifact,
    )

    # Plan emits then checkpoints; the step persists both rows, checkpoints,
    # then emits the step event followed by one artifact event per file.
    assert ops == [
        "emit:plan",
        "checkpoint:0",
        "persist:a.py",
        "persist:b/c.css",
        "checkpoint:1",
        "emit:step",
        "emit:artifact",
        "emit:artifact",
    ]
    # Artifact event payloads carry the persisted id + path + metadata.
    artifacts = [e for e in events.events if e["kind"] == "artifact"]
    assert [a["payload"]["artifact_id"] for a in artifacts] == ["id-a.py", "id-b/c.css"]
    assert [a["payload"]["path"] for a in artifacts] == ["a.py", "b/c.css"]
    assert all(a["payload"]["mime"] == "text/plain" for a in artifacts)
    # Seqs: plan=1, step=2, artifacts=3,4 — and the step checkpoint's high-water
    # mark covers every artifact seq, so a resume can never re-hand-out one.
    assert [e["seq"] for e in events.events] == [1, 2, 3, 4]
    step_checkpoint_state = cp.writes[1][2]
    assert step_checkpoint_state["event_seq"] == 4


async def test_persist_failure_aborts_before_step_checkpoint() -> None:
    """If row persistence fails the run fails and the step checkpoint is NOT
    written — so a redelivery re-executes the whole step (no orphaned object,
    no half-advanced checkpoint)."""
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = scripted_model(
        [
            '{"steps": ["only step"]}',
            '{"summary": "x", "files": [{"path": "a.py", "content": "y"}]}',
            '{"verdict": "finish"}',
        ]
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=f"k/{path}", bytes=1, sha256="h")

    async def persist_artifact(ctx_in: Any, art: ProducedArtifact) -> str:
        raise RuntimeError("db down")

    with pytest.raises(RuntimeError):
        await run_agent_loop(
            ctx,
            _msg(),
            model=model,
            system_prompt="sys",
            write_file=write_file,
            max_step_retries=0,
            persist_artifact=persist_artifact,
        )
    # Only the plan checkpoint exists; the step checkpoint never advanced, and
    # no artifact event was emitted.
    assert [w[0] for w in cp.writes] == [0]
    assert [e["kind"] for e in events.events] == ["plan"]


# --- file deletion (add-artifact-deletion) ----------------------------------


async def _noop_write(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
    return ProducedArtifact(path=path, oss_key=f"k/{path}", bytes=len(content), sha256="h")


async def test_deletion_removes_row_and_emits_artifact_deleted_after_checkpoint() -> None:
    """A declared deletion whose row is removed emits one artifact_deleted event,
    applied AFTER the (empty) persist pass and BEFORE the checkpoint; the event is
    emitted AFTER the checkpoint with a pre-reserved seq, and carries no oss_key."""
    ops: list[str] = []

    class RecordingCp(FakeCheckpointStore):
        async def write(self, *, step_seq: int, step_name: str, state: dict[str, Any]) -> Any:
            ops.append(f"checkpoint:{step_seq}")
            return await super().write(step_seq=step_seq, step_name=step_name, state=state)

    class RecordingEvents(FakeEventPublisher):
        async def publish_event(self, **kwargs: Any) -> None:
            ops.append(f"emit:{kwargs['kind']}")
            await super().publish_event(**kwargs)

    cp, events = RecordingCp(), RecordingEvents()
    ctx = _make_ctx(cp, events)
    model = scripted_model(
        [
            '{"steps": ["only step"]}',
            '{"summary": "removed styles", "files": [], "deletions": ["styles.css"]}',
            '{"verdict": "finish"}',
        ]
    )

    async def delete_file(ctx_in: Any, path: str) -> bool:
        ops.append(f"delfile:{path}")
        return True

    async def delete_artifact(ctx_in: Any, path: str) -> bool:
        ops.append(f"delrow:{path}")
        return True

    await run_agent_loop(
        ctx,
        _msg(),
        model=model,
        system_prompt="sys",
        write_file=_noop_write,
        max_step_retries=0,
        delete_file=delete_file,
        delete_artifact=delete_artifact,
    )

    assert ops == [
        "emit:plan",
        "checkpoint:0",
        "delfile:styles.css",
        "delrow:styles.css",
        "checkpoint:1",
        "emit:step",
        "emit:artifact_deleted",
    ]
    deleted = [e for e in events.events if e["kind"] == "artifact_deleted"]
    assert len(deleted) == 1
    assert deleted[0]["payload"]["path"] == "styles.css"
    assert "version_id" in deleted[0]["payload"]
    assert "oss_key" not in deleted[0]["payload"]
    # The artifact_deleted seq is covered by the step checkpoint's high-water.
    assert deleted[0]["seq"] == cp.writes[-1][2]["event_seq"]


async def test_deletion_of_absent_path_emits_nothing() -> None:
    """A deletion that removes no row (path absent from this version) is a silent
    no-op: the step still finishes and no artifact_deleted event is emitted."""
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = scripted_model(
        [
            '{"steps": ["only step"]}',
            '{"summary": "tried delete", "files": [], "deletions": ["nope.txt"]}',
            '{"verdict": "finish"}',
        ]
    )

    async def delete_file(ctx_in: Any, path: str) -> bool:
        return False

    async def delete_artifact(ctx_in: Any, path: str) -> bool:
        return False  # no row matched

    await run_agent_loop(
        ctx,
        _msg(),
        model=model,
        system_prompt="sys",
        write_file=_noop_write,
        max_step_retries=0,
        delete_file=delete_file,
        delete_artifact=delete_artifact,
    )
    assert [e["kind"] for e in events.events] == ["plan", "step"]
    assert ctx.step == 1


async def test_same_step_write_and_delete_of_one_path_nets_to_absent() -> None:
    """When a step writes AND deletes the same path, the write is dropped from
    the persisted/emitted set (delete wins) — no artifact row is inserted-then-
    deleted, and the OSS object the write created is removed via delete_file."""
    persisted: list[str] = []
    deleted_files: list[str] = []
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = scripted_model(
        [
            '{"steps": ["only step"]}',
            '{"summary": "rewrite then drop", "files": '
            '[{"path": "x.css", "content": "body{}"}], "deletions": ["x.css"]}',
            '{"verdict": "finish"}',
        ]
    )

    async def persist_artifact(ctx_in: Any, art: ProducedArtifact) -> str:
        persisted.append(art.path)
        return f"id-{art.path}"

    async def delete_file(ctx_in: Any, path: str) -> bool:
        deleted_files.append(path)
        return True

    async def delete_artifact(ctx_in: Any, path: str) -> bool:
        return False  # nothing was persisted for x.css this run

    await run_agent_loop(
        ctx,
        _msg(),
        model=model,
        system_prompt="sys",
        write_file=_noop_write,
        max_step_retries=0,
        persist_artifact=persist_artifact,
        delete_file=delete_file,
        delete_artifact=delete_artifact,
    )
    # The write was NOT persisted (delete supersedes it) and produced no artifact
    # event; the OSS object the write created was deleted; no artifact_deleted
    # (no row existed to remove).
    assert persisted == []
    assert deleted_files == ["x.css"]
    kinds = [e["kind"] for e in events.events]
    assert "artifact" not in kinds
    assert "artifact_deleted" not in kinds


async def test_malformed_file_content_raises_executor_output_error() -> None:
    """A files entry with null content raises ExecutorOutputError (carrying the
    path) instead of writing — the consumer maps it to executor_output_invalid."""
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = scripted_model(
        [
            '{"steps": ["only step"]}',
            '{"summary": "oops", "files": [{"path": "styles.css", "content": null}]}',
        ]
    )

    with pytest.raises(ExecutorOutputError) as ei:
        await run_agent_loop(
            ctx,
            _msg(),
            model=model,
            system_prompt="sys",
            write_file=_noop_write,
            max_step_retries=0,
        )
    assert ei.value.path == "styles.css"


async def test_resume_reexecutes_deletion_idempotently_no_duplicate_event() -> None:
    """Resume-safety (8.2): after a crash before the deletion step's checkpoint,
    the redelivered run re-executes the step but finds the OSS object AND row
    already gone (the delete happened pre-crash; inheritance is skipped on resume
    because a checkpoint exists). The re-applied deletes are idempotent no-ops, no
    duplicate artifact_deleted event is emitted, and the step event's seq
    continues past the restored high-water (no (run_id, seq) collision)."""
    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    # Seed a plan checkpoint (step 0) whose single deletion step is NOT yet done,
    # with event_seq high-water 1 — the state a pre-step-checkpoint crash leaves.
    cp.seeded_latest = SimpleNamespace(
        step_seq=0,
        step_name="plan",
        state={
            "plan": [{"idx": 0, "title": "remove styles", "done": False}],
            "step_count": 1,
            "event_seq": 1,
        },
    )
    # Resume consumes only the executor+critic for the step (no planner call).
    model = scripted_model(
        [
            '{"summary": "removed styles", "files": [], "deletions": ["styles.css"]}',
            '{"verdict": "finish"}',
        ]
    )

    async def delete_file(ctx_in: Any, path: str) -> bool:
        return False  # object already removed by the crashed attempt

    async def delete_artifact(ctx_in: Any, path: str) -> bool:
        return False  # row already removed by the crashed attempt

    await run_agent_loop(
        ctx,
        _msg(),
        model=model,
        system_prompt="sys",
        write_file=_noop_write,
        max_step_retries=0,
        delete_file=delete_file,
        delete_artifact=delete_artifact,
    )
    # The deletion step re-ran (converging to absent) but emitted NO
    # artifact_deleted — nothing was removed the second time — and its seq
    # continues above the restored high-water (1), never colliding.
    kinds = [e["kind"] for e in events.events]
    assert kinds == ["step"]
    assert "artifact_deleted" not in kinds
    assert events.events[0]["seq"] == 2
    assert ctx.step == 1


# --- conversation-context injection (refactor-task-conversation-continuity) --


def _history_msg() -> Any:
    from worker.core.messages import HistoryTurn

    return SimpleNamespace(
        prompt="add a settings page",
        attempt_no=1,
        deadline_ts=None,
        history=[
            HistoryTurn(
                version_no=1, prompt="build app", summary="built the shell", status="succeeded"
            ),
            HistoryTurn(version_no=2, prompt="add login", summary=None, status="failed"),
        ],
    )


class _ExcerptOss:
    def __init__(self, contents: dict[str, bytes]) -> None:
        self._contents = contents
        self.reads: list[str] = []

    async def get(self, prefix: str, key: str) -> bytes:
        self.reads.append(key)
        return self._contents[key]


async def test_context_block_injected_into_planner_and_executor() -> None:
    from tests.support.fake_model import capturing_scripted_model

    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    ctx.oss_client = _ExcerptOss({"index.html": b"<h1>app</h1>"})
    ctx.oss_prefix = "t/task/v/"
    model = capturing_scripted_model(
        [
            '{"steps": ["one step"]}',
            '{"summary": "done", "files": []}',
            '{"verdict": "finish"}',
        ]
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    await run_agent_loop(
        ctx,
        _history_msg(),
        model=model,
        system_prompt="sys",
        write_file=write_file,
        max_step_retries=0,
        inherited=[("index.html", 12)],
    )

    planner_in = model.calls[0][1].content
    executor_in = model.calls[1][1].content
    critic_in = model.calls[2][1].content
    # Planner sees history (oldest first), the failure marker, the inherited
    # file content, then the current request.
    assert "[v1] user: build app" in planner_in
    assert "[v2] result: (this attempt ended FAILED)" in planner_in
    assert "--- index.html ---" in planner_in
    assert planner_in.endswith("add a settings page")
    assert planner_in.index("[v1]") < planner_in.index("[v2]")
    # Executor gets the same block ahead of the task framing.
    assert executor_in.startswith("Conversation so far")
    assert "Overall task: add a settings page" in executor_in
    # Critic input unchanged — no context block.
    assert critic_in.startswith("Step: ")
    # The plan checkpoint persisted the block for resume.
    assert "context_block" in cp.writes[0][2]


async def test_no_history_no_inherit_inputs_byte_identical() -> None:
    from tests.support.fake_model import capturing_scripted_model

    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    model = capturing_scripted_model(
        [
            '{"steps": ["one step"]}',
            '{"summary": "done", "files": []}',
            '{"verdict": "finish"}',
        ]
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    await run_agent_loop(
        ctx, _msg(), model=model, system_prompt="sys", write_file=write_file, max_step_retries=0
    )
    # Pre-change byte-identical composition (compatibility invariant).
    assert model.calls[0][1].content == "build a thing"
    assert model.calls[1][1].content == "Overall task: build a thing\nCurrent step: one step"
    assert "context_block" not in cp.writes[0][2]


async def test_resume_restores_context_block_without_oss_reads() -> None:
    from tests.support.fake_model import capturing_scripted_model

    class _ExplodingOss:
        async def get(self, prefix: str, key: str) -> bytes:
            raise AssertionError("resume must not re-read OSS for the context block")

    cp, events = FakeCheckpointStore(), FakeEventPublisher()
    ctx = _make_ctx(cp, events)
    ctx.oss_client = _ExplodingOss()
    cp.seeded_latest = SimpleNamespace(
        step_seq=1,
        step_name="step one",
        state={
            "plan": [
                {"idx": 0, "title": "step one", "done": True, "result_summary": "did one"},
                {"idx": 1, "title": "step two", "done": False},
            ],
            "step_count": 2,
            "event_seq": 2,
            "context_block": "Conversation so far (oldest first):\n[v1] user: build app",
        },
    )
    model = capturing_scripted_model(
        ['{"summary": "did two", "files": []}', '{"verdict": "finish"}']
    )

    async def write_file(ctx_in: Any, path: str, content: str) -> ProducedArtifact:
        return ProducedArtifact(path=path, oss_key=path, bytes=0, sha256="h")

    await run_agent_loop(
        ctx,
        _history_msg(),
        model=model,
        system_prompt="sys",
        write_file=write_file,
        max_step_retries=0,
        inherited=[],  # resume: inheritance was skipped upstream
    )
    # Executor input uses the checkpointed block verbatim (no planner call on resume).
    executor_in = model.calls[0][1].content
    assert executor_in.startswith("Conversation so far (oldest first):\n[v1] user: build app")
    # The new step checkpoint carries the block forward for further redeliveries.
    assert cp.writes[-1][2]["context_block"].startswith("Conversation so far")
