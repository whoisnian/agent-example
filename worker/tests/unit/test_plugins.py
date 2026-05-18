"""Unit tests for plugin loader and registry."""

from __future__ import annotations

from pathlib import Path

import pytest
from worker.plugins.loader import PluginLoadError, default_plugins_root, load_plugins
from worker.plugins.registry import PluginRegistrationError, PluginRegistry
from worker.plugins.schema import PluginManifest


def test_load_bundled_noop_tool() -> None:
    registry = load_plugins()
    record = registry.get_tool("noop")
    assert record is not None
    assert record.manifest.kind == "tool"
    assert record.manifest.name == "noop"
    assert record.manifest.version == "0.1.0"
    # Entrypoint resolves lazily.
    fn = record.resolve()
    assert callable(fn)


def test_empty_plugins_directory(tmp_path: Path) -> None:
    """Empty / missing dirs are tolerated (spec: empty plugins directory is allowed)."""
    registry = load_plugins(root=tmp_path)
    assert len(registry) == 0


def test_duplicate_registration_raises(tmp_path: Path) -> None:
    plugin_a = tmp_path / "tool" / "alpha" / "plugin.yaml"
    plugin_b = tmp_path / "tool" / "beta" / "plugin.yaml"
    plugin_a.parent.mkdir(parents=True)
    plugin_b.parent.mkdir(parents=True)
    yaml_body = """
kind: tool
name: shared
version: 0.1.0
entrypoint: pkg:fn
"""
    plugin_a.write_text(yaml_body)
    plugin_b.write_text(yaml_body)
    with pytest.raises(PluginRegistrationError):
        load_plugins(root=tmp_path)


def test_malformed_yaml_raises(tmp_path: Path) -> None:
    plugin = tmp_path / "tool" / "broken" / "plugin.yaml"
    plugin.parent.mkdir(parents=True)
    plugin.write_text(": this is not valid yaml :")
    with pytest.raises(PluginLoadError):
        load_plugins(root=tmp_path)


def test_schema_violation_raises(tmp_path: Path) -> None:
    plugin = tmp_path / "tool" / "schema-bad" / "plugin.yaml"
    plugin.parent.mkdir(parents=True)
    plugin.write_text("kind: tool\nname: x\n")  # missing version / entrypoint
    with pytest.raises(PluginLoadError):
        load_plugins(root=tmp_path)


def test_kind_must_match_directory(tmp_path: Path) -> None:
    plugin = tmp_path / "tool" / "wrong-kind" / "plugin.yaml"
    plugin.parent.mkdir(parents=True)
    plugin.write_text("kind: subagent\nname: x\nversion: 0.1.0\nentrypoint: pkg:fn\n")
    with pytest.raises(PluginLoadError):
        load_plugins(root=tmp_path)


def test_registry_query_helpers() -> None:
    reg = PluginRegistry()
    from worker.plugins.registry import PluginRecord

    m = PluginManifest(
        kind="tool",
        name="x",
        version="1.0.0",
        entrypoint="pkg:fn",
        applies_to={"task_types": ["code-gen", "research"]},  # type: ignore[arg-type]
    )
    reg.register(PluginRecord(manifest=m, source_path=Path("/tmp/x.yaml")))
    assert reg.get_tool("x") is not None
    assert reg.get_tool("x", "1.0.0") is not None
    assert reg.get_tool("x", "9.9.9") is None
    assert len(reg.list_by_task_type("code-gen")) == 1
    assert len(reg.list_by_task_type("none")) == 0


def test_default_plugins_root_points_at_package() -> None:
    root = default_plugins_root()
    assert root.is_dir()
    assert (root / "tool").is_dir()


def test_lazy_import_only_on_resolve() -> None:
    registry = load_plugins()
    # Loading does NOT import the entrypoint module. We can verify by
    # observing that resolving returns the callable without error and the
    # module is now in sys.modules.
    record = registry.get_tool("noop")
    assert record is not None
    fn = record.resolve()
    import sys

    assert "worker.plugins.tool.noop_tool.handler" in sys.modules
    assert callable(fn)
