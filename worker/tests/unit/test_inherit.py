"""Unit tests for parent-artifact inheritance (add-worker-rollback-handling)."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any
from uuid import uuid4

from worker.agents.inherit import inherit_parent_artifacts


class FakeOss:
    """Records copies; ``list_keys`` returns a fixed (relative_key, size) list."""

    def __init__(self, objects: list[tuple[str, int]]) -> None:
        self._objects = objects
        self.copied: list[tuple[str, str, str]] = []

    async def list_keys(self, prefix: str) -> list[tuple[str, int]]:
        return list(self._objects)

    async def server_side_copy(self, src_prefix: str, dst_prefix: str, key: str) -> str:
        self.copied.append((src_prefix, dst_prefix, key))
        return dst_prefix + key  # the absolute destination key


class FakePersistence:
    def __init__(self) -> None:
        self.rows: list[dict[str, Any]] = []

    async def insert_artifact(
        self,
        *,
        version_id: Any,
        kind: str,
        oss_key: str,
        mime: str | None,
        bytes_size: int | None,
        sha256: str | None,
    ) -> Any:
        self.rows.append(
            {
                "version_id": version_id,
                "kind": kind,
                "oss_key": oss_key,
                "mime": mime,
                "bytes_size": bytes_size,
                "sha256": sha256,
            }
        )
        return uuid4()


def _ctx(oss: FakeOss) -> Any:
    version_id = uuid4()
    return SimpleNamespace(
        tenant_id="tenant-a",
        task_id=uuid4(),
        version_id=version_id,
        oss_prefix=f"tenant-a/task/{version_id}/",
        oss_client=oss,
    )


async def test_inherit_copies_and_records_each_object() -> None:
    oss = FakeOss([("code/main.py", 12), ("README.md", 5)])
    persistence = FakePersistence()
    ctx = _ctx(oss)

    copied = await inherit_parent_artifacts(ctx, persistence, uuid4())

    assert copied == [("code/main.py", 12), ("README.md", 5)]
    # Each object copied into the new run's prefix.
    assert [c[1] for c in oss.copied] == [ctx.oss_prefix, ctx.oss_prefix]
    assert [c[2] for c in oss.copied] == ["code/main.py", "README.md"]
    # One row per object, keyed on the copy's absolute key, with size carried.
    assert [r["oss_key"] for r in persistence.rows] == [
        ctx.oss_prefix + "code/main.py",
        ctx.oss_prefix + "README.md",
    ]
    assert [r["bytes_size"] for r in persistence.rows] == [12, 5]
    assert all(r["kind"] == "file" and r["sha256"] is None for r in persistence.rows)


async def test_inherit_skips_checkpoint_blobs() -> None:
    oss = FakeOss([("checkpoints/0.bin", 999), ("out.txt", 3)])
    persistence = FakePersistence()
    ctx = _ctx(oss)

    copied = await inherit_parent_artifacts(ctx, persistence, uuid4())

    assert copied == [("out.txt", 3)]
    assert [c[2] for c in oss.copied] == ["out.txt"]  # checkpoint blob not copied
    assert [r["oss_key"] for r in persistence.rows] == [ctx.oss_prefix + "out.txt"]


async def test_inherit_empty_parent_is_noop() -> None:
    oss = FakeOss([])
    persistence = FakePersistence()
    ctx = _ctx(oss)

    copied = await inherit_parent_artifacts(ctx, persistence, uuid4())

    assert copied == []
    assert oss.copied == []
    assert persistence.rows == []


# --- LoopAgent gating ------------------------------------------------------


class FakeCheckpointStore:
    def __init__(self, latest_val: Any = None) -> None:
        self._latest = latest_val

    async def latest(self) -> Any:
        return self._latest


def _loop_agent(persistence: FakePersistence) -> Any:
    from pathlib import Path

    from worker.agents.base import AgentSpec, LoopAgent

    from tests.support.fake_model import FakeModelFactory, scripted_model

    async def _noop_write(ctx: Any, path: str, content: str) -> Any:  # pragma: no cover
        raise AssertionError("write_file must not be called by the gate test")

    spec = AgentSpec(
        task_type="code-gen", model_key="code", system_prompt_path=Path("/nonexistent")
    )
    return LoopAgent(
        spec=spec,
        model_factory=FakeModelFactory(model=scripted_model([])),
        persistence=persistence,
        write_file=_noop_write,
        max_step_retries=1,
    )


def _gate_ctx(oss: FakeOss, latest: Any = None) -> Any:
    import structlog

    version_id = uuid4()
    return SimpleNamespace(
        tenant_id="tenant-a",
        task_id=uuid4(),
        version_id=version_id,
        oss_prefix=f"tenant-a/task/{version_id}/",
        oss_client=oss,
        checkpoint_store=FakeCheckpointStore(latest),
        logger=structlog.get_logger(),
    )


def _msg(parent_version_id: Any = None, parent_artifact_root: Any = None) -> Any:
    return SimpleNamespace(
        parent_version_id=parent_version_id, parent_artifact_root=parent_artifact_root
    )


async def test_fresh_run_with_parent_inherits() -> None:
    oss = FakeOss([("a.py", 1)])
    persistence = FakePersistence()
    agent = _loop_agent(persistence)
    await agent._maybe_inherit_parent_artifacts(
        _gate_ctx(oss, latest=None), _msg(parent_version_id=uuid4())
    )
    assert len(persistence.rows) == 1 and len(oss.copied) == 1


async def test_no_parent_version_id_skips() -> None:
    oss = FakeOss([("a.py", 1)])
    persistence = FakePersistence()
    agent = _loop_agent(persistence)
    await agent._maybe_inherit_parent_artifacts(
        _gate_ctx(oss, latest=None), _msg(parent_version_id=None)
    )
    assert persistence.rows == [] and oss.copied == []


async def test_resume_skips_inheritance() -> None:
    oss = FakeOss([("a.py", 1)])
    persistence = FakePersistence()
    agent = _loop_agent(persistence)
    # A prior checkpoint exists → resume → skip.
    await agent._maybe_inherit_parent_artifacts(
        _gate_ctx(oss, latest=object()), _msg(parent_version_id=uuid4())
    )
    assert persistence.rows == [] and oss.copied == []
