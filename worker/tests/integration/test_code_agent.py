"""Integration test for the code-gen agent against a real Postgres.

Exercises ``LoopAgent.run`` end-to-end with a scripted fake model: plan/step
events are emitted, files are written through the (here in-memory) OSS client,
``artifacts`` rows are persisted, and checkpoints land in the real DB. A second
case proves an OSS upload failure fails the run (no artifact rows) rather than
reporting success.

RabbitMQ is intentionally out of scope here (the worker MQ testcontainer
fixtures are unrunnable under the installed testcontainers version); the
dispatcher→agent→succeeded wiring is covered by ``test_consumer_end_to_end``.
"""

from __future__ import annotations

from typing import Any
from uuid import uuid4

import pytest
import structlog
from worker.agents.code_agent import build_code_agent
from worker.core.checkpoint import CheckpointStore
from worker.core.messages import TaskExecuteMessage
from worker.core.persistence import Persistence
from worker.core.run_context import CancelToken, PauseToken, RunContext, compute_oss_prefix

from tests.support.fake_model import FakeModelFactory, scripted_model

pytestmark = pytest.mark.integration


@pytest.fixture
def settings_for_agent(required_env: dict[str, str]) -> Any:
    from worker.core.config import load

    return load(env=required_env)


class _InMemoryOss:
    """Minimal Oss stand-in honoring put/get under a prefix."""

    def __init__(self, *, fail_on_put: bool = False) -> None:
        self.objects: dict[str, bytes] = {}
        self._fail = fail_on_put

    async def put(self, prefix: str, key: str, body: bytes) -> str:
        if self._fail:
            raise OSError("simulated OSS outage")
        absolute = prefix + key
        self.objects[absolute] = body
        return absolute

    async def get(self, prefix: str, key: str) -> bytes:
        return self.objects[prefix + key]


class _CapturingEvents:
    def __init__(self) -> None:
        self.events: list[dict[str, Any]] = []

    async def publish_event(self, **kwargs: Any) -> None:
        self.events.append(kwargs)


class _NoopCost:
    async def publish_cost(self, **kwargs: Any) -> None:
        return None


def _script() -> Any:
    return scripted_model(
        [
            '{"steps": ["write module", "finish up"]}',
            '{"summary": "wrote main", "files": [{"path": "main.py", "content": "print(1)\\n"}]}',
            '{"verdict": "advance"}',
            '{"summary": "done", "files": []}',
            '{"verdict": "finish"}',
        ]
    )


async def _make_ctx(persistence: Persistence, oss: _InMemoryOss, events: _CapturingEvents) -> Any:
    from worker.core.cost_meter import CostMeter

    task_id, version_id, run_id = uuid4(), uuid4(), uuid4()
    tenant_id = "demo"
    ctx = RunContext(
        task_id=task_id,
        version_id=version_id,
        run_id=run_id,
        attempt_no=1,
        task_type="code-gen",
        worker_id="wk-codegen",
        tenant_id=tenant_id,
        oss_prefix=compute_oss_prefix(tenant_id, task_id, version_id),
        cancel_token=CancelToken(),
        pause_token=PauseToken(),
        cost_meter=None,  # type: ignore[arg-type]
        event_publisher=events,  # type: ignore[arg-type]
        cost_publisher=_NoopCost(),  # type: ignore[arg-type]
        checkpoint_store=None,  # type: ignore[arg-type]
        oss_client=oss,  # type: ignore[arg-type]
        logger=structlog.get_logger(),
        trace_id="0" * 32,
    )
    ctx.cost_meter = CostMeter(ctx)
    ctx.checkpoint_store = CheckpointStore(
        run_id=run_id,
        oss_prefix=ctx.oss_prefix,
        persistence=persistence,
        oss_client=oss,  # type: ignore[arg-type]
        inline_byte_limit=8 * 1024,
    )
    return ctx


def _msg(ctx: Any) -> TaskExecuteMessage:
    return TaskExecuteMessage(
        msg_id=uuid4(),
        idempotency_key=str(ctx.run_id),
        task_id=ctx.task_id,
        version_id=ctx.version_id,
        run_id=ctx.run_id,
        attempt_no=1,
        task_type="code-gen",
        prompt="build a tiny script",
    )


async def _seed_run(pool: Any, ctx: Any) -> None:
    async with pool.acquire() as conn:
        await conn.execute(
            """
            INSERT INTO task_runs (id, version_id, attempt_no, status, idempotency_key)
            VALUES ($1, $2, 1, 'running', $3)
            """,
            ctx.run_id,
            ctx.version_id,
            str(ctx.run_id),
        )


