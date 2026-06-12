"""Wire-format pydantic models for messages exchanged with the broker.

These shapes match ``docs/ARCHITECTURE.md §5.3`` and the contract restated by
the ``worker-messaging`` spec. Keep this module dependency-light — it is
imported from many places and ought not pull in MQ / DB clients.
"""

from __future__ import annotations

from datetime import datetime
from typing import Any
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, TypeAdapter, model_validator


class HistoryTurn(BaseModel):
    """One conversation turn in the execute payload's ``history`` array.

    Shape per ``task-conversation-history``: ``summary`` is null for versions
    without a worker summary; ``status`` is the version's terminal status so
    failed prior attempts can be rendered as such.
    """

    model_config = ConfigDict(extra="ignore", frozen=True)

    version_no: int
    prompt: str
    summary: str | None = None
    status: str = "succeeded"


_HISTORY_ADAPTER = TypeAdapter(list[HistoryTurn])


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
    # Create-only flag: set by the API only on the create path when the task
    # title was derived (placeholder) rather than user-supplied. Iterate /
    # rollback / any republish never set it; absent means False.
    gen_title: bool = False
    # Conversation history (task-conversation-history): absent → empty list.
    # A structurally invalid value degrades to [] with `history_invalid=True`
    # instead of poisoning the message — history is an enhancement signal, and
    # DLX-ing every iterate on a producer bug costs far more than running
    # without context (design D6). The consumer logs/counts the degradation.
    history: list[HistoryTurn] = Field(default_factory=list)
    history_invalid: bool = False
    # Optional tenant context; carried for OSS prefix resolution. When absent
    # we fall back to a deterministic placeholder so the scaffold runs.
    tenant_id: str | None = None
    # Optional inbound trace context (W3C traceparent). Headers are also
    # checked at the AMQP layer; this field is for tests / replay use.
    traceparent: str | None = None

    @model_validator(mode="before")
    @classmethod
    def _degrade_invalid_history(cls, data: Any) -> Any:
        if isinstance(data, dict) and "history" in data and data["history"] is not None:
            try:
                _HISTORY_ADAPTER.validate_python(data["history"])
            except Exception:  # noqa: BLE001 - any structural failure degrades
                data = {**data, "history": [], "history_invalid": True}
        return data


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
