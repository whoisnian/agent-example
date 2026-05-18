"""Per-run execution context.

A single ``RunContext`` is constructed for each consumed ``task.execute``
message. It is the only object passed to agent / tool code (spec:
worker-execution-runtime → "Run Context"); direct access to MQ / DB / OSS
clients from inside agents is forbidden.
"""

from __future__ import annotations

import asyncio
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any
from uuid import UUID

import structlog

if TYPE_CHECKING:
    from worker.core.checkpoint import CheckpointStore
    from worker.core.cost_meter import CostMeter
    from worker.core.publisher import CostEventPublisher, EventPublisher
    from worker.core.storage import OssClient


class CancelToken:
    """An ``asyncio.Event`` wrapper used to ask the agent to stop.

    Set by the control listener (or by the heartbeat watchdog after sustained
    DB failure). Idempotent: setting twice is a no-op.
    """

    def __init__(self) -> None:
        self._event = asyncio.Event()

    def set(self) -> None:
        self._event.set()

    def is_set(self) -> bool:
        return self._event.is_set()

    async def wait(self) -> None:
        await self._event.wait()


class PauseToken:
    """Pause / resume token for cooperative checkpointing.

    ``set_paused`` causes ``wait_if_paused`` to block; ``resume`` releases all
    waiters. Like :class:`CancelToken`, idempotent.
    """

    def __init__(self) -> None:
        self._paused = asyncio.Event()
        self._resumed = asyncio.Event()
        self._resumed.set()

    def set_paused(self) -> None:
        self._paused.set()
        self._resumed.clear()

    def resume(self) -> None:
        self._paused.clear()
        self._resumed.set()

    def is_paused(self) -> bool:
        return self._paused.is_set()

    async def wait_if_paused(self) -> None:
        if self._paused.is_set():
            await self._resumed.wait()


def compute_oss_prefix(tenant_id: str, task_id: UUID, version_id: UUID) -> str:
    """Build ``{tenant_id}/{task_id}/{version_id}/`` (must end with ``/``)."""
    return f"{tenant_id}/{task_id}/{version_id}/"


@dataclass
class RunContext:
    """Bundle of per-run handles passed to agents and tools.

    All infrastructure handles required by agents MUST be acquired through
    this object — never imported directly. The dataclass is mutable only
    for the ``step`` counter and the cost-event seq registry; everything else
    is set at construction time.
    """

    task_id: UUID
    version_id: UUID
    run_id: UUID
    attempt_no: int
    task_type: str
    worker_id: str
    tenant_id: str
    oss_prefix: str
    cancel_token: CancelToken
    pause_token: PauseToken
    cost_meter: CostMeter
    event_publisher: EventPublisher
    cost_publisher: CostEventPublisher
    checkpoint_store: CheckpointStore
    oss_client: OssClient
    logger: structlog.stdlib.BoundLogger
    trace_id: str
    traceparent: str | None = None
    trace_span: Any | None = None  # opentelemetry Span
    step: int = 0
    # Per-run-per-kind monotonic counters for task.events / cost.events.
    event_seq: int = 0
    cost_seq_by_kind: dict[str, int] = field(default_factory=dict)

    def next_event_seq(self) -> int:
        self.event_seq += 1
        return self.event_seq

    def next_cost_seq(self, kind: str) -> int:
        cur = self.cost_seq_by_kind.get(kind, 0) + 1
        self.cost_seq_by_kind[kind] = cur
        return cur