async def test_code_agent_success_writes_artifact_and_checkpoints(
    pg_pool,  # type: ignore[no-untyped-def]
    settings_for_agent,  # type: ignore[no-untyped-def]
) -> None:
    persistence = Persistence(pg_pool, heartbeat_interval_seconds=5.0)
    oss = _InMemoryOss()
    events = _CapturingEvents()
    ctx = await _make_ctx(persistence, oss, events)
    await _seed_run(pg_pool, ctx)

    agent = build_code_agent(FakeModelFactory(model=_script()), persistence, settings_for_agent)
    await agent.run(ctx, _msg(ctx))

    # Plan + two step events, then the run-summary event after artifact rows.
    assert [e["kind"] for e in events.events] == ["plan", "step", "step", "summary"]
    assert events.events[-1]["payload"]["summary"] == "1. wrote main\n2. done"
    # Seqs are strictly increasing (step seqs are pre-reserved before their
    # checkpoints; the summary event continues the sequence).
    seqs = [e["seq"] for e in events.events]
    assert seqs == sorted(seqs) and len(set(seqs)) == len(seqs)
    # File written under the run's OSS prefix.
    assert f"{ctx.oss_prefix}main.py" in oss.objects
    # Artifact row persisted.
    async with pg_pool.acquire() as conn:
        rows = await conn.fetch(
            "SELECT oss_key, kind, sha256 FROM artifacts WHERE version_id = $1", ctx.version_id
        )
        cps = await conn.fetch(
            "SELECT step_seq FROM task_checkpoints WHERE run_id = $1 ORDER BY step_seq", ctx.run_id
        )
    assert len(rows) == 1
    assert rows[0]["oss_key"] == f"{ctx.oss_prefix}main.py"
    assert [c["step_seq"] for c in cps] == [0, 1, 2]


async def test_code_agent_upload_failure_fails_run_no_artifacts(
    pg_pool,  # type: ignore[no-untyped-def]
    settings_for_agent,  # type: ignore[no-untyped-def]
) -> None:
    persistence = Persistence(pg_pool, heartbeat_interval_seconds=5.0)
    oss = _InMemoryOss(fail_on_put=True)
    events = _CapturingEvents()
    ctx = await _make_ctx(persistence, oss, events)
    await _seed_run(pg_pool, ctx)

    agent = build_code_agent(FakeModelFactory(model=_script()), persistence, settings_for_agent)
    with pytest.raises(OSError, match="simulated OSS outage"):
        await agent.run(ctx, _msg(ctx))

    async with pg_pool.acquire() as conn:
        rows = await conn.fetch("SELECT 1 FROM artifacts WHERE version_id = $1", ctx.version_id)
    assert rows == []


async def test_research_agent_writes_report(
    pg_pool,  # type: ignore[no-untyped-def]
    settings_for_agent,  # type: ignore[no-untyped-def]
) -> None:
    from worker.agents.research_agent import build_research_agent

    persistence = Persistence(pg_pool, heartbeat_interval_seconds=5.0)
    oss = _InMemoryOss()
    events = _CapturingEvents()
    ctx = await _make_ctx(persistence, oss, events)
    ctx.task_type = "research"
    await _seed_run(pg_pool, ctx)

    model = scripted_model(
        [
            '{"steps": ["gather", "write report"]}',
            '{"summary": "gathered sources", "files": []}',
            '{"verdict": "advance"}',
            '{"summary": "report written", "files": [{"path": "report.md", "content": "# Findings\\n"}]}',
            '{"verdict": "finish"}',
        ]
    )
    agent = build_research_agent(FakeModelFactory(model=model), persistence, settings_for_agent)
    await agent.run(ctx, _msg(ctx))

    assert f"{ctx.oss_prefix}report.md" in oss.objects
    async with pg_pool.acquire() as conn:
        rows = await conn.fetch(
            "SELECT oss_key FROM artifacts WHERE version_id = $1", ctx.version_id
        )
    assert [r["oss_key"] for r in rows] == [f"{ctx.oss_prefix}report.md"]


