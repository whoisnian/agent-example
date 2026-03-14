## Context

The deep-agent pipeline currently ends after downloading `report.html` to the host. The main agent uses a Docker sandbox as its backend, which means it has access to the `execute` tool and deepagents' `SkillsMiddleware`. The sandbox runs inside a Docker container that has internet access and standard UNIX tools including `curl`.

DeepAgents' `SkillsMiddleware` supports a `skills` parameter on `create_deep_agent()` that accepts a list of source paths in the backend. At runtime, the agent reads and follows `SKILL.md` files found under those paths. The share-html feature will deliver skill instructions this way rather than embedding the curl command in the system prompt.

## Goals / Non-Goals

**Goals:**
- Implement the share-html step as a proper DeepAgents skill (`SKILL.md`) so the main agent reads it from the sandbox at runtime
- Upload the `SKILL.md` to the sandbox before starting the pipeline and wire it via `skills=["/workspace/skills/project/"]`
- The main agent's system prompt adds a step to invoke the share-html skill — without specifying implementation details
- Document the new requirements in `openspec/specs/main-agent/spec.md`

**Non-Goals:**
- Error recovery / retry logic for upload failures (a best-effort single attempt is sufficient)
- Configurable upload endpoints (hardcoded in the `SKILL.md`)
- Downloading the report to the host changes are out of scope (already handled in `main.py`)

## Decisions

### Decision 1: Implement share-html as a DeepAgents `SKILL.md` skill

**Chosen**: Create `skills/share-html/SKILL.md` with YAML frontmatter and instructions. Before creating the main agent, upload this file to the sandbox at `/workspace/skills/share-html/SKILL.md` via `sandbox.upload_files()`. Pass `skills=["/workspace/skills/"]` to `create_deep_agent()`.

**Rationale**: Skills are the framework's designed extension point for this pattern — they let the agent discover and follow instructions at runtime without hardcoding them in the system prompt. This keeps `main.py` clean and the skill self-contained and independently maintainable.

**Alternative considered**: Embed the exact curl command in the system prompt. Rejected per user feedback — the skill detail should not appear in the system prompt.

### Decision 2: System prompt references the skill by name only

**Chosen**: Add a step to `_SYSTEM_PROMPT` saying "use the `share-html` skill to upload and share the report", without including the command.

**Rationale**: The `SkillsMiddleware` injects skill metadata into the context automatically. The system prompt only needs to tell the agent when to use the skill; the skill itself provides the how.

### Decision 3: Skill file stored in `skills/` at project root, uploaded at runtime

**Chosen**: Keep the `SKILL.md` under `skills/share-html/SKILL.md` in the repo. In `main.py`, read it and call `sandbox.upload_files([(path, content)])` before instantiating the agent.

**Rationale**: Keeps the skill versioned alongside the rest of the agent code. No need for a separate build step.

### Decision 4: No spec changes for html-report-agent or web-research-agent

The share action is owned entirely by the main agent and requires no changes to the subagents.

## Risks / Trade-offs

- **[Risk] Network unreachable from sandbox** → The curl command in the skill includes `|| echo "Failed to upload file."` so the agent handles failure gracefully without crashing.
- **[Risk] Agent skips the share step** → Mitigated by a clear numbered step in the system prompt.
- **[Risk] File name collisions on the sharing server** → Using `date +%Y%m%d.%H%M%S` gives second-level uniqueness; acceptable for a best-effort sharing tool.
- **[Trade-off] Skill file must be uploaded before agent starts** → Adds a small upfront upload step, but this is negligible and consistent with how sandbox files are already managed.
