"""Structured JSON logging via ``structlog``.

Every log entry MUST include ``ts``, ``level``, ``event``, ``worker_id``;
when emitted inside a ``RunContext`` it must also carry ``task_id``,
``run_id``, ``step``, ``trace_id`` (spec: worker-bootstrap → "Structured
Logging").
"""

from __future__ import annotations

import logging
import sys
from typing import Any, Protocol

import structlog


class RunLogContext(Protocol):
    """Structural type for ``bind_run_context`` — accepts anything carrying the bind fields.

    Lets the consumer pre-build a tiny shim before the full ``RunContext`` is
    constructed, without taking a runtime dependency on the RunContext type.
    """

    task_id: Any
    run_id: Any
    version_id: Any
    step: int
    trace_id: str


def setup_logging(*, worker_id: str, log_level: str = "INFO") -> structlog.stdlib.BoundLogger:
    """Configure structlog + stdlib logging for JSON output to stderr.

    Returns a root logger pre-bound with ``worker_id``. Task-scoped fields are
    added per-run via :func:`bind_run_context`.
    """
    level_num = getattr(logging, log_level.upper(), logging.INFO)

    # Bridge stdlib logging (used by third-party libs) into the structlog
    # pipeline so output is uniform JSON.
    logging.basicConfig(
        format="%(message)s",
        stream=sys.stderr,
        level=level_num,
        force=True,
    )

    structlog.configure(
        processors=[
            structlog.contextvars.merge_contextvars,
            structlog.processors.add_log_level,
            structlog.processors.TimeStamper(fmt="iso", utc=True, key="ts"),
            structlog.processors.StackInfoRenderer(),
            structlog.processors.format_exc_info,
            structlog.processors.JSONRenderer(),
        ],
        wrapper_class=structlog.make_filtering_bound_logger(level_num),
        logger_factory=structlog.PrintLoggerFactory(file=sys.stderr),
        cache_logger_on_first_use=True,
    )

    root: structlog.stdlib.BoundLogger = structlog.get_logger().bind(worker_id=worker_id)
    return root


def bind_run_context(
    logger: structlog.stdlib.BoundLogger, ctx: RunLogContext
) -> structlog.stdlib.BoundLogger:
    """Return a child logger with run-scoped fields bound.

    Adds ``task_id``, ``run_id``, ``step`` (defaults to 0), and ``trace_id``.
    The returned logger is independent — mutating the parent context does not
    affect already-bound loggers.
    """
    fields: dict[str, Any] = {
        "task_id": str(ctx.task_id),
        "run_id": str(ctx.run_id),
        "version_id": str(ctx.version_id),
        "step": ctx.step,
        "trace_id": ctx.trace_id,
    }
    return logger.bind(**fields)