async def test_resume_from_db_checkpoint_skips_planner(
    pg_pool,  # type: ignore[no-untyped-def]
    settings_for_agent,  # type: ignore[no-untyped-def]
) -> None:
    persistence = Persistence(pg_pool, heartbeat_interval_seconds=5.0)
    oss = _InMemoryOss()
    events = _CapturingEvents()
    ctx = await _make_ctx(persistence, oss, events)
    await _seed_run(pg_pool, ctx)
    # Seed a checkpoint at step_seq=2 of a 4-step plan.
    plan = [f"step {i}" for i in range(4)]
    await persistence.insert_checkpoint(
        run_id=ctx.run_id,
        step_seq=2,
        step_name="step 1",
        state={
            "plan": [
                {"idx": i, "title": t, "done": i < 2}
                | ({"result_summary": f"s{i + 1}"} if i < 2 else {})
                for i, t in enumerate(plan)
            ],
            "step_count": 4,
            "event_seq": 3,
        },
        oss_key=None,
    )
    # Only steps 3 and 4 (idx 2,3) should run — no planner call.
    model = scripted_model(
        [
            '{"summary": "s3", "files": []}',
            '{"verdict": "advance"}',
            '{"summary": "s4", "files": []}',
            '{"verdict": "finish"}',
        ]
    )
    agent = build_code_agent(FakeModelFactory(model=model), persistence, settings_for_agent)
    await agent.run(ctx, _msg(ctx))

    # No plan event on resume; two step events plus the run summary.
    assert [e["kind"] for e in events.events] == ["step", "step", "summary"]
    # Seq continues past the checkpointed high-water mark (3) — no collision
    # with attempt 1's persisted (run_id, seq) rows.
    assert [e["seq"] for e in events.events] == [4, 5, 6]
    # The summary spans BOTH attempts' steps (spec: "Resumed run summarizes
    # all steps, not just its own").
    assert events.events[-1]["payload"]["summary"] == "1. s1\n2. s2\n3. s3\n4. s4"
    assert ctx.step == 4
    async with pg_pool.acquire() as conn:
        cps = await conn.fetch(
            "SELECT step_seq FROM task_checkpoints WHERE run_id = $1 ORDER BY step_seq", ctx.run_id
        )
    assert [c["step_seq"] for c in cps] == [2, 3, 4]


async def test_metrics_recorded_on_success(
    pg_pool,  # type: ignore[no-untyped-def]
    settings_for_agent,  # type: ignore[no-untyped-def]
) -> None:
    from prometheus_client import CollectorRegistry
    from worker.core.metrics import build_metrics

    reg = CollectorRegistry()
    metrics = build_metrics(registry=reg)
    persistence = Persistence(pg_pool, heartbeat_interval_seconds=5.0)
    oss = _InMemoryOss()
    events = _CapturingEvents()
    ctx = await _make_ctx(persistence, oss, events)
    await _seed_run(pg_pool, ctx)

    model = scripted_model(
        [
            '{"steps": ["a", "b", "c"]}',
            '{"summary": "a", "files": []}',
            '{"verdict": "advance"}',
            '{"summary": "b", "files": []}',
            '{"verdict": "advance"}',
            '{"summary": "c", "files": []}',
            '{"verdict": "finish"}',
        ]
    )
    agent = build_code_agent(
        FakeModelFactory(model=model), persistence, settings_for_agent, metrics
    )
    await agent.run(ctx, _msg(ctx))

    assert (
        reg.get_sample_value(
            "worker_agent_runs_total", {"task_type": "code-gen", "outcome": "success"}
        )
        == 1.0
    )
    assert reg.get_sample_value("worker_agent_steps_total", {"task_type": "code-gen"}) == 3.0


async def test_metrics_records_cancelled(
    pg_pool,  # type: ignore[no-untyped-def]
    settings_for_agent,  # type: ignore[no-untyped-def]
) -> None:
    import asyncio

    from prometheus_client import CollectorRegistry
    from worker.core.metrics import build_metrics

    reg = CollectorRegistry()
    metrics = build_metrics(registry=reg)
    persistence = Persistence(pg_pool, heartbeat_interval_seconds=5.0)
    oss = _InMemoryOss()
    events = _CapturingEvents()
    ctx = await _make_ctx(persistence, oss, events)
    await _seed_run(pg_pool, ctx)
    ctx.cancel_token.set()  # cancel before the first step boundary

    model = scripted_model(['{"steps": ["only step"]}'])
    agent = build_code_agent(
        FakeModelFactory(model=model), persistence, settings_for_agent, metrics
    )
    with pytest.raises(asyncio.CancelledError):
        await agent.run(ctx, _msg(ctx))

    assert (
        reg.get_sample_value(
            "worker_agent_runs_total", {"task_type": "code-gen", "outcome": "cancelled"}
        )
        == 1.0
    )
