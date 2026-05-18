"""Worker process entry point.

Registered as ``worker = "worker.main:run"`` in ``pyproject.toml``. Use
``uv run worker`` (or ``python -m worker``) to launch.
"""

from __future__ import annotations

import asyncio
import sys

from worker.core.config import load_or_exit
from worker.core.lifecycle import serve


def run() -> None:
    """Synchronous wrapper for the asyncio event loop."""
    settings = load_or_exit(sys.argv[1:])
    exit_code = asyncio.run(serve(settings))
    sys.exit(exit_code)


if __name__ == "__main__":
    run()
