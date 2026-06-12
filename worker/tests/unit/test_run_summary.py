"""Run-summary assembly + emission tests (spec: "Run Summary Event")."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any
from uuid import uuid4

import pytest
import structlog
from langchain_core.callbacks import BaseCallbackHandler
from worker.agents.loop import _RUN_SUMMARY_MAX_BYTES, assemble_run_summary
from worker.core.run_context import CancelToken, PauseToken

from tests.support.fake_model import FakeModelFactory, scripted_model


def test_assemble_run_summary_joins_step_lines_in_order() -> None:
    assert assemble_run_summary(["wrote main", "added tests"]) == "1. wrote main\n2. added tests"


def test_assemble_run_summary_skips_missing_and_empty() -> None:
    # Plan positions without a summary (None / "") are omitted; numbering
    # stays the step seq, not a compacted index.
    assert assemble_run_summary([None, "did two", ""]) == "2. did two"


def test_assemble_run_summary_all_empty_yields_empty_string() -> None:
    assert assemble_run_summary([None, None]) == ""


def test_assemble_run_summary_truncates_on_rune_boundary() -> None:
    out = assemble_run_summary(["汉" * 500, "汉" * 500])  # ~3 KB > 2048
    encoded = out.encode("utf-8")
    assert len(encoded) <= _RUN_SUMMARY_MAX_BYTES
    assert out.endswith("…")
    assert out == encoded.decode("utf-8")  # rune-boundary cut → decodable


# --- LoopAgent emission timing ----------------------------------------------


class _FakeCheckpointStore:
    async def latest(self) -> Any:
        return None

    async def write(self, **kwargs: Any) -> None:
        return None


class _OrderRecorder:
    """Captures the relative order of artifact inserts and event publishes."""

    def __init__(self) -> None:
        self.timeline: list[str] = []
        self.events: list[dict[str, Any]] = []

    async def insert_artifact(self, **kwargs: Any) -> Any:
        self.timeline.append("artifact_row")
        return uuid4()

    async def publish_event(self, **kwargs: Any) -> None:
        self.events.append(kwargs)
        self.timeline.append(f"event:{kwargs['kind']}")


def _ctx(rec: _OrderRecorder) -> Any:
    seq = {"n": 0}

    def next_event_seq() -> int:
        seq["n"] += 1
        return seq["n"]

    return SimpleNamespace(
        task_id=uuid4(),
        version_id=uuid4(),
        run_id=uuid4(),
        task_type="code-gen",
        traceparent=None,
        step=0,
        tenant_id="t",
        oss_prefix="t/task/v/",
        oss_client=None,
        checkpoint_store=_FakeCheckpointStore(),
        event_publisher=rec,
        cost_meter=BaseCallbackHandler(),
        cancel_token=CancelToken(),
        pause_token=PauseToken(),
        logger=structlog.get_logger(),
        next_event_seq=next_event_seq,
        restore_event_seq=lambda high: None,
        event_seq=0,
    )


def _agent(rec: _OrderRecorder, model: Any, tmp_path: Any) -> Any:
    from worker.agents.base import AgentSpec, LoopAgent

    prompt_file = tmp_path / "system.md"
    prompt_file.write_text("sys", encoding="utf-8")

    async def write_file(ctx: Any, path: str, content: str) -> Any:
        from worker.agents.loop import ProducedArtifact

        return ProducedArtifact(path=path, oss_key=f"k/{path}", bytes=len(content), sha256="h")

    return LoopAgent(
        spec=AgentSpec(task_type="code-gen", model_key="code", system_prompt_path=prompt_file),
        model_factory=FakeModelFactory(model=model),
        persistence=rec,
        write_file=write_file,
        max_step_retries=0,
    )


def _msg(ctx: Any) -> Any:
    return SimpleNamespace(
        parent_version_id=None,
        parent_artifact_root=None,
        deadline_ts=None,
        attempt_no=1,
        prompt="do the thing",
        history=[],
    )


async def test_summary_emitted_after_artifact_rows(tmp_path: Any) -> None:
    rec = _OrderRecorder()
    ctx = _ctx(rec)
    model = scripted_model(
        [
            '{"steps": ["one step"]}',
            '{"summary": "made a file", "files": [{"path": "a.py", "content": "x"}]}',
            '{"verdict": "finish"}',
        ]
    )
    await _agent(rec, model, tmp_path).run(ctx, _msg(ctx))

    summary_events = [e for e in rec.events if e["kind"] == "summary"]
    assert len(summary_events) == 1
    assert summary_events[0]["payload"]["summary"] == "1. made a file"
    # Ordering: artifact rows land BEFORE the summary event, which is last.
    assert rec.timeline.index("artifact_row") < rec.timeline.index("event:summary")
    assert rec.timeline[-1] == "event:summary"


async def test_failed_run_emits_no_summary(tmp_path: Any) -> None:
    rec = _OrderRecorder()
    ctx = _ctx(rec)
    # Planner output is invalid JSON → the run fails before any step.
    model = scripted_model(["not json at all"])
    with pytest.raises(ValueError):
        await _agent(rec, model, tmp_path).run(ctx, _msg(ctx))
    assert [e for e in rec.events if e["kind"] == "summary"] == []


async def test_all_empty_step_summaries_still_emit_event(tmp_path: Any) -> None:
    rec = _OrderRecorder()
    ctx = _ctx(rec)
    model = scripted_model(
        [
            '{"steps": ["one step"]}',
            '{"summary": "", "files": []}',
            '{"verdict": "finish"}',
        ]
    )
    await _agent(rec, model, tmp_path).run(ctx, _msg(ctx))
    summary_events = [e for e in rec.events if e["kind"] == "summary"]
    # Event still emitted with empty payload.summary; ingest skips the column
    # update but persists the event row (spec scenario).
    assert len(summary_events) == 1
    assert summary_events[0]["payload"]["summary"] == ""
