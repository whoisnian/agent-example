# AGENTS.md

## Project structure
- `openspec/` — Canonical capability specifications and archived change proposals
- `sandbox/` — The `DockerSandbox` implementation for running agent tools in a containerized environment
- `agents/` — Subagent builder functions for modular capabilities
- `skills/` — DeepAgents Skills defined via `SKILL.md` files, which are uploaded to the sandbox and read at runtime
- `utils.py` — Shared utility functions
- `main.py` — Entry point for the main agent that orchestrates the pipeline

## Development guidelines
- Follow **PEP 8** style guide
- Use `async`/`await` throughout the main pipeline
- Agent system prompts live as module-level `_SYSTEM_PROMPT` constants in each agent file
- Subagent builder functions follow the pattern `build_<name>_subagent(...)` => `CompiledSubAgent`
- Use `uv` commands to manage dependencies and run scripts:
  - `uv add` to add a new dependency (e.g., `uv add langchain`)
  - `uv run` to run python scripts (e.g., `uv run python -c "print('hello')"`)
