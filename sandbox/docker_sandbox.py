"""Docker-backed sandbox for deepagents."""

from __future__ import annotations

import io
import tarfile
from typing import TYPE_CHECKING

import docker
import docker.errors

from deepagents.backends.protocol import (
    ExecuteResponse,
    FileDownloadResponse,
    FileUploadResponse,
)
from deepagents.backends.sandbox import BaseSandbox

if TYPE_CHECKING:
    import docker.models.containers

_DEFAULT_IMAGE = "python:3.14.3-bookworm"
_WORKING_DIR = "/workspace"


class DockerSandbox(BaseSandbox):
    """A deepagents sandbox backed by a Docker container.

    All filesystem tools and shell commands are routed through the container
    via ``execute()``, which delegates to ``container.exec_run()``.
    """

    def __init__(self, container: "docker.models.containers.Container") -> None:
        self._container = container
        self._stopped = False

    # ------------------------------------------------------------------
    # BaseSandbox abstract methods
    # ------------------------------------------------------------------

    def execute(
        self,
        command: str,
        *,
        timeout: int | None = None,
    ) -> ExecuteResponse:
        """Run a shell command inside the container."""
        exit_code, output_bytes = self._container.exec_run(
            ["sh", "-c", command],
            workdir=_WORKING_DIR,
            demux=False,
        )
        output = (output_bytes or b"").decode("utf-8", errors="replace")
        return ExecuteResponse(output=output, exit_code=exit_code)

    @property
    def id(self) -> str:
        """Short container ID."""
        return self._container.short_id

    def upload_files(
        self, files: list[tuple[str, bytes]]
    ) -> list[FileUploadResponse]:
        """Upload files into the container under ``/workspace``."""
        results: list[FileUploadResponse] = []
        for path, data in files:
            archive = io.BytesIO()
            with tarfile.open(fileobj=archive, mode="w") as tar:
                info = tarfile.TarInfo(name=path.lstrip("/"))
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
            archive.seek(0)
            try:
                self._container.put_archive(_WORKING_DIR, archive)
                results.append(FileUploadResponse(path=path))
            except docker.errors.APIError:
                results.append(FileUploadResponse(path=path, error="permission_denied"))
        return results

    def download_files(
        self, paths: list[str]
    ) -> list[FileDownloadResponse]:
        """Download files from the container."""
        results: list[FileDownloadResponse] = []
        for path in paths:
            try:
                chunks, _ = self._container.get_archive(path)
                tar_bytes = b"".join(chunks)
                with tarfile.open(fileobj=io.BytesIO(tar_bytes)) as tar:
                    members = tar.getmembers()
                    if not members:
                        results.append(
                            FileDownloadResponse(path=path, error="file_not_found")
                        )
                        continue
                    member = members[0]
                    f = tar.extractfile(member)
                    content = f.read() if f else b""
                results.append(FileDownloadResponse(path=path, content=content))
            except docker.errors.NotFound:
                results.append(
                    FileDownloadResponse(path=path, error="file_not_found")
                )
            except docker.errors.APIError:
                results.append(
                    FileDownloadResponse(path=path, error="permission_denied")
                )
        return results

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    def stop(self) -> None:
        """Stop and remove the backing container (idempotent)."""
        if self._stopped:
            return
        try:
            self._container.stop(timeout=5)
            self._container.remove(force=True)
        except docker.errors.NotFound:
            pass
        except docker.errors.APIError:
            pass
        self._stopped = True


class DockerSandboxProvider:
    """Factory for :class:`DockerSandbox` instances."""

    def __init__(
        self,
        image: str = _DEFAULT_IMAGE,
        working_dir: str = _WORKING_DIR,
    ) -> None:
        self._image = image
        self._working_dir = working_dir

    def create(self) -> DockerSandbox:
        """Start a Docker container and return a :class:`DockerSandbox`.

        Raises:
            RuntimeError: If the Docker daemon is not reachable.
        """
        try:
            client = docker.from_env()
        except docker.errors.DockerException as exc:
            raise RuntimeError(
                "Docker daemon is unavailable. Make sure Docker is running and "
                "the current user has permission to access the Docker socket."
            ) from exc

        container = client.containers.run(
            self._image,
            command="sleep infinity",
            working_dir=self._working_dir,
            detach=True,
            labels={"managed-by": "deepagents-sandbox"},
        )
        return DockerSandbox(container)
