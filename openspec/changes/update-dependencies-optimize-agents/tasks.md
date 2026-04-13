## 1. Upgrade Dependencies

- [x] 1.1 Bump `deepagents` version from `>=0.4.11` to `>=0.5.2` in `pyproject.toml`
- [x] 1.2 Bump all other dependencies (`dashscope`, `ddgs`, `docker`, `duckduckgo-search`, `langchain`, `langchain-community`, `langgraph-checkpoint-sqlite`, `python-dotenv`) to latest compatible versions in `pyproject.toml`
- [x] 1.3 Run `uv sync` and resolve any dependency conflicts

## 2. Verify and Fix Import Paths

- [x] 2.1 Verify `deepagents` imports in `main.py` (`create_deep_agent`, `BaseSandbox`, `FilesystemMiddleware`, `CompiledSubAgent`, etc.) are valid with 0.5.x
- [x] 2.2 Verify `langchain`/`langchain-community` imports in `utils.py` (`ChatTongyi`), `agents/web_research.py` (`DuckDuckGoSearchRun`), and `agents/html_report.py`
- [x] 2.3 Verify `deepagents.middleware` imports in `middlewares/datetime_middleware.py` (`AgentMiddleware`, `append_to_system_message`, `ModelRequest`, `ModelResponse`)
- [x] 2.4 Fix any broken import paths found in 2.1–2.3 (no fixes needed — all imports valid)

## 3. Update DockerSandbox for 0.5.x BaseSandbox Protocol

- [x] 3.1 Check `deepagents.backends.sandbox.BaseSandbox` for any renamed or new abstract methods in 0.5.x (no changes — same 3 abstract methods + id property)
- [x] 3.2 Update `DockerSandbox` in `sandbox/docker_sandbox.py` to implement any new/renamed methods (no changes needed — already compliant)
- [x] 3.3 Verify `ExecuteResponse`, `FileUploadResponse`, `FileDownloadResponse` from `deepagents.backends.protocol` are unchanged or update accordingly (unchanged)

## 4. Update DatetimeMiddleware for 0.5.x AgentMiddleware API

- [x] 4.1 Check `deepagents.graph.AgentMiddleware` for any changed method signatures in 0.5.x (signatures unchanged)
- [x] 4.2 Update `DatetimeMiddleware` in `middlewares/datetime_middleware.py` if method signatures have changed (no changes needed)
- [x] 4.3 Verify `append_to_system_message` utility is still available and functioning (confirmed)

## 5. Add Filesystem Permissions to Main Agent

- [x] ~~5.1 Import `FilesystemPermission` from `deepagents` in `main.py`~~ **Reverted**: `FilesystemPermission` does not support sandbox backends with command execution (`SandboxBackendProtocol`). Docker sandbox already provides isolation.
- [x] ~~5.2 Add `permissions` parameter to `create_deep_agent()` call restricting writes to `/workspace/**`~~ **Reverted**: Same as 5.1.

## 6. Validate End-to-End Pipeline

- [x] 6.1 Run the pipeline with `uv run python main.py "test topic"` and verify it completes without import errors
- [x] 6.2 Verify the web-research subagent performs searches successfully
- [x] 6.3 Verify the html-report subagent generates and writes `report.html`
- [x] 6.4 Verify `report.html` is downloaded to the host and the shareable URL is printed
