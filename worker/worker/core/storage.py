"""S3-compatible OSS client wrapper with path prefix discipline.

All ``put`` / ``get`` calls accept a ``prefix`` argument that MUST end with
``/`` and a relative ``key`` that MUST NOT escape the prefix (no ``..``,
absolute paths, or backslashes). The actual object key issued to S3 is
``prefix + normalized_key`` (spec: worker-execution-runtime → "Run Context"
OSS prefix scoping).

Higher-level callers (CheckpointStore, agent-uploaded artifacts) MUST pass
``RunContext.oss_prefix`` so that no write can target a foreign run's
namespace.
"""

from __future__ import annotations

import posixpath
from typing import Any

import aioboto3


class OssPathError(ValueError):
    """Raised when a key violates path safety rules."""


def _validate_prefix(prefix: str) -> None:
    if not prefix.endswith("/"):
        raise OssPathError(f"prefix must end with '/': {prefix!r}")
    if prefix.startswith("/"):
        raise OssPathError(f"prefix must be relative (no leading '/'): {prefix!r}")
    if "\\" in prefix:
        raise OssPathError(f"prefix must not contain backslashes: {prefix!r}")


def _normalize_key(prefix: str, key: str) -> str:
    """Validate the relative key and return the joined absolute object key.

    Rejects: empty key, absolute paths, backslashes, paths containing ``..``
    that resolve outside the prefix.
    """
    _validate_prefix(prefix)
    if not key:
        raise OssPathError("key must not be empty")
    if key.startswith("/"):
        raise OssPathError(f"key must be relative: {key!r}")
    if "\\" in key:
        raise OssPathError(f"key must not contain backslashes: {key!r}")
    # Normalize the joined path and ensure it is still within the prefix.
    joined = posixpath.normpath(prefix + key)
    if joined == "." or joined.startswith("../") or "/../" in joined or joined == "..":
        raise OssPathError(f"key escapes prefix: prefix={prefix!r} key={key!r}")
    expected_root = posixpath.normpath(prefix.rstrip("/"))
    if not (joined == expected_root or joined.startswith(expected_root + "/")):
        raise OssPathError(f"key escapes prefix: prefix={prefix!r} key={key!r}")
    return joined


class OssClient:
    """Minimal async S3 wrapper exposing put / get / copy with prefix guards."""

    def __init__(
        self,
        *,
        endpoint_url: str,
        bucket: str,
        access_key_id: str,
        access_key_secret: str,
        region_name: str = "us-east-1",
    ) -> None:
        self._endpoint_url = endpoint_url
        self._bucket = bucket
        self._access_key_id = access_key_id
        self._access_key_secret = access_key_secret
        self._region_name = region_name
        self._session = aioboto3.Session()

    def _client_cm(self) -> Any:
        return self._session.client(
            "s3",
            endpoint_url=self._endpoint_url,
            aws_access_key_id=self._access_key_id,
            aws_secret_access_key=self._access_key_secret,
            region_name=self._region_name,
        )

    @property
    def bucket(self) -> str:
        return self._bucket

    async def put(self, prefix: str, key: str, body: bytes) -> str:
        """Upload ``body`` under ``prefix + key``. Returns the absolute object key."""
        absolute_key = _normalize_key(prefix, key)
        async with self._client_cm() as s3:
            await s3.put_object(Bucket=self._bucket, Key=absolute_key, Body=body)
        return absolute_key

    async def get(self, prefix: str, key: str) -> bytes:
        absolute_key = _normalize_key(prefix, key)
        async with self._client_cm() as s3:
            response = await s3.get_object(Bucket=self._bucket, Key=absolute_key)
            stream = response["Body"]
            data: bytes = await stream.read()
            return data

    async def server_side_copy(self, src_prefix: str, dst_prefix: str, key: str) -> str:
        """Copy an object from ``src_prefix + key`` to ``dst_prefix + key`` server-side.

        Useful for parent-version artifact inheritance (no caller wired yet —
        kept available for downstream proposals).
        """
        src_absolute = _normalize_key(src_prefix, key)
        dst_absolute = _normalize_key(dst_prefix, key)
        async with self._client_cm() as s3:
            await s3.copy_object(
                Bucket=self._bucket,
                Key=dst_absolute,
                CopySource={"Bucket": self._bucket, "Key": src_absolute},
            )
        return dst_absolute

    async def ensure_bucket(self) -> None:
        """Create the bucket if it does not exist (idempotent, dev convenience)."""
        async with self._client_cm() as s3:
            try:
                await s3.head_bucket(Bucket=self._bucket)
            except Exception:  # noqa: BLE001 - botocore raises a wide range
                await s3.create_bucket(Bucket=self._bucket)
