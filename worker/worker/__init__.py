"""Agent-example Worker service package.

Top-level package containing the asyncio process skeleton that consumes
``task.execute`` messages from RabbitMQ, drives a deepagents-based agent
(stub at this scaffold stage), and emits ``task.events`` / ``cost.events``.

Submodules:
- ``core``: cross-cutting infrastructure (config, logging, MQ, DB, OSS,
  cost meter, checkpoint store, etc.).
- ``plugins``: plugin loader, registry, and bundled stub plugins.
- ``agents``: per-task agents (empty at scaffold stage).
- ``main``: process entry point (``uv run worker``).
"""

__version__ = "0.1.0"
