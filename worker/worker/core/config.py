"""Configuration loading for the Worker process.

Loads settings from environment variables (``pydantic-settings``) with an
optional YAML overlay supplied via ``--config <path>``. Environment variables
take precedence over YAML values. Missing required keys cause ``exit(2)`` with
a structured fatal log naming every missing key (spec: worker-bootstrap →
"Configuration Loading").
"""

from __future__ import annotations

import argparse
import os
import sys
import uuid
from pathlib import Path
from typing import Any

import yaml
from pydantic import Field, SecretStr, ValidationError, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Worker runtime settings, loaded from env (+ optional YAML overlay).

    Required keys: ``RABBITMQ_URL``, ``DATABASE_URL``, ``OSS_ENDPOINT``,
    ``OSS_BUCKET``, ``OSS_ACCESS_KEY_ID``, ``OSS_ACCESS_KEY_SECRET``.

    ``WORKER_ID`` auto-generates a UUIDv4 when absent (logged at info).
    """

    model_config = SettingsConfigDict(
        env_file=None,
        env_prefix="",
        case_sensitive=False,
        extra="ignore",
    )

    # Identity
    worker_id: str = Field(
        default_factory=lambda: str(uuid.uuid4()),
        description="UUIDv4 identifying this worker process; auto-generated when absent.",
    )

    # Required external dependencies (no defaults; missing → fail fast)
    rabbitmq_url: str = Field(..., description="amqp://user:pass@host:5672/vhost")
    database_url: str = Field(..., description="postgres://user:pass@host:5432/db")
    oss_endpoint: str = Field(..., description="S3-compatible endpoint URL")
    oss_bucket: str = Field(..., description="OSS bucket name")
    oss_access_key_id: str = Field(..., description="OSS access key id")
    oss_access_key_secret: str = Field(..., description="OSS access key secret")

    # Optional dependencies
    redis_url: str = Field(
        default="redis://localhost:6379/0",
        description="Redis URL for control signal fast-path.",
    )
    otel_exporter_otlp_endpoint: str | None = Field(
        default=None,
        description="OTLP HTTP exporter endpoint; when unset, a noop exporter is used.",
    )

    # Runtime knobs
    log_level: str = Field(default="INFO", description="Log level: DEBUG/INFO/WARNING/ERROR")
    metrics_port: int = Field(default=9090, description="Prometheus /metrics HTTP port")
    heartbeat_interval: float = Field(default=5.0, description="Seconds between heartbeat UPDATEs")
    checkpoint_inline_bytes: int = Field(
        default=8 * 1024,
        description="Max bytes for inline (DB JSONB) checkpoint state; larger goes to OSS.",
    )
    lane: str = Field(default="default", description="Queue lane suffix: q.task.execute.<lane>")
    drain_timeout_seconds: float = Field(
        default=60.0, description="Graceful shutdown drain timeout"
    )

    # Agent assembly (design D3/D9). Models are resolved per task_type by the
    # ModelFactory; the API key is held in a SecretStr so it is never rendered
    # in repr/log output (AGENTS.md §6).
    code_agent_model: str = Field(
        default="claude-opus-4-7",
        description="Chat model id for the code-gen agent (ARCHITECTURE §8.6).",
    )
    research_agent_model: str = Field(
        default="claude-sonnet-4-6",
        description="Chat model id for the research agent (ARCHITECTURE §8.6).",
    )
    openai_api_key: SecretStr | None = Field(
        default=None,
        description="Provider API key for ProviderModelFactory (OpenAI protocol); never logged.",
    )
    openai_base_url: str | None = Field(
        default=None,
        description="Optional OpenAI-compatible base URL (e.g. a gateway/proxy); None = OpenAI.",
    )
    max_step_retries: int = Field(
        default=2,
        ge=0,
        description="Per-delivery critic retry budget per step (design D13).",
    )

    @field_validator("log_level")
    @classmethod
    def _validate_log_level(cls, v: str) -> str:
        v_upper = v.upper()
        if v_upper not in {"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"}:
            raise ValueError(f"invalid LOG_LEVEL: {v}")
        return v_upper


_REQUIRED_KEYS: tuple[str, ...] = (
    "RABBITMQ_URL",
    "DATABASE_URL",
    "OSS_ENDPOINT",
    "OSS_BUCKET",
    "OSS_ACCESS_KEY_ID",
    "OSS_ACCESS_KEY_SECRET",
)


def _load_yaml_overlay(path: Path) -> dict[str, Any]:
    """Read a YAML file and return its top-level mapping (uppercased keys).

    Returns an empty dict when the file is empty. Raises ``ValueError`` if the
    file content is not a mapping.
    """
    raw = yaml.safe_load(path.read_text(encoding="utf-8"))
    if raw is None:
        return {}
    if not isinstance(raw, dict):
        raise ValueError(f"YAML config at {path} must be a mapping, got {type(raw).__name__}")
    # Uppercase keys so they match env var convention.
    return {str(k).upper(): v for k, v in raw.items()}


def parse_cli(argv: list[str] | None = None) -> argparse.Namespace:
    """Parse process argv for the ``--config`` flag.

    Kept as a separate helper so tests can drive it independently of ``load``.
    """
    parser = argparse.ArgumentParser(prog="worker", add_help=True)
    parser.add_argument(
        "--config",
        dest="config_path",
        type=Path,
        default=None,
        help="Optional YAML overlay applied on top of environment variables.",
    )
    return parser.parse_args(argv)


def load(
    *,
    env: dict[str, str] | None = None,
    config_path: Path | None = None,
) -> Settings:
    """Build a ``Settings`` instance from env + optional YAML overlay.

    Resolution rules:
    - Env vars (or the provided ``env`` mapping) win over YAML values.
    - Missing required keys raise ``ConfigError`` listing them all.
    - The optional YAML file must be a top-level mapping; its keys are
      uppercased to match env var convention.
    """
    effective_env: dict[str, str] = dict(env if env is not None else os.environ)

    if config_path is not None:
        overlay = _load_yaml_overlay(config_path)
        # YAML values fill keys missing from env (env wins).
        for k, v in overlay.items():
            effective_env.setdefault(k, "" if v is None else str(v))

    missing = [k for k in _REQUIRED_KEYS if not effective_env.get(k)]
    if missing:
        raise ConfigError(missing_keys=tuple(missing))

    # Map env-style upper-case keys to Settings field names (lower-case).
    kwargs: dict[str, Any] = {}
    known_fields = set(Settings.model_fields.keys())
    for key, value in effective_env.items():
        field = key.lower()
        if field in known_fields:
            kwargs[field] = value

    try:
        return Settings(**kwargs)
    except ValidationError as exc:
        raise ConfigError(missing_keys=(), validation_error=str(exc)) from exc


class ConfigError(Exception):
    """Raised when required configuration is missing or invalid."""

    def __init__(
        self,
        *,
        missing_keys: tuple[str, ...] = (),
        validation_error: str | None = None,
    ) -> None:
        self.missing_keys = missing_keys
        self.validation_error = validation_error
        if missing_keys:
            msg = f"missing required config keys: {', '.join(missing_keys)}"
        else:
            msg = f"invalid configuration: {validation_error}"
        super().__init__(msg)


def load_or_exit(argv: list[str] | None = None) -> Settings:
    """Wrapper for use from ``main``: log + ``exit(2)`` on failure.

    Use ``load`` directly in tests so failure paths can be asserted without
    process exit.
    """
    args = parse_cli(argv)
    try:
        return load(config_path=args.config_path)
    except ConfigError as exc:
        # Logging is not yet initialized at this point — emit a structured
        # JSON line directly to stderr so operators can grep it cleanly.
        import json

        record = {
            "level": "fatal",
            "event": "config_load_failed",
            "missing_keys": list(exc.missing_keys),
            "error": exc.validation_error,
        }
        print(json.dumps(record), file=sys.stderr)
        sys.exit(2)
