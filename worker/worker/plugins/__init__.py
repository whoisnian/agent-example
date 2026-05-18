"""Plugin loader, registry, and bundled plugins.

Plugins are declared by ``plugin.yaml`` files under
``worker/plugins/{tool,subagent}/<name>/``. The loader scans these eagerly at
startup; entrypoints are imported lazily on first lookup (design D10).
"""

from worker.plugins.loader import PluginLoadError, load_plugins
from worker.plugins.registry import (
    PluginRecord,
    PluginRegistrationError,
    PluginRegistry,
)
from worker.plugins.schema import PluginManifest

__all__ = [
    "PluginLoadError",
    "PluginManifest",
    "PluginRecord",
    "PluginRegistrationError",
    "PluginRegistry",
    "load_plugins",
]
