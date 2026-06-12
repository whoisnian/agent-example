"""Unit tests for the conversation-context block assembler
(refactor-task-conversation-continuity)."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any

from worker.agents.context_block import (
    EXCERPT_TOTAL_BUDGET_BYTES,
    TEXT_EXCERPT_MAX_BYTES,
    assemble_context_block,
    is_text_artifact,
    render_history_section,
)
from worker.core.messages import HistoryTurn


class FakeOss:
    def __init__(self, contents: dict[str, bytes]) -> None:
        self._contents = contents
        self.reads: list[str] = []

    async def get(self, prefix: str, key: str) -> bytes:
        self.reads.append(key)
        return self._contents[key]


def _ctx(oss: FakeOss) -> Any:
    return SimpleNamespace(oss_client=oss, oss_prefix="t/task/v/")


def _turn(no: int, prompt: str, summary: str | None, status: str = "succeeded") -> HistoryTurn:
    return HistoryTurn(version_no=no, prompt=prompt, summary=summary, status=status)


def test_history_section_renders_oldest_first_with_markers() -> None:
    text = render_history_section(
        [
            _turn(1, "build app", "did v1"),
            _turn(2, "add login", None, status="failed"),
            _turn(3, "retry login", None),
        ]
    )
    lines = text.splitlines()
    assert lines[1] == "[v1] user: build app"
    assert lines[2] == "[v1] result: did v1"
    # Failed turn carries an explicit marker so the model does not treat it
    # as a successful prior result.
    assert lines[4] == "[v2] result: (this attempt ended FAILED)"
    # Null summary on a succeeded turn gets the no-summary marker.
    assert lines[6] == "[v3] result: (no summary recorded)"


async def test_empty_inputs_yield_empty_block() -> None:
    block = await assemble_context_block(_ctx(FakeOss({})), [], [])
    assert block == ""


async def test_inventory_lists_all_and_excerpts_small_text_files() -> None:
    oss = FakeOss({"index.html": b"<h1>hi</h1>", "notes.md": b"# note"})
    inherited = [
        ("index.html", 11),
        ("logo.png", 300_000),  # not text → list-only
        ("notes.md", 6),
    ]
    block = await assemble_context_block(_ctx(oss), [], inherited)
    # Inventory: every inherited object by path + size, lexicographic order.
    assert "- index.html (11 bytes)" in block
    assert "- logo.png (300000 bytes)" in block
    assert "- notes.md (6 bytes)" in block
    # Excerpts: ascending size → notes.md before index.html.
    assert oss.reads == ["notes.md", "index.html"]
    assert "--- notes.md ---\n# note" in block
    assert "--- index.html ---\n<h1>hi</h1>" in block
    # logo.png never read.
    assert "--- logo.png" not in block


async def test_excerpt_budget_and_single_file_ceiling() -> None:
    big = b"x" * (TEXT_EXCERPT_MAX_BYTES + 1)  # over the single-file ceiling
    filler_size = EXCERPT_TOTAL_BUDGET_BYTES - 10
    oss = FakeOss({"a.txt": b"a" * filler_size, "b.txt": b"b" * 100, "huge.md": big})
    inherited = [
        ("a.txt", filler_size),
        ("b.txt", 100),
        ("huge.md", len(big)),
    ]
    block = await assemble_context_block(_ctx(oss), [], inherited)
    # b.txt (smallest) consumes budget first; a.txt no longer fits; huge.md is
    # over the per-file ceiling. All three still appear in the inventory.
    assert oss.reads == ["b.txt"]
    assert "--- b.txt ---" in block
    assert "--- a.txt" not in block and "--- huge.md" not in block
    assert "- a.txt" in block and "- huge.md" in block


async def test_binary_masquerading_as_text_is_skipped() -> None:
    oss = FakeOss({"data.txt": b"\xff\xfe\x00\x01"})
    block = await assemble_context_block(_ctx(oss), [], [("data.txt", 4)])
    assert "- data.txt (4 bytes)" in block
    assert "--- data.txt" not in block  # undecodable → list-only


def test_is_text_artifact_heuristics() -> None:
    assert is_text_artifact("src/main.py")
    assert is_text_artifact("README.md")
    assert is_text_artifact("config.json")
    assert not is_text_artifact("logo.png")
    assert not is_text_artifact("binary.bin")
