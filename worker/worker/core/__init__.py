"""Cross-cutting infrastructure for the Worker process.

All MQ / DB / OSS / observability primitives live here. Agents and plugins
MUST NOT import infrastructure clients directly — they MUST go through
``RunContext`` (see ``worker.core.run_context``).
"""
