"""Integration-only fixtures (testcontainers-backed).

This module is imported only when ``pytest -m integration`` is run; the unit
lane skips collection of this directory's tests via the ``integration``
marker.

When ``testcontainers`` or Docker is unavailable, fixtures that depend on
them are skipped at fixture-resolution time rather than failing collection.
"""

from __future__ import annotations

import os
from collections.abc import AsyncIterator, Iterator
from typing import Any

import pytest
import pytest_asyncio


def _maybe_skip_no_docker() -> None:
    """Skip if Docker is plainly unreachable.

    Allows the test file to be collected (the @pytest.mark.integration marker
    is what gates the default lane).
    """
    if os.environ.get("SKIP_INTEGRATION") == "1":
        pytest.skip("SKIP_INTEGRATION=1 set", allow_module_level=False)


# --- PostgreSQL ------------------------------------------------------------


@pytest.fixture(scope="session")
def pg_container() -> Iterator[Any]:
    """A live PostgreSQL container with the Worker-owned tables created."""
    _maybe_skip_no_docker()
    pytest.importorskip("testcontainers.postgres")
    from testcontainers.postgres import PostgresContainer

    with PostgresContainer("postgres:16") as pg:
        yield pg


@pytest_asyncio.fixture
async def pg_pool(pg_container: Any) -> AsyncIterator[Any]:
    import asyncpg

    dsn = pg_container.get_connection_url().replace("postgresql+psycopg2://", "postgresql://")
    pool = await asyncpg.create_pool(dsn=dsn, min_size=1, max_size=2)
    assert pool is not None
    # Bootstrap the Worker-owned schema slice (subset of docs/ARCHITECTURE §4.2).
    async with pool.acquire() as conn:
        await conn.execute(
            """
            CREATE TABLE IF NOT EXISTS task_runs (
                id              UUID PRIMARY KEY,
                version_id      UUID NOT NULL,
                attempt_no      INT NOT NULL,
                worker_run_id   UUID,
                status          TEXT NOT NULL,
                started_at      TIMESTAMPTZ,
                ended_at        TIMESTAMPTZ,
                last_heartbeat  TIMESTAMPTZ,
                error           JSONB,
                idempotency_key TEXT NOT NULL UNIQUE
            );
            CREATE TABLE IF NOT EXISTS task_checkpoints (
                id          UUID PRIMARY KEY,
                run_id      UUID NOT NULL REFERENCES task_runs(id),
                step_seq    INT NOT NULL,
                step_name   TEXT NOT NULL,
                state       JSONB NOT NULL,
                oss_key     TEXT,
                created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
                UNIQUE (run_id, step_seq)
            );
            CREATE TABLE IF NOT EXISTS artifacts (
                id          UUID PRIMARY KEY,
                version_id  UUID NOT NULL,
                kind        TEXT NOT NULL,
                oss_key     TEXT NOT NULL,
                mime        TEXT,
                bytes       BIGINT,
                sha256      TEXT,
                created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
            );
            """
        )
    try:
        yield pool
    finally:
        async with pool.acquire() as conn:
            await conn.execute(
                "TRUNCATE artifacts, task_checkpoints, task_runs RESTART IDENTITY CASCADE"
            )
        await pool.close()


# --- RabbitMQ --------------------------------------------------------------


@pytest.fixture(scope="session")
def rmq_container() -> Iterator[Any]:
    _maybe_skip_no_docker()
    pytest.importorskip("testcontainers.rabbitmq")
    from testcontainers.rabbitmq import RabbitMqContainer

    with RabbitMqContainer("rabbitmq:3.13") as rmq:
        yield rmq


# --- MinIO -----------------------------------------------------------------


@pytest.fixture(scope="session")
def minio_container() -> Iterator[Any]:
    _maybe_skip_no_docker()
    pytest.importorskip("testcontainers.minio")
    from testcontainers.minio import MinioContainer

    with MinioContainer() as minio:
        yield minio
