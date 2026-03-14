## Why

Currently the html-report subagent writes files directly to the host filesystem, and agent tool execution is not isolated. Introducing a Docker sandbox as a deepagents `backend` gives the entire pipeline an isolated, reproducible execution environment — all filesystem tools (`write_file`, `read_file`, etc.) and shell commands route through the container, preventing host pollution and enabling safe agent-driven file operations.

## What Changes

- Add a `DockerSandboxProvider` class that provisions a `DockerSandbox` container using `python:3.14.3-bookworm` image with `/workspace` as the working directory.
- Create a `DockerSandbox` class that extends `deepagents.backends.sandbox.BaseSandbox`, implementing `execute()` via Docker `exec_run` so all deepagents filesystem tools run inside the container.
- Modify the `main-agent` to instantiate the `DockerSandboxProvider`, create a sandbox, and pass it as `backend` to `create_deep_agent` and to the `html-report` subagent.
- Modify `build_html_report_subagent()` to accept a sandbox parameter and wire `FilesystemMiddleware(backend=sandbox)` so the agent uses deepagents's built-in `write_file` tool routed through the sandbox.
- Remove the custom `write_report_html` tool from `html_report.py` — replaced by deepagents's built-in filesystem tools.

## Capabilities

### New Capabilities

- `docker-sandbox-provider`: Creates and manages `DockerSandbox` instances backed by Docker containers. `DockerSandbox` extends `BaseSandbox`, implementing `execute()` via `exec_run`. Default image: `python:3.14.3-bookworm`, default working directory: `/workspace`.

### Modified Capabilities

- `main-agent`: Now creates a `DockerSandbox` via `DockerSandboxProvider`, passes it as `backend` to `create_deep_agent`, and passes it to the `html-report` subagent so all agents share the same sandbox environment.
- `html-report-agent`: Now accepts a `DockerSandbox` instance; uses `FilesystemMiddleware(backend=sandbox)` so deepagents's built-in `write_file` tool writes into the sandbox's `/workspace` instead of the host filesystem.

## Impact

- New dependency: `docker` Python SDK (`docker` package).
- `agents/html_report.py`: `build_html_report_subagent()` signature changes to accept a `sandbox: DockerSandbox` argument; custom `write_report_html` tool removed.
- `main.py`: Must create sandbox before building agents, pass it as `backend` to `create_deep_agent`, and tear it down in a `finally` block.
- Breaking change for callers of `build_html_report_subagent()` — signature now requires a sandbox argument.
