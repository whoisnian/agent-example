## Context

The project is a deep agent pipeline built on `deepagents 0.4.11`, `langchain`, and `langgraph`. It uses a Docker-backed sandbox (`DockerSandbox` extending `BaseSandbox`) with web research and HTML report subagents. The codebase currently uses `create_deep_agent` for the main orchestrator and `create_agent` for subagents, with custom middleware for datetime injection and filesystem access. Running on Python 3.14, the current Pydantic V1 compatibility layer in `langchain_core` produces deprecation warnings.

`deepagents` has progressed to 0.5.2 with significant improvements: async subagents, filesystem permissions, improved backend protocol with better error propagation, and deprecated backend factories. Other dependencies also have newer versions available.

## Goals / Non-Goals

**Goals:**
- Update all dependencies in `pyproject.toml` to latest compatible versions
- Ensure full compatibility with `deepagents` 0.5.x API changes
- Adopt new `deepagents` 0.5.x features where they provide clear value (permissions, improved model initialization)
- Fix or suppress the Pydantic V1 deprecation warning on Python 3.14
- Verify `DockerSandbox` conforms to updated `BaseSandbox` protocol
- Maintain full pipeline functionality: research → report → share

**Non-Goals:**
- Migrating to async subagents (requires LangSmith Deployment, out of scope)
- Switching to a different sandbox provider (Modal, Daytona, etc.)
- Adding multi-modal support to the pipeline
- Changing the overall agent architecture or adding new subagents
- Migrating from `ChatTongyi` to a different model provider

## Decisions

### 1. Upgrade strategy: all-at-once vs incremental

**Decision**: All-at-once upgrade with `uv add` for each dependency, followed by end-to-end validation.

**Rationale**: The dependencies are tightly coupled (deepagents depends on langchain, which depends on langchain-core, etc.). Incremental upgrades would require multiple rounds of testing. A single coordinated upgrade is cleaner and avoids transitive dependency conflicts.

**Alternative**: Incremental upgrade starting with deepagents, then langchain ecosystem, then other packages. Rejected because deepagents 0.5.x already pins compatible langchain versions.

### 2. Model string format adoption

**Decision**: Keep using `ChatTongyi` directly in `utils.py` rather than adopting the `provider:model` string format.

**Rationale**: The `provider:model` format works via `init_chat_model` which may not support DashScope/Tongyi out of the box. The current explicit `ChatTongyi` instantiation gives full control over the API key and model name. No benefit to changing this.

### 3. Filesystem permissions

**Decision**: ~~Add `FilesystemPermission` rules to the main agent to restrict writes to `/workspace/`.~~ **Revised**: Cannot use `FilesystemPermission` with our `DockerSandbox` because the permission middleware does not yet support backends with command execution (`SandboxBackendProtocol`). The Docker sandbox already provides isolation, so this is acceptable.

**Rationale**: During implementation, `_PermissionMiddleware` raised `NotImplementedError: _PermissionMiddleware does not yet support backends with command execution`. This is a known limitation in deepagents 0.5.2. Since the Docker sandbox itself provides file isolation, the security benefit is already achieved at the container level.

### 4. Backend factory deprecation

**Decision**: Verify that we do not use deprecated backend factory patterns. Currently, we pass the `DockerSandbox` instance directly as `backend=`, which is the recommended approach.

**Rationale**: The 0.5.x changelog deprecates `backend=(lambda rt: StateBackend(rt))` style factory functions. Our code already passes the sandbox instance directly, so no change needed here.

### 5. Handling renamed backend methods

**Decision**: Audit `DockerSandbox` for any methods renamed in the 0.5.x `BaseSandbox` protocol and update accordingly.

**Rationale**: The 0.5.0 changelog mentions `feat(sdk): rename backend methods (#1907)`. Our custom `DockerSandbox` extends `BaseSandbox` and must comply with the updated protocol.

## Risks / Trade-offs

- **[Breaking API changes]** → Mitigated by reviewing the full 0.4.11→0.5.2 changelog and running the pipeline end-to-end after upgrade.
- **[Transitive dependency conflicts]** → Mitigated by using `uv` which handles resolution well. Lock file will be regenerated.
- **[DashScope/Tongyi compatibility]** → Risk that newer langchain-community changes the `ChatTongyi` API. Mitigated by checking import paths and constructor signature after upgrade.
- **[Docker SDK compatibility]** → The `docker` package is relatively stable; risk is low but we verify container operations still work.
- **[Pydantic V1 warning may persist]** → If the warning comes from transitive dependencies, it may not be fully fixable. Acceptable to suppress if needed.
