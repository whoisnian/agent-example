"""asyncpg-backed persistence for Worker-owned writes.

This module is the **only** code path in the Worker that issues writes to
PostgreSQL. The set of allowed write targets is hard-coded and enumerated by
:data:`ALLOWED_WRITE_TABLES`:

- ``task_runs`` — ONLY for ``last_heartbeat``, ``worker_run_id``, ``status``
  (status flips done as part of claim / heartbeat boundary), ``started_at``,
  ``ended_at``, ``error`` (matched against the run's own ``id``).
- ``task_checkpoints`` — INSERTs only.
- ``artifacts`` — INSERTs only.

No other tables may be written from the Worker. Per AGENTS.md §4.2 the
business state tables (``tasks``, ``task_versions``) are owned by the API
service; cost-related tables (``cost_events``, ``task_costs``) are owned by
the Cost Service which consumes ``cost.events`` from the MQ. Adding a new
write target here requires a follow-up OpenSpec proposal (see §4.7 of
``init-worker-scaffold/tasks.md``).
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from enum import StrEnum
from typing import Any, Final
from uuid import UUID, uuid4

import asyncpg

#: Tables the Worker is permitted to write to. Code-review gate: any new entry
#: needs a paired OpenSpec change updating ``worker-execution-runtime``.
ALLOWED_WRITE_TABLES: Final[frozenset[str]] = frozenset(
    {
        "task_runs",  # only last_heartbeat / worker_run_id / status timestamps / error
        "task_checkpoints",  # INSERT only
        "artifacts",  # INSERT only
    }
)


class ClaimOutcome(StrEnum):
    """Result of attempting to claim a task run via idempotency key."""

    FRESH = "fresh"
    ALREADY_SUCCEEDED = "already_succeeded"
    ALREADY_FAILED = "already_failed"
    ALREADY_CANCELLED = "already_cancelled"
    RUNNING_BY_OTHER_RECENT = "running_by_other_recent"
    RUNNING_STALE_TAKEOVER = "running_stale_takeover"


@dataclass(slots=True, frozen=True)
class ClaimResult:
    outcome: ClaimOutcome
    run_id: UUID
    version_id: UUID
    attempt_no: int
    worker_run_id: UUID | None


@dataclass(slots=True, frozen=True)
class Checkpoint:
    id: UUID
    run_id: UUID
    step_seq: int
    step_name: str
    state: dict[str, Any]
    oss_key: str | None
    created_at: datetime


@dataclass(slots=True, frozen=True)
class RunRow:
    """Minimum shape passed by the consumer when claiming a fresh run."""

    run_id: UUID
    version_id: UUID
    attempt_no: int
    idempotency_key: str
    worker_run_id: UUID


class CheckpointConflictError(RuntimeError):
    """Raised when an INSERT into ``task_checkpoints`` violates ``(run_id, step_seq)`` uniqueness."""


class Persistence:
    """Worker-owned database access; wraps an ``asyncpg`` pool."""

    def __init__(
        self,
        pool: asyncpg.Pool,
        *,
        heartbeat_interval_seconds: float,
    ) -> None:
        self._pool = pool
        self._heartbeat_interval = heartbeat_interval_seconds

    @property
    def pool(self) -> asyncpg.Pool:
        return self._pool

    @classmethod
    async def connect(
        cls,
        dsn: str,
        *,
        heartbeat_interval_seconds: float,
        min_size: int = 1,
        max_size: int = 4,
    ) -> Persistence:
        pool = await asyncpg.create_pool(
            dsn=dsn,
            min_size=min_size,
            max_size=max_size,
        )
        if pool is None:  # pragma: no cover - asyncpg only returns None when init_command fails
            raise RuntimeError("asyncpg.create_pool returned None")
        await cls._probe(pool)
        return cls(pool, heartbeat_interval_seconds=heartbeat_interval_seconds)

    @staticmethod
    async def _probe(pool: asyncpg.Pool) -> None:
        async with pool.acquire() as conn:
            result = await conn.fetchval("SELECT 1")
            if result != 1:  # pragma: no cover - defensive
                raise RuntimeError(f"DB probe returned {result!r}")

    async def close(self) -> None:
        await self._pool.close()

    # ------------------------------------------------------------------
    # task_runs: claim / heartbeat / terminal status
    # ------------------------------------------------------------------

    async def claim_or_skip_run(self, run: RunRow) -> ClaimResult:
        """Atomically claim a fresh run or describe an existing run's state.

        Implements the four-branch logic from ``worker-messaging`` →
        "Idempotent Consumption":

        1. No row → INSERT (status=running) → ``FRESH``.
        2. Row exists with terminal status → ``ALREADY_*``.
        3. Row exists with status=running, recent heartbeat, foreign worker
           → ``RUNNING_BY_OTHER_RECENT``.
        4. Row exists with status=running, stale heartbeat (> 2 *
           heartbeat_interval) → CAS-update ``worker_run_id`` to this worker
           → ``RUNNING_STALE_TAKEOVER``.
        """
        stale_threshold_seconds = self._heartbeat_interval * 2
        async with self._pool.acquire() as conn, conn.transaction():
            # Attempt INSERT first; if it succeeds we own the run.
            inserted = await conn.fetchrow(
                """
                INSERT INTO task_runs (
                    id, version_id, attempt_no, worker_run_id,
                    status, started_at, last_heartbeat, idempotency_key
                ) VALUES (
                    $1, $2, $3, $4,
                    'running', now(), now(), $5
                )
                ON CONFLICT (idempotency_key) DO NOTHING
                RETURNING id, version_id, attempt_no, worker_run_id
                """,
                run.run_id,
                run.version_id,
                run.attempt_no,
                run.worker_run_id,
                run.idempotency_key,
            )
            if inserted is not None:
                return ClaimResult(
                    outcome=ClaimOutcome.FRESH,
                    run_id=inserted["id"],
                    version_id=inserted["version_id"],
                    attempt_no=inserted["attempt_no"],
                    worker_run_id=inserted["worker_run_id"],
                )

            # Existing row: inspect status and heartbeat.
            existing = await conn.fetchrow(
                """
                SELECT id, version_id, attempt_no, worker_run_id, status,
                       last_heartbeat,
                       (last_heartbeat IS NULL
                        OR last_heartbeat < now() - make_interval(secs => $2)) AS heartbeat_stale
                FROM task_runs
                WHERE idempotency_key = $1
                """,
                run.idempotency_key,
                stale_threshold_seconds,
            )
            if existing is None:  # pragma: no cover - race lost both INSERT and SELECT
                raise RuntimeError(
                    f"task_runs disappeared after ON CONFLICT for key={run.idempotency_key}"
                )

            status: str = existing["status"]
            if status == "succeeded":
                return _terminal(existing, ClaimOutcome.ALREADY_SUCCEEDED)
            if status == "failed":
                return _terminal(existing, ClaimOutcome.ALREADY_FAILED)
            if status == "cancelled":
                return _terminal(existing, ClaimOutcome.ALREADY_CANCELLED)

            # status is running / paused / queued — treat anything non-terminal as live
            if not existing["heartbeat_stale"]:
                return _terminal(existing, ClaimOutcome.RUNNING_BY_OTHER_RECENT)

            # Stale heartbeat — CAS takeover.
            taken = await conn.fetchrow(
                """
                UPDATE task_runs
                SET worker_run_id = $2, last_heartbeat = now()
                WHERE id = $1
                  AND (last_heartbeat IS NULL
                       OR last_heartbeat < now() - make_interval(secs => $3))
                RETURNING id, version_id, attempt_no, worker_run_id
                """,
                existing["id"],
                run.worker_run_id,
                stale_threshold_seconds,
            )
            if taken is None:
                # Someone heartbeat'd between SELECT and UPDATE — treat as recent.
                return _terminal(existing, ClaimOutcome.RUNNING_BY_OTHER_RECENT)
            return ClaimResult(
                outcome=ClaimOutcome.RUNNING_STALE_TAKEOVER,
                run_id=taken["id"],
                version_id=taken["version_id"],
                attempt_no=taken["attempt_no"],
                worker_run_id=taken["worker_run_id"],
            )

    async def update_heartbeat(self, run_id: UUID, worker_run_id: UUID) -> bool:
        """Bump ``last_heartbeat`` for the run, guarded by ``worker_run_id``.

        Returns ``True`` when the UPDATE actually touched the row; ``False``
        when the row was taken over by another worker (CAS lost).
        """
        async with self._pool.acquire() as conn:
            updated = await conn.execute(
                """
                UPDATE task_runs
                SET last_heartbeat = now()
                WHERE id = $1 AND worker_run_id = $2
                """,
                run_id,
                worker_run_id,
            )
        # ``conn.execute`` returns "UPDATE n" — split off the count.
        result: str = str(updated)
        return result.split()[-1] != "0"

    async def mark_run_terminal(
        self,
        run_id: UUID,
        *,
        status: str,
        error: dict[str, Any] | None = None,
    ) -> None:
        """Mark the run as ``succeeded`` / ``failed`` / ``cancelled``.

        Only valid terminal statuses are accepted; we deliberately do NOT
        expose a generic ``UPDATE task_runs`` API to keep the write surface
        narrow.
        """
        if status not in {"succeeded", "failed", "cancelled"}:
            raise ValueError(f"invalid terminal status: {status}")
        async with self._pool.acquire() as conn:
            await conn.execute(
                """
                UPDATE task_runs
                SET status = $2, ended_at = now(), error = $3
                WHERE id = $1
                """,
                run_id,
                status,
                json.dumps(error) if error is not None else None,
            )

    # ------------------------------------------------------------------
    # task_checkpoints
    # ------------------------------------------------------------------

    async def insert_checkpoint(
        self,
        *,
        run_id: UUID,
        step_seq: int,
        step_name: str,
        state: dict[str, Any],
        oss_key: str | None,
    ) -> UUID:
        """INSERT a checkpoint row. Raises on duplicate ``(run_id, step_seq)``."""
        cp_id = uuid4()
        try:
            async with self._pool.acquire() as conn:
                await conn.execute(
                    """
                    INSERT INTO task_checkpoints (id, run_id, step_seq, step_name, state, oss_key)
                    VALUES ($1, $2, $3, $4, $5::jsonb, $6)
                    """,
                    cp_id,
                    run_id,
                    step_seq,
                    step_name,
                    json.dumps(state),
                    oss_key,
                )
        except asyncpg.UniqueViolationError as exc:
            raise CheckpointConflictError(
                f"checkpoint already exists for run_id={run_id} step_seq={step_seq}"
            ) from exc
        return cp_id

    async def select_latest_checkpoint(self, run_id: UUID) -> Checkpoint | None:
        async with self._pool.acquire() as conn:
            row = await conn.fetchrow(
                """
                SELECT id, run_id, step_seq, step_name, state, oss_key, created_at
                FROM task_checkpoints
                WHERE run_id = $1
                ORDER BY step_seq DESC
                LIMIT 1
                """,
                run_id,
            )
        if row is None:
            return None
        raw_state = row["state"]
        state: dict[str, Any] = json.loads(raw_state) if isinstance(raw_state, str) else raw_state
        return Checkpoint(
            id=row["id"],
            run_id=row["run_id"],
            step_seq=row["step_seq"],
            step_name=row["step_name"],
            state=state,
            oss_key=row["oss_key"],
            created_at=row["created_at"],
        )

    # ------------------------------------------------------------------
    # artifacts
    # ------------------------------------------------------------------

    async def insert_artifact(
        self,
        *,
        version_id: UUID,
        kind: str,
        oss_key: str,
        mime: str | None,
        bytes_size: int | None,
        sha256: str | None,
    ) -> UUID:
        # Upsert on (version_id, oss_key): re-recording the same object (a
        # redelivered run re-inheriting a parent artifact) or overwriting a
        # produced file collapses to one row. RETURNING yields the existing
        # row's id on conflict, so callers always get the authoritative id.
        artifact_id = uuid4()
        async with self._pool.acquire() as conn:
            row_id: UUID = await conn.fetchval(
                """
                INSERT INTO artifacts (id, version_id, kind, oss_key, mime, bytes, sha256)
                VALUES ($1, $2, $3, $4, $5, $6, $7)
                ON CONFLICT (version_id, oss_key) DO UPDATE
                SET kind = EXCLUDED.kind,
                    mime = EXCLUDED.mime,
                    bytes = EXCLUDED.bytes,
                    sha256 = EXCLUDED.sha256
                RETURNING id
                """,
                artifact_id,
                version_id,
                kind,
                oss_key,
                mime,
                bytes_size,
                sha256,
            )
        return row_id


def _terminal(row: asyncpg.Record, outcome: ClaimOutcome) -> ClaimResult:
    return ClaimResult(
        outcome=outcome,
        run_id=row["id"],
        version_id=row["version_id"],
        attempt_no=row["attempt_no"],
        worker_run_id=row["worker_run_id"],
    )
