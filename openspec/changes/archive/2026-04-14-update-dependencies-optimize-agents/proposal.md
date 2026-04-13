## Why

The project depends on `deepagents>=0.4.11` which is now two major minor versions behind the latest `0.5.2`. The 0.5.x line introduces async subagents, multi-modal support, filesystem permissions, improved backend protocol, and deprecates backend factories. Other core dependencies (`langchain`, `langchain-community`, `langgraph-checkpoint-sqlite`) also have newer releases. Additionally, `langchain_core` Pydantic V1 compatibility throws warnings on Python 3.14. Upgrading now ensures we benefit from bug fixes, security patches, and new capabilities while the migration delta is still manageable.

## What Changes

- **BREAKING**: Bump `deepagents` from `>=0.4.11` to `>=0.5.2`. This may include backend protocol changes (new file format for State/Store backends, deprecated backend factories).
- Bump all other dependencies (`dashscope`, `ddgs`, `docker`, `duckduckgo-search`, `langchain`, `langchain-community`, `langgraph-checkpoint-sqlite`, `python-dotenv`) to their latest compatible versions.
- Replace deprecated `create_agent` import path if changed in 0.5.x.
- Adopt `create_deep_agent` for subagents where it improves capability (e.g., filesystem access, improved summarization).
- Add `FilesystemPermission` rules to the main agent to restrict sandbox writes to `/workspace/`.
- Use the new `model` string format (`provider:model`) for cleaner model initialization where applicable.
- Resolve the Pydantic V1 deprecation warning on Python 3.14 by ensuring compatible dependency versions.
- Verify and fix any breaking changes in `BaseSandbox` protocol (renamed backend methods introduced in 0.5.0).

## Capabilities

### New Capabilities
- `dependency-upgrade`: Covers the version bump strategy, compatibility matrix, and validation of all project dependencies against the latest releases.

### Modified Capabilities
- `main-agent`: Update to use new `deepagents` 0.5.x API (model string format, permissions parameter, any renamed parameters).
- `web-research-agent`: Ensure compatibility with updated `langchain-community` and `deepagents` 0.5.x `create_agent` API.
- `html-report-agent`: Ensure compatibility with updated `FilesystemMiddleware` and `deepagents` 0.5.x middleware API.
- `docker-sandbox-provider`: Verify `BaseSandbox` protocol compliance with 0.5.x (renamed backend methods, improved error propagation).
- `datetime-middleware`: Verify middleware API compat with `deepagents` 0.5.x `AgentMiddleware` changes.

## Impact

- **Dependencies**: All pinned versions in `pyproject.toml` will be updated.
- **Code**: `main.py`, `agents/web_research.py`, `agents/html_report.py`, `utils.py`, `sandbox/docker_sandbox.py`, `middlewares/datetime_middleware.py`, `context.py` may need changes.
- **APIs**: Import paths may change if `deepagents` renamed or moved modules between 0.4 and 0.5.
- **Testing**: Full end-to-end run required after upgrade to verify the pipeline still produces correct reports.
