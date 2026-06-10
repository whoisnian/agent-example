"""Process lifecycle orchestration.

Composes the dependency graph in the correct order, wires SIGTERM / SIGINT
into a graceful drain, and enforces the drain timeout (spec: worker-bootstrap
→ "Process Lifecycle and Graceful Shutdown").
"""

from __future__ import annotations

import asyncio
import signal
from contextlib import AsyncExitStack, suppress
from typing import TYPE_CHECKING

from worker.agents import build_agent_registry
from worker.agents.model import ProviderModelFactory
from worker.core.config import Settings
from worker.core.consumer import TaskConsumer
from worker.core.control import ControlListener
from worker.core.dispatcher import ExecutionDispatcher
from worker.core.logging import setup_logging
from worker.core.metrics import build_metrics, start_metrics_server
from worker.core.mq_connection import (
    MqConnection,
    assert_topology,
    declare_worker_queues,
)
from worker.core.persistence import Persistence
from worker.core.publisher import CostEventPublisher, EventPublisher
from worker.core.storage import OssClient
from worker.core.tracing import setup_tracing, shutdown_tracing
from worker.plugins.loader import load_plugins

if TYPE_CHECKING:
    pass


async def serve(settings: Settings) -> int:
    """Run the worker until SIGTERM / SIGINT, then drain.

    Returns the desired exit code; the caller invokes ``sys.exit``.
    """
    logger = setup_logging(worker_id=settings.worker_id, log_level=settings.log_level)
    logger.info("worker_starting", worker_id=settings.worker_id, lane=settings.lane)

    setup_tracing(
        worker_id=settings.worker_id,
        otlp_endpoint=settings.otel_exporter_otlp_endpoint,
    )

    metrics = build_metrics()
    # Prometheus client owns a daemon thread per call; we use a private
    # registry to avoid leaks across pytest runs and pass it to start_http_server.
    from prometheus_client import CollectorRegistry

    registry = CollectorRegistry()
    # Re-register on the new registry so the HTTP exposition shows them.
    metrics = build_metrics(registry=registry)
    start_metrics_server(settings.metrics_port, registry=registry)
    logger.info("metrics_server_started", port=settings.metrics_port)

    # Eagerly load plugins so malformed yaml aborts before MQ consumer.
    registry_plugins = load_plugins()
    logger.info("plugins_loaded", count=len(registry_plugins))

    async with AsyncExitStack() as stack:
        persistence = await Persistence.connect(
            settings.database_url,
            heartbeat_interval_seconds=settings.heartbeat_interval,
        )
        stack.push_async_callback(persistence.close)
        logger.info("db_connected")

        mq = MqConnection(settings.rabbitmq_url, logger=logger)
        await mq.connect()
        stack.push_async_callback(mq.close)
        channel = await mq.channel()

        await assert_topology(channel)
        execute_queue, control_queue, control_exchange = await declare_worker_queues(
            channel, lane=settings.lane, worker_id=settings.worker_id
        )
        logger.info("topology_asserted_and_queues_declared")

        oss = OssClient(
            endpoint_url=settings.oss_endpoint,
            bucket=settings.oss_bucket,
            access_key_id=settings.oss_access_key_id,
            access_key_secret=settings.oss_access_key_secret,
        )

        event_pub = EventPublisher(mq, metrics=metrics, logger=logger)
        cost_pub = CostEventPublisher(mq, metrics=metrics, logger=logger)

        model_factory = ProviderModelFactory(
            model_by_key={
                "code": settings.code_agent_model,
                "research": settings.research_agent_model,
            },
            api_key=settings.openai_api_key,
            base_url=settings.openai_base_url,
        )
        agent_registry = build_agent_registry(
            registry_plugins, model_factory, persistence, settings, metrics
        )
        logger.info("agents_registered", count=len(agent_registry))
        dispatcher = ExecutionDispatcher(agent_registry)

        control = ControlListener(
            worker_id=settings.worker_id,
            mq=mq,
            redis_url=settings.redis_url,
            metrics=metrics,
            control_queue=control_queue,
            control_exchange=control_exchange,
            logger=logger,
        )
        consumer = TaskConsumer(
            worker_id=settings.worker_id,
            lane=settings.lane,
            mq_channel=channel,
            queue=execute_queue,
            persistence=persistence,
            oss_client=oss,
            event_publisher=event_pub,
            cost_publisher=cost_pub,
            dispatcher=dispatcher,
            control_listener=control,
            metrics=metrics,
            logger=logger,
            heartbeat_interval=settings.heartbeat_interval,
            checkpoint_inline_bytes=settings.checkpoint_inline_bytes,
        )

        shutdown_event = asyncio.Event()
        loop = asyncio.get_running_loop()

        def _request_shutdown() -> None:
            logger.info("shutdown_requested")
            shutdown_event.set()
            consumer.stop()

        for sig in (signal.SIGTERM, signal.SIGINT):
            with suppress(NotImplementedError):
                loop.add_signal_handler(sig, _request_shutdown)

        consumer_task = asyncio.create_task(consumer.run(), name="consumer")
        control_task = asyncio.create_task(control.run(), name="control")

        await shutdown_event.wait()

        # Drain phase.
        try:
            await asyncio.wait_for(consumer_task, timeout=settings.drain_timeout_seconds)
        except TimeoutError:
            logger.warning("drain_timeout_force_exit")
            metrics.forced_shutdown_total.inc()
            consumer_task.cancel()
            # CancelledError derives from BaseException, so a bare
            # ``suppress(Exception)`` lets the cancellation escape ``serve``.
            with suppress(asyncio.CancelledError, Exception):
                await consumer_task

        control_task.cancel()
        with suppress(asyncio.CancelledError, Exception):
            await control_task

    shutdown_tracing()
    logger.info("worker_stopped")
    return 0
