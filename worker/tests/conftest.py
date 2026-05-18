"""Shared pytest fixtures.

Heavy / live-stack fixtures live under ``tests/integration/conftest.py`` so
that the unit lane (``pytest -m 'not integration'``) does not import
testcontainers.
"""

from __future__ import annotations

import pytest


@pytest.fixture
def required_env() -> dict[str, str]:
    """Minimum env dict to satisfy ``Settings`` required fields."""
    return {
        "RABBITMQ_URL": "amqp://guest:guest@localhost:5672/",
        "DATABASE_URL": "postgres://postgres:postgres@localhost:5432/agent_example",
        "OSS_ENDPOINT": "http://localhost:9000",
        "OSS_BUCKET": "worker-bucket",
        "OSS_ACCESS_KEY_ID": "minioadmin",
        "OSS_ACCESS_KEY_SECRET": "minioadmin",
    }
