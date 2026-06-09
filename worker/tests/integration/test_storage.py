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


async def test_list_keys_and_copy(minio_container) -> None:  # type: ignore[no-untyped-def]
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

    src = "tenant/task/parent/"
    await client.put(src, "code/main.py", b"print(1)")
    await client.put(src, "README.md", b"# parent")

    # list_keys returns relative keys + sizes (prefix stripped).
    listed = sorted(await client.list_keys(src))
    assert listed == [("README.md", 8), ("code/main.py", 8)]

    # server_side_copy into a child prefix round-trips the bytes.
    dst = "tenant/task/child/"
    dst_key = await client.server_side_copy(src, dst, "code/main.py")
    assert dst_key == "tenant/task/child/code/main.py"
    assert await client.get(dst, "code/main.py") == b"print(1)"

    # Empty prefix lists nothing.
    assert await client.list_keys("tenant/task/empty/") == []
