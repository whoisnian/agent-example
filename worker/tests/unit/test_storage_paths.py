"""Unit tests for the OSS path guard helpers."""

from __future__ import annotations

import pytest
from worker.core.storage import OssPathError, _normalize_key


def test_normalize_joins_simple() -> None:
    assert (
        _normalize_key("tenant/task/v1/", "checkpoints/3.bin") == "tenant/task/v1/checkpoints/3.bin"
    )


@pytest.mark.parametrize(
    "prefix,key",
    [
        ("tenant/", "../etc/passwd"),
        ("tenant/", "a/../../escape"),
        ("tenant/", "/abs/path"),
        ("tenant/", "back\\slash"),
        ("tenant/", ""),
        ("no-slash", "anything"),
        ("/leading-slash/", "x"),
        ("ok/", ".."),
    ],
)
def test_normalize_rejects_traversal(prefix: str, key: str) -> None:
    with pytest.raises(OssPathError):
        _normalize_key(prefix, key)


def test_dot_segments_collapse_within_prefix() -> None:
    # 'a/./b' is fine; 'a/../b' goes outside is rejected only when it actually escapes.
    assert _normalize_key("p/", "a/./b") == "p/a/b"


def test_keep_within_prefix() -> None:
    # 'sub/../sub2' resolves to 'p/sub2' which is still inside the prefix.
    assert _normalize_key("p/", "sub/../sub2") == "p/sub2"
