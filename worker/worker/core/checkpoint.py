"""Per-run checkpoint store.

Small JSON-serializable state is written inline into ``task_checkpoints.state``;
larger payloads (or payloads provided via ``large_payload``) are uploaded to
OSS at ``checkpoints/<oss_prefix><step_seq>.bin`` and only the OSS key is
stored in the row (spec: worker-execution-runtime → "Checkpoint Store").

Duplicate writes for the same ``(run_id, step_seq)`` raise
:class:`CheckpointConflictError` so callers can detect replays cleanly.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any
from uuid import UUID

from worker.core.persistence import Checkpoint, CheckpointConflictError, Persistence
from worker.core.storage import OssClient


@dataclass(frozen=True, slots=True)
class CheckpointRecord:
    """Public-facing checkpoint shape returned by ``latest()``."""

    step_seq: int
    step_name: str
    state: dict[str, Any]
    oss_key: str | None


class CheckpointStore:
    """Combines DB INSERT and (optional) OSS upload for a single run.

    Bound to one run by its ``oss_prefix``; the store rejects writes that
    would cross prefix boundaries (via :class:`OssClient`'s prefix guard).
    """

    def __init__(
        self,
        *,
        run_id: UUID,
        oss_prefix: str,
        persistence: Persistence,
        oss_client: OssClient,
        inline_byte_limit: int,
    ) -> None:
        self._run_id = run_id
        self._oss_prefix = oss_prefix
        self._persistence = persistence
        self._oss = oss_client
        self._inline_limit = inline_byte_limit

    async def write(
        self,
        *,
        step_seq: int,
        step_name: str,
        state: dict[str, Any],
        large_payload: bytes | None = None,
    ) -> CheckpointRecord:
        """Persist a checkpoint. See module docstring for routing rules.

        Raises :class:`CheckpointConflictError` on duplicate ``(run_id, step_seq)``.
        """
        oss_key: str | None = None

        if large_payload is not None:
            # Explicit blob → upload to OSS regardless of state size.
            relative_key = f"checkpoints/{step_seq}.bin"
            absolute_key = await self._oss.put(self._oss_prefix, relative_key, large_payload)
            oss_key = absolute_key
            inline_state = state
        else:
            payload_bytes = json.dumps(state).encode("utf-8")
            if len(payload_bytes) > self._inline_limit:
                relative_key = f"checkpoints/{step_seq}.bin"
                absolute_key = await self._oss.put(self._oss_prefix, relative_key, payload_bytes)
                oss_key = absolute_key
                inline_state = {"_oss_offloaded": True}
            else:
                inline_state = state

        try:
            await self._persistence.insert_checkpoint(
                run_id=self._run_id,
                step_seq=step_seq,
                step_name=step_name,
                state=inline_state,
                oss_key=oss_key,
            )
        except CheckpointConflictError:
            raise

        return CheckpointRecord(
            step_seq=step_seq,
            step_name=step_name,
            state=inline_state,
            oss_key=oss_key,
        )

    async def latest(self) -> CheckpointRecord | None:
        row: Checkpoint | None = await self._persistence.select_latest_checkpoint(self._run_id)
        if row is None:
            return None
        return CheckpointRecord(
            step_seq=row.step_seq,
            step_name=row.step_name,
            state=row.state,
            oss_key=row.oss_key,
        )
