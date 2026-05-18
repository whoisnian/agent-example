"""Plugin loader: scans ``plugin.yaml`` files and populates the registry.

Discovery is deterministic (sorted glob) so registration order is
reproducible across machines. Parse / schema errors abort startup with a
fatal log naming the offending file (spec: worker-bootstrap → "Plugin
Registry Initialization").
"""

from __future__ import annotations

from pathlib import Path
from typing import Final

import yaml
from pydantic import ValidationError

from worker.plugins.registry import PluginRecord, PluginRegistry
from worker.plugins.schema import PluginManifest

#: Subdirectories under ``worker/plugins/`` that host plugin definitions.
PLUGIN_KIND_DIRS: Final[tuple[str, ...]] = ("tool", "subagent")


class PluginLoadError(RuntimeError):
    """Raised when a plugin.yaml fails to parse or validate."""


def default_plugins_root() -> Path:
    """Return ``worker/plugins/`` relative to this file."""
    return Path(__file__).resolve().parent


def load_plugins(
    root: Path | None = None,
    registry: PluginRegistry | None = None,
) -> PluginRegistry:
    """Scan ``root`` for ``plugin.yaml`` files and register them.

    When ``root`` is ``None`` we use the package's own ``worker/plugins/``
    directory. Returns the (possibly newly created) registry.
    """
    base = root if root is not None else default_plugins_root()
    reg = registry if registry is not None else PluginRegistry()

    for kind_dir in PLUGIN_KIND_DIRS:
        kind_root = base / kind_dir
        if not kind_root.is_dir():
            continue
        for yaml_path in sorted(kind_root.glob("*/plugin.yaml")):
            manifest = _parse_manifest(yaml_path)
            if manifest.kind != kind_dir:
                # Defensive — yaml said it's a `subagent` but lives under
                # tool/. Surface clearly rather than silently mis-categorizing.
                raise PluginLoadError(
                    f"plugin.yaml at {yaml_path} declares kind={manifest.kind!r} "
                    f"but lives under {kind_dir}/"
                )
            reg.register(PluginRecord(manifest=manifest, source_path=yaml_path))
    return reg


def _parse_manifest(path: Path) -> PluginManifest:
    try:
        raw = yaml.safe_load(path.read_text(encoding="utf-8"))
    except yaml.YAMLError as exc:
        raise PluginLoadError(f"failed to parse {path}: {exc}") from exc
    if not isinstance(raw, dict):
        raise PluginLoadError(f"plugin.yaml at {path} must be a mapping")
    try:
        return PluginManifest.model_validate(raw)
    except ValidationError as exc:
        raise PluginLoadError(f"invalid plugin.yaml at {path}: {exc}") from exc
