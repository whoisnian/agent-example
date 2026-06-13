"""Unit tests for the oss_fs tool handler, incl. the delete op
(add-artifact-deletion)."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any

import pytest
from worker.plugins.tool.oss_fs.handler import oss_fs


class _FakeOss:
    def __init__(self, *, exists: set[str] | None = None) -> None:
        self._exists = exists if exists is not None else set()
        self.deleted: list[str] = []
        self.put: list[tuple[str, str]] = []

    async def delete(self, prefix: str, key: str) -> bool:
        # Mirrors the real client: True iff an object was present, else no-op.
        self.deleted.append(key)
        if key in self._exists:
            self._exists.discard(key)
            return True
        return False


def _ctx(oss: _FakeOss) -> Any:
    return SimpleNamespace(oss_client=oss, oss_prefix="t/task/v/")


async def test_delete_existing_key_reports_deleted() -> None:
    oss = _FakeOss(exists={"styles.css"})
    res = await oss_fs(_ctx(oss), op="delete", path="styles.css")
    assert res == {"path": "styles.css", "deleted": True}
    assert oss.deleted == ["styles.css"]


async def test_delete_missing_key_is_idempotent_noop() -> None:
    oss = _FakeOss(exists=set())
    res = await oss_fs(_ctx(oss), op="delete", path="gone.css")
    assert res == {"path": "gone.css", "deleted": False}


async def test_delete_ignores_content_argument() -> None:
    oss = _FakeOss(exists={"a.txt"})
    # content is accepted by the signature but irrelevant to delete.
    res = await oss_fs(_ctx(oss), op="delete", path="a.txt", content=None)
    assert res["deleted"] is True


async def test_unknown_op_still_raises() -> None:
    with pytest.raises(ValueError, match="unknown oss_fs op"):
        await oss_fs(_ctx(_FakeOss()), op="purge", path="x")


async def test_write_without_content_still_guarded() -> None:
    with pytest.raises(ValueError, match="requires `content`"):
        await oss_fs(_ctx(_FakeOss()), op="write", path="x")
