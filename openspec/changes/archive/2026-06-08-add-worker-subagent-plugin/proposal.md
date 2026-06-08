## Why

The worker's plugin system already supports `kind: subagent` end-to-end — the loader scans `worker/plugins/subagent/`, the schema validates it, and `PluginRegistry.get_subagent()` exists — but **no subagent plugin has ever been authored**: `worker/plugins/subagent/` holds only an empty `__init__.py`. Meanwhile `docs/ARCHITECTURE.md §8.1` models the planner / executor / critic as subagents, yet today their role prompts are hardcoded as module constants in `agents/loop.py` (`_PLANNER_INSTRUCTION` / `_EXECUTOR_INSTRUCTION` / `_CRITIC_INSTRUCTION`) and `AgentSpec.subagents` is a declared-but-unused field. The subagent extension point is therefore inert. This change makes it real — the second of the two plugin kinds the MVP is meant to deliver (Skill stays Post-MVP).

## What Changes

- Define a **subagent plugin contract**: a subagent plugin is `worker/plugins/subagent/<role>/{plugin.yaml, prompt.md, handler.py}`, where `plugin.yaml` declares `kind: subagent` and its `entrypoint` resolves to a builder returning a `SubagentDefinition{name, instruction}` (the instruction loaded from the sibling `prompt.md`). This mirrors the existing tool-plugin shape (`plugin.yaml` + `handler.py`) and reuses the existing loader/registry unchanged.
- **Bundle the planner / executor / critic as the first three subagent plugins**, carrying the role prompts that currently live as constants in `loop.py`.
- **Wire the step loop to source its role instructions from the registered subagent plugins** instead of the hardcoded constants. The agent resolves its declared subagents at build time (validated at startup, like tools) and passes their instructions into `run_agent_loop`. The constants remain only as a documented in-code default/fallback for direct callers, so existing `run_agent_loop(...)` and `build_*_agent(...)` call sites keep working unchanged.
- **Extend `AgentSpec` with `subagent_names`** (the declared role plugins) and **extend agent-spec validation** so an agent that names a missing subagent fails startup non-zero — exactly as it already does for an unknown tool.
- Add a `SubagentDefinition` contract type + a `resolve_subagents(registry, names)` helper, plus unit tests for the new plugins, the resolver, validation, and the loop sourcing prompts from plugins.
- **Out of scope (Post-MVP / not now):** per-subagent `model_key`/`tool_names` (the §8.3 richer form — MVP role subagents share the parent agent's model and tools); the `skill` plugin kind; switching the loop to `deepagents.create_deep_agent`; any new task type or new role beyond planner/executor/critic.

## Capabilities

### New Capabilities
- `worker-subagent-plugins`: the subagent plugin contract (`plugin.yaml kind: subagent` + `prompt.md` + an `entrypoint` returning a `SubagentDefinition`), the `SubagentDefinition` type and `resolve_subagents` helper, and the three bundled role plugins (planner / executor / critic) discovered by the existing loader.

### Modified Capabilities
- `worker-agent-orchestration`: the "Planner / Executor / Critic Step Loop" now sources each role's instruction from a registered subagent plugin (with the in-code constants as a default), and the "Agent Registry and Contract" validation extends to the agent's declared `subagent_names` (a missing subagent fails startup), and `AgentSpec` gains `subagent_names`.

## Impact

- **Code (worker):** new `worker/plugins/subagent/{planner,executor,critic}/{plugin.yaml,prompt.md,handler.py}`; new `worker/plugins/subagent_spec.py` (`SubagentDefinition` + `prompt.md` loader); new `worker/agents/subagent.py` (or addition to `agents/`) for `resolve_subagents`; `agents/base.py` (`AgentSpec.subagent_names`, `LoopAgent` passes role instructions); `agents/loop.py` (`run_agent_loop` accepts a `roles` mapping, default = current constants); `agents/registry.py` (`validate_spec` checks subagent names); `agents/code_agent.py` + `research_agent.py` (declare `subagent_names`); `agents/__init__.py` (`build_agent_registry` resolves role defs from the registry and threads them into the builders).
- **No infra/schema changes:** the plugin loader, `PluginManifest` schema, MQ, DB, and OSS are untouched; the change is internal to the worker's agent assembly.
- **Behavior:** the planner/executor/critic loop runs identically (same prompts, same per-step checkpoint/event cadence); only the *source* of the role prompts moves from constants to plugins, and a misdeclared subagent now fails fast at startup.
