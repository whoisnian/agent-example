## Context

The current pipeline writes `report.html` directly to the host filesystem via a custom `write_report_html` tool in `agents/html_report.py`. All filesystem operations happen on the host, which is unsafe and non-reproducible. The deepagents framework has a `backend` abstraction (`BackendProtocol` / `SandboxBackendProtocol`) that routes all built-in filesystem tools (`write_file`, `read_file`, `edit_file`, `glob`, `grep`) and shell execution through a pluggable backend. `deepagents.backends.sandbox.BaseSandbox` provides a fully functional `BackendProtocol` implementation where every operation is delegated to an abstract `execute()` method — making it straightforward to back agent tools with any container runtime.

The project uses `deepagents`, `langchain`, `asyncio`, and the DashScope API. Docker containers are managed synchronously via the `docker` Python SDK.

## Goals / Non-Goals

**Goals:**
- Introduce `DockerSandboxProvider` and `DockerSandbox` in a new `sandbox/` module.
- `DockerSandbox` extends `deepagents.backends.sandbox.BaseSandbox`, implementing `execute()` via `container.exec_run()` so all deepagents filesystem tools and shell commands run inside the Docker container.
- `DockerSandboxProvider.create()` starts a `python:3.14.3-bookworm` container with `working_dir="/workspace"` and returns a `DockerSandbox` instance.
- `main()` passes the sandbox as `backend` to `create_deep_agent`, giving the main agent and its subagent infrastructure a shared sandboxed filesystem.
- `build_html_report_subagent(sandbox)` wires `FilesystemMiddleware(backend=sandbox)` into the html-report agent so it uses deepagents's built-in `write_file` tool routed through the sandbox — the custom `write_report_html` tool is removed.
- `main()` stops the sandbox in a `finally` block after the pipeline ends.

**Non-Goals:**
- No container image building or Dockerfile management.
- No persistent sandbox volumes or named containers.
- No async Docker client — the `docker` SDK is used synchronously.

## Decisions

### Decision: `DockerSandbox` extends `BaseSandbox` rather than implementing `BackendProtocol` directly

**Choice**: Subclass `deepagents.backends.sandbox.BaseSandbox` and implement only `execute()`, `id`, `upload_files()`, and `download_files()`.

**Rationale**: `BaseSandbox` already implements all `SandboxBackendProtocol` methods (`write()`, `read()`, `edit()`, `grep_raw()`, `glob_info()`, `ls_info()`) by delegating to `execute()`. This means all deepagents filesystem tools work automatically as soon as `execute()` is implemented with Docker `exec_run`. Zero code duplication of the tool-command layer.

**Alternative considered**: Implementing `BackendProtocol` from scratch with `put_archive` for writes. Rejected — would bypass the battle-tested `BaseSandbox` command templates and require reimplementing every filesystem operation.

---

### Decision: `execute()` implemented via `container.exec_run()`

**Choice**: `DockerSandbox.execute(command)` calls `self._container.exec_run(["sh", "-c", command], workdir="/workspace")` and returns `ExecuteResponse`.

**Rationale**: `exec_run` routes commands into the running container. Combined with `BaseSandbox`'s command templates (which use safe base64-encoded payloads), this is free of shell injection risk. The container's `working_dir` is `/workspace`, so relative paths in agent tool calls resolve correctly.

---

### Decision: Sandbox passed as `backend` to both `create_deep_agent` and `build_html_report_subagent`

**Choice**: `main()` passes `sandbox` as `backend=sandbox` to `create_deep_agent`. `build_html_report_subagent(sandbox)` passes `FilesystemMiddleware(backend=sandbox)` as middleware to `create_agent`.

**Rationale**: `create_deep_agent` forwards `backend` to `FilesystemMiddleware` for itself and all `SubAgent`-spec subagents. `build_html_report_subagent` uses a `CompiledSubAgent` (pre-compiled), so it must explicitly receive and wire `FilesystemMiddleware(backend=sandbox)` itself. Both agents then share the same container filesystem.

**Note**: The custom `write_report_html` tool is entirely removed — the deepagents built-in `write_file` (provided by `FilesystemMiddleware`) replaces it.

---

### Decision: Sandbox lifecycle managed in `main()` with `try/finally`

**Choice**: `main()` calls `provider.create()` before the pipeline, then calls `sandbox.stop()` in `finally`.

**Rationale**: Guarantees cleanup even on pipeline errors. Lifecycle is visible at the top level.

## Risks / Trade-offs

- **Docker not available at runtime** → `DockerSandboxProvider.create()` raises `RuntimeError` with a descriptive message if the Docker daemon is unreachable.
- **`python:3.14.3-bookworm` image not present** → First run requires a Docker pull. The SDK pulls automatically; document in README.
- **Blocking Docker SDK in async event loop** → `exec_run` blocks during each tool call. Mitigation: acceptable for the current use case; can be wrapped in `asyncio.to_thread()` in a follow-up if latency becomes a concern.
- **Container not stopped on SIGKILL** → `finally` only runs for graceful exits. Label containers with a known prefix (e.g., `deepagents-sandbox-`) for easy manual cleanup.
