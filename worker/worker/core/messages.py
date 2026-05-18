"""Wire-format pydantic models for messages exchanged with the broker.

These shapes match ``docs/ARCHITECTURE.md §5.3`` and the contract restated by
the ``worker-messaging`` spec. Keep this module dependency-light — it is
imported from many places and ought not pull in MQ / DB clients.
"""

from __future__ import annotations

from datetime import datetime
from typing import Any
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field


class TaskExecuteMessage(BaseModel):
    """Inbound envelope on ``task.exchange`` routed to ``q.task.execute.<lane>``."""

    model_config = ConfigDict(extra="ignore", frozen=True)

    msg_id: UUID
    idempotency_key: str
    task_id: UUID
    version_id: UUID
    run_id: UUID
    attempt_no: int
    task_type: str
    prompt: str = ""
    params: dict[str, Any] = Field(default_factory=dict)
    parent_version_id: UUID | None = None
    parent_artifact_root: str | None = None
    deadline_ts: int | None = None
    # Optional tenant context; carried for OSS prefix resolution. When absent
    # we fall back to a deterministic placeholder so the scaffold runs.
    tenant_id: str | None = None
    # Optional inbound trace context (W3C traceparent). Headers are also
    # checked at the AMQP layer; this field is for tests / replay use.
    traceparent: str | None = None


class TaskEvent(BaseModel):
    """Outbound message on ``task.events`` (Realtime Gateway + DB consumer)."""

    model_config = ConfigDict(extra="forbid", frozen=True)

    task_id: UUID
    version_id: UUID
    run_id: UUID
    seq: int
    kind: str
    payload: dict[str, Any]
    ts: datetime


class CostEvent(BaseModel):
    """Outbound message on ``cost.exchange`` (Cost Service)."""

    model_config = ConfigDict(extra="forbid", frozen=True)

    task_id: UUID
    version_id: UUID
    run_id: UUID
    seq: int
    kind: str  # llm | tool | compute
    resource_name: str
    input_tokens: int | None = None
    output_tokens: int | None = None
    cached_tokens: int | None = None
    calls: int | None = None
    duration_ms: int | None = None
    occurred_at: datetime


class ControlMessage(BaseModel):
    """Control signal payload (RMQ direct + Redis pub/sub)."""

    model_config = ConfigDict(extra="ignore", frozen=True)

    task_id: UUID
    run_id: UUID
    action: str  # pause | resume | cancel
    ts: str
