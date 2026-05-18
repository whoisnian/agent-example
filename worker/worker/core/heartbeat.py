"""Heartbeat coroutine.

Runs in its own asyncio task while a run is in-flight. On 3 consecutive
failures it cancels the run via ``RunContext.cancel_token`` and increments
``worker_heartbeat_failures_total`` (spec: worker-execution-runtime →
"Heartbeat").
"""

from __future__ import annotations

import asyncio
from typing import TYPE_CHECKING
from uuid import UUID

import structlog

if TYPE_CHECKING:
    from worker.core.metrics import Metrics
    from worker.core.persistence import Persistence
    from worker.core.run_context import RunContext


MAX_CONSECUTIVE_FAILURES = 3


async def heartbeat_loop(
    *,
    ctx: RunContext,
    worker_run_id: UUID,
    persistence: Persistence,
    interval_seconds: float,
    metrics: Metrics,
    logger: structlog.stdlib.BoundLogger | None = None,
) -> None:
    """Update ``task_runs.last_heartbeat`` every ``interval_seconds``.

    Exits when:

    - ``RunContext.cancel_token`` is set (clean shutdown).
    - 3 consecutive UPDATEs fail — the runtime fires the cancel token and
      records a metric increment.
    - The task is cancelled by the outer TaskGroup.
    - The CAS guard fails because another worker took over.
    """
    log = (logger or ctx.logger).bind(component="heartbeat")
    failures = 0
    while not ctx.cancel_token.is_set():
        try:
            owned = await persistence.update_heartbeat(ctx.run_id, worker_run_id)
        except asyncio.CancelledError:
            raise
        except Exception as exc:  # noqa: BLE001
            failures += 1
            log.warning("heartbeat_update_failed", error=str(exc), failures=failures)
            if failures >= MAX_CONSECUTIVE_FAILURES:
                metrics.heartbeat_failures_total.inc()
                log.error("heartbeat_cancel_threshold_reached", failures=failures)
                ctx.cancel_token.set()
                return
        else:
            failures = 0
            if not owned:
                # CAS lost — another worker took over. Stop heartbeating;
                # the consumer will see the run was taken over and act.
                log.warning("heartbeat_cas_lost")
                ctx.cancel_token.set()
                return

        try:
            await asyncio.wait_for(
                ctx.cancel_token.wait(),
                timeout=interval_seconds,
            )
        except TimeoutError:
            continue
