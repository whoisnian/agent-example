"""Integration test for CheckpointStore against MinIO + Postgres."""

from __future__ import annotations

from uuid import uuid4

import pytest
from worker.core.checkpoint import CheckpointStore
from worker.core.persistence import (
    CheckpointConflictError,
    Persistence,
    RunRow,
)
from worker.core.storage import OssClient

pytestmark = pytest.mark.integration


async def _setup(pg_pool, minio_container):  # type: ignore[no-untyped-def]
    p = Persistence(pg_pool, heartbeat_interval_seconds=5.0)
    endpoint = (
        f"http://{minio_container.get_container_host_ip()}:{minio_container.get_exposed_port(9000)}"
    )
    oss = OssClient(
        endpoint_url=endpoint,
        bucket="worker-bucket",
        access_key_id=minio_container.access_key,
        access_key_secret=minio_container.secret_key,
    )
    await oss.ensure_bucket()
    run = RunRow(
        run_id=uuid4(),
        version_id=uuid4(),
        attempt_no=1,
        idempotency_key=f"key-{uuid4()}",
        worker_run_id=uuid4(),
    )
    await p.claim_or_skip_run(run)
    store = CheckpointStore(
        run_id=run.run_id,
        oss_prefix=f"tenant/{run.version_id}/",
        persistence=p,
        oss_client=oss,
        inline_byte_limit=8 * 1024,
    )
    return p, store, run


async def test_inline_path(pg_pool, minio_container):  # type: ignore[no-untyped-def]
    _, store, _ = await _setup(pg_pool, minio_container)
    record = await store.write(step_seq=1, step_name="plan", state={"x": 1})
    assert record.oss_key is None
    latest = await store.latest()
    assert latest is not None and latest.step_seq == 1


async def test_oss_offload(pg_pool, minio_container):  # type: ignore[no-untyped-def]
    _, store, _ = await _setup(pg_pool, minio_container)
    # Force OSS via large state.
    big = {"data": "x" * (20 * 1024)}
    record = await store.write(step_seq=2, step_name="gen", state=big)
    assert record.oss_key is not None


async def test_duplicate_raises(pg_pool, minio_container):  # type: ignore[no-untyped-def]
    _, store, _ = await _setup(pg_pool, minio_container)
    await store.write(step_seq=1, step_name="plan", state={"x": 1})
    with pytest.raises(CheckpointConflictError):
        await store.write(step_seq=1, step_name="plan", state={"x": 2})
