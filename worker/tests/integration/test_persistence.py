"""Integration tests for ``worker.core.persistence`` against real Postgres."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from worker.core.persistence import (
    CheckpointConflictError,
    ClaimOutcome,
    Persistence,
    RunRow,
)

pytestmark = pytest.mark.integration


async def _make_persistence(pg_pool):  # type: ignore[no-untyped-def]
    return Persistence(pg_pool, heartbeat_interval_seconds=5.0)


async def test_claim_fresh(pg_pool):  # type: ignore[no-untyped-def]
    p = await _make_persistence(pg_pool)
    run = RunRow(
        run_id=uuid4(),
        version_id=uuid4(),
        attempt_no=1,
        idempotency_key=f"key-{uuid4()}",
        worker_run_id=uuid4(),
    )
    result = await p.claim_or_skip_run(run)
    assert result.outcome == ClaimOutcome.FRESH


async def test_claim_already_succeeded(pg_pool):  # type: ignore[no-untyped-def]
    p = await _make_persistence(pg_pool)
    run = RunRow(
        run_id=uuid4(),
        version_id=uuid4(),
        attempt_no=1,
        idempotency_key=f"key-{uuid4()}",
        worker_run_id=uuid4(),
    )
    await p.claim_or_skip_run(run)
    await p.mark_run_terminal(run.run_id, status="succeeded")
    result = await p.claim_or_skip_run(run)
    assert result.outcome == ClaimOutcome.ALREADY_SUCCEEDED


async def test_claim_running_by_other_recent(pg_pool):  # type: ignore[no-untyped-def]
    p = await _make_persistence(pg_pool)
    run = RunRow(
        run_id=uuid4(),
        version_id=uuid4(),
        attempt_no=1,
        idempotency_key=f"key-{uuid4()}",
        worker_run_id=uuid4(),
    )
    await p.claim_or_skip_run(run)
    # Second worker tries to claim with the same key.
    intruder = RunRow(**{**run.__dict__, "worker_run_id": uuid4()})
    result = await p.claim_or_skip_run(intruder)
    assert result.outcome == ClaimOutcome.RUNNING_BY_OTHER_RECENT


async def test_claim_stale_takeover(pg_pool):  # type: ignore[no-untyped-def]
    p = await _make_persistence(pg_pool)
    run = RunRow(
        run_id=uuid4(),
        version_id=uuid4(),
        attempt_no=1,
        idempotency_key=f"key-{uuid4()}",
        worker_run_id=uuid4(),
    )
    await p.claim_or_skip_run(run)
    # Forge a stale heartbeat directly to avoid sleeping.
    stale = datetime.now(UTC) - timedelta(seconds=60)
    async with pg_pool.acquire() as conn:
        await conn.execute(
            "UPDATE task_runs SET last_heartbeat = $1 WHERE id = $2",
            stale,
            run.run_id,
        )
    intruder = RunRow(**{**run.__dict__, "worker_run_id": uuid4()})
    result = await p.claim_or_skip_run(intruder)
    assert result.outcome == ClaimOutcome.RUNNING_STALE_TAKEOVER
    assert result.worker_run_id == intruder.worker_run_id


async def test_heartbeat_cas_protection(pg_pool):  # type: ignore[no-untyped-def]
    p = await _make_persistence(pg_pool)
    run = RunRow(
        run_id=uuid4(),
        version_id=uuid4(),
        attempt_no=1,
        idempotency_key=f"key-{uuid4()}",
        worker_run_id=uuid4(),
    )
    await p.claim_or_skip_run(run)
    assert await p.update_heartbeat(run.run_id, run.worker_run_id) is True
    other = uuid4()
    assert await p.update_heartbeat(run.run_id, other) is False


async def test_checkpoint_inline_and_duplicate(pg_pool):  # type: ignore[no-untyped-def]
    p = await _make_persistence(pg_pool)
    run = RunRow(
        run_id=uuid4(),
        version_id=uuid4(),
        attempt_no=1,
        idempotency_key=f"key-{uuid4()}",
        worker_run_id=uuid4(),
    )
    await p.claim_or_skip_run(run)
    await p.insert_checkpoint(
        run_id=run.run_id, step_seq=1, step_name="plan", state={"x": 1}, oss_key=None
    )
    latest = await p.select_latest_checkpoint(run.run_id)
    assert latest is not None
    assert latest.step_seq == 1
    assert latest.state == {"x": 1}
    with pytest.raises(CheckpointConflictError):
        await p.insert_checkpoint(
            run_id=run.run_id, step_seq=1, step_name="plan", state={"x": 2}, oss_key=None
        )


async def test_insert_artifact(pg_pool):  # type: ignore[no-untyped-def]
    p = await _make_persistence(pg_pool)
    aid = await p.insert_artifact(
        version_id=uuid4(),
        kind="report",
        oss_key="x/y/report.md",
        mime="text/markdown",
        bytes_size=42,
        sha256="abc",
    )
    assert aid is not None
