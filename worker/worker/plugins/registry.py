"""In-memory plugin registry.

Plugins are keyed by ``(kind, name, version)``. Duplicate keys cause
:class:`PluginRegistrationError` (spec: worker-execution-runtime → "Plugin
Loader"). The ``entrypoint`` attribute is imported lazily on first call,
which lets startup remain fast even with many plugins.
"""

from __future__ import annotations

import importlib
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from worker.plugins.schema import PluginManifest


class PluginRegistrationError(RuntimeError):
    """Raised when a duplicate plugin is registered."""


@dataclass(frozen=True, slots=True)
class PluginRecord:
    """An entry in the in-memory registry.

    ``manifest`` carries the validated YAML; ``source_path`` is the absolute
    path on disk where the yaml lived (useful for diagnostics).
    """

    manifest: PluginManifest
    source_path: Path

    def resolve(self) -> Any:
        """Lazily import the entrypoint module and return the callable.

        Import errors propagate to the caller — the registry deliberately does
        not catch them so they surface as real exceptions at agent runtime
        rather than masking misconfigured plugins.
        """
        module_name, attr = self.manifest.parsed_entrypoint()
        module = importlib.import_module(module_name)
        return getattr(module, attr)


class PluginRegistry:
    """Lookup table for registered plugins.

    All mutation methods raise :class:`PluginRegistrationError` on duplicates;
    the lookup methods are pure reads.
    """

    def __init__(self) -> None:
        self._records: dict[tuple[str, str, str], PluginRecord] = {}

    def register(self, record: PluginRecord) -> None:
        key = (record.manifest.kind, record.manifest.name, record.manifest.version)
        existing = self._records.get(key)
        if existing is not None:
            raise PluginRegistrationError(
                f"duplicate plugin {key!r}: already registered from {existing.source_path}, "
                f"refusing to register {record.source_path}"
            )
        self._records[key] = record

    def get(self, kind: str, name: str, version: str | None = None) -> PluginRecord | None:
        if version is not None:
            return self._records.get((kind, name, version))
        # Pick the lexicographically largest version when not pinned.
        candidates = sorted(
            (rec for (k, n, _), rec in self._records.items() if k == kind and n == name),
            key=lambda r: r.manifest.version,
            reverse=True,
        )
        return candidates[0] if candidates else None

    def get_tool(self, name: str, version: str | None = None) -> PluginRecord | None:
        return self.get("tool", name, version)

    def get_subagent(self, name: str, version: str | None = None) -> PluginRecord | None:
        return self.get("subagent", name, version)

    def list_by_task_type(self, task_type: str) -> list[PluginRecord]:
        return [
            rec for rec in self._records.values() if task_type in rec.manifest.applies_to.task_types
        ]

    def __len__(self) -> int:
        return len(self._records)

    def __iter__(self) -> Any:
        return iter(self._records.values())
