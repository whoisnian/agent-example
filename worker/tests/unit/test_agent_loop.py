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
    ProducedArtifact,
    StepRetryBudgetExceeded,
    run_agent_loop,
)
from worker.core.persistence import CheckpointConflictError
from worker.core.run_context import CancelToken, PauseToken

from tests.support.fake_model import scripted_model


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


def _make_ctx(cp: FakeCheckpointStore, events: FakeEventPublisher) -> Any:
    seq = {"n": 0}

    def next_event_seq() -> int:
        seq["n"] += 1
        return seq["n"]

    return SimpleNamespace(
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
        next_event_seq=next_event_seq,
    )


def _msg(prompt: str = "build a thing", attempt_no: int = 1, deadline_ts: int | None = None) -> Any:
    return SimpleNamespace(prompt=prompt, attempt_no=attempt_no, deadline_ts=deadline_ts)


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
    assert [a.path for a in produced] == ["a.py"]
    assert ctx.step == 2


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
    assert produced == []
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
