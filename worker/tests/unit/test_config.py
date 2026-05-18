"""Unit tests for ``worker.core.config``."""

from __future__ import annotations

import re
import uuid
from pathlib import Path

import pytest
from worker.core.config import ConfigError, Settings, load, load_or_exit


def test_load_with_full_env(required_env: dict[str, str]) -> None:
    settings = load(env=required_env)
    assert settings.rabbitmq_url == required_env["RABBITMQ_URL"]
    assert settings.database_url == required_env["DATABASE_URL"]
    assert settings.oss_bucket == required_env["OSS_BUCKET"]
    # WORKER_ID auto-generated when absent (UUIDv4)
    assert isinstance(settings.worker_id, str)
    uuid.UUID(settings.worker_id)  # would raise if not a uuid


def test_missing_required_keys_raises(required_env: dict[str, str]) -> None:
    env = dict(required_env)
    del env["DATABASE_URL"]
    with pytest.raises(ConfigError) as info:
        load(env=env)
    assert "DATABASE_URL" in info.value.missing_keys


def test_yaml_overlay_filled_when_env_missing(tmp_path: Path, required_env: dict[str, str]) -> None:
    env = dict(required_env)
    del env["OSS_BUCKET"]
    cfg = tmp_path / "config.yaml"
    cfg.write_text("oss_bucket: from-yaml\n", encoding="utf-8")
    settings = load(env=env, config_path=cfg)
    assert settings.oss_bucket == "from-yaml"


def test_env_wins_over_yaml(tmp_path: Path, required_env: dict[str, str]) -> None:
    env = dict(required_env)
    env["OSS_BUCKET"] = "from-env"
    cfg = tmp_path / "config.yaml"
    cfg.write_text("oss_bucket: from-yaml\n", encoding="utf-8")
    settings = load(env=env, config_path=cfg)
    assert settings.oss_bucket == "from-env"


def test_worker_id_used_from_env(required_env: dict[str, str]) -> None:
    env = dict(required_env)
    env["WORKER_ID"] = "fixed-id"
    settings = load(env=env)
    assert settings.worker_id == "fixed-id"


def test_invalid_log_level_raises(required_env: dict[str, str]) -> None:
    env = dict(required_env)
    env["LOG_LEVEL"] = "SHOUT"
    with pytest.raises(ConfigError):
        load(env=env)


def test_load_or_exit_exits_on_missing(
    required_env: dict[str, str],
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    env = dict(required_env)
    del env["RABBITMQ_URL"]
    # Wire load_or_exit by patching the process env.
    monkeypatch.setattr("os.environ", env)
    with pytest.raises(SystemExit) as info:
        load_or_exit([])
    assert info.value.code == 2
    captured = capsys.readouterr()
    assert "RABBITMQ_URL" in captured.err
    assert re.search(r'"event"\s*:\s*"config_load_failed"', captured.err)


def test_settings_defaults(required_env: dict[str, str]) -> None:
    settings = load(env=required_env)
    assert settings.heartbeat_interval == 5.0
    assert settings.checkpoint_inline_bytes == 8 * 1024
    assert settings.lane == "default"
    assert settings.metrics_port == 9090
    assert settings.drain_timeout_seconds == 60.0
    assert isinstance(settings, Settings)
