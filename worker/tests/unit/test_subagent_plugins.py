"""Unit tests for the bundled planner/executor/critic subagent plugins."""

from __future__ import annotations

from pathlib import Path

import pytest
from worker.plugins.loader import PluginLoadError, default_plugins_root, load_plugins
from worker.plugins.subagent_spec import SubagentDefinition


@pytest.mark.parametrize("role", ["planner", "executor", "critic"])
def test_bundled_subagent_resolves_to_definition(role: str) -> None:
    registry = load_plugins()
    record = registry.get_subagent(role)
    assert record is not None
    assert record.manifest.kind == "subagent"
    assert record.manifest.name == role

    build = record.resolve()
    definition = build()
    assert isinstance(definition, SubagentDefinition)
    assert definition.name == role

    # The instruction equals the sibling prompt.md contents (stripped).
    prompt_path = default_plugins_root() / "subagent" / role / "prompt.md"
    assert definition.instruction == prompt_path.read_text(encoding="utf-8").strip()


def test_subagent_kind_mismatch_aborts(tmp_path: Path) -> None:
    """A plugin.yaml under subagent/ declaring a non-subagent kind aborts loading."""
    plugin = tmp_path / "subagent" / "wrong-kind" / "plugin.yaml"
    plugin.parent.mkdir(parents=True)
    plugin.write_text("kind: tool\nname: x\nversion: 0.1.0\nentrypoint: pkg:fn\n")
    with pytest.raises(PluginLoadError):
        load_plugins(root=tmp_path)
