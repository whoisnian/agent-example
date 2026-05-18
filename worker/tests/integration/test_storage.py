"""Integration test for OSS round-trip against MinIO."""

from __future__ import annotations

import pytest
from worker.core.storage import OssClient

pytestmark = pytest.mark.integration


async def test_put_get_roundtrip(minio_container) -> None:  # type: ignore[no-untyped-def]
    endpoint = (
        f"http://{minio_container.get_container_host_ip()}:{minio_container.get_exposed_port(9000)}"
    )
    client = OssClient(
        endpoint_url=endpoint,
        bucket="worker-bucket",
        access_key_id=minio_container.access_key,
        access_key_secret=minio_container.secret_key,
    )
    await client.ensure_bucket()
    abs_key = await client.put("tenant/task/v1/", "checkpoints/3.bin", b"hello-world")
    assert abs_key == "tenant/task/v1/checkpoints/3.bin"
    body = await client.get("tenant/task/v1/", "checkpoints/3.bin")
    assert body == b"hello-world"
