## 1. Dependencies & Module Setup

- [x] 1.1 Add `docker` package to project dependencies in `pyproject.toml`
- [x] 1.2 Create `sandbox/` package: `sandbox/__init__.py` and `sandbox/docker_sandbox.py`

## 2. Implement DockerSandbox and DockerSandboxProvider

- [x] 2.1 Implement `DockerSandbox` class extending `deepagents.backends.sandbox.BaseSandbox` in `sandbox/docker_sandbox.py`
- [x] 2.2 Implement `DockerSandbox.execute(command)` using `container.exec_run(["sh", "-c", command], workdir="/workspace")` and return `ExecuteResponse`
- [x] 2.3 Implement `DockerSandbox.id` property returning a unique identifier for the container
- [x] 2.4 Implement `DockerSandbox.upload_files()` and `DockerSandbox.download_files()` using Docker SDK `put_archive` / `get_archive`
- [x] 2.5 Implement `DockerSandbox.stop()` method that stops and removes the container (idempotent)
- [x] 2.6 Implement `DockerSandboxProvider` class with `create()` method that starts a `python:3.14.3-bookworm` container with `working_dir="/workspace"` and returns a `DockerSandbox`
- [x] 2.7 Add error handling in `DockerSandboxProvider.create()` to raise `RuntimeError` with a descriptive message when the Docker daemon is unavailable
- [x] 2.8 Export `DockerSandboxProvider` and `DockerSandbox` from `sandbox/__init__.py`

## 3. Update html-report-agent

- [x] 3.1 Change `build_html_report_subagent()` signature to accept `sandbox: DockerSandbox` as a required parameter
- [x] 3.2 Remove the custom `write_report_html` tool; add `FilesystemMiddleware(backend=sandbox)` as middleware to `create_agent`
- [x] 3.3 Update the system prompt in `html_report.py` to instruct the agent to use the `write_file` tool with path `/workspace/report.html`

## 4. Update main-agent

- [x] 4.1 Import `DockerSandboxProvider` from `sandbox` in `main.py`
- [x] 4.2 In `main()`, create a sandbox via `DockerSandboxProvider().create()` before building subagents
- [x] 4.3 Pass `backend=sandbox` to `create_deep_agent` so main agent filesystem tools use the sandbox
- [x] 4.4 Pass the sandbox to `build_html_report_subagent(sandbox)`
- [x] 4.5 After the pipeline loop, call `sandbox.download_files(["/workspace/report.html"])`, write the content bytes to `report.html` on the host, and print the absolute local path (or print a warning if the download response has an error)
- [x] 4.6 Wrap the pipeline execution in `try/finally` to call `sandbox.stop()` on completion or error
