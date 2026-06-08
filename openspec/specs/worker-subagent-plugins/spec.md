# worker-subagent-plugins Specification

## Purpose

The worker's subagent plugin capability: the contract for authoring `kind: subagent` plugins (a `plugin.yaml` + `prompt.md` + an `entrypoint` returning a `SubagentDefinition`), the `SubagentDefinition` type and `resolve_subagents` helper, and the three bundled role plugins (planner / executor / critic) discovered by the existing Plugin Loader and consumed by the agent step loop. Realizes the `subagent` half of the worker's two MVP plugin kinds (`tool` + `subagent`; `skill` is Post-MVP). Established by archiving change `add-worker-subagent-plugin`.

## Requirements

### Requirement: Subagent Plugin Contract

The worker SHALL support `subagent` plugins authored under `worker/plugins/subagent/<name>/` and discovered by the existing Plugin Loader (no loader change). A subagent plugin MUST consist of a `plugin.yaml` declaring `kind: subagent` (conforming to the shared `PluginManifest` schema — `name`, `version`, `entrypoint`, optional `applies_to`/`resources`), a sibling `prompt.md` holding the role instruction text, and an `entrypoint` (`module:callable`) that resolves to a zero-argument builder returning a `SubagentDefinition`.

`SubagentDefinition` MUST carry at least a `name` and an `instruction` (the role prompt). For MVP a subagent is prompt-only: it does NOT carry its own model or tool set (the parent agent's model and tools apply). The builder MUST load its instruction from the plugin's own `prompt.md` so the prompt is editable as data, not code. Resolution MUST stay lazy (the entrypoint is imported on first use, not at scan time), consistent with tool plugins.

#### Scenario: Subagent plugin is discovered and resolves to a definition
- **WHEN** the worker starts with a valid `worker/plugins/subagent/planner/plugin.yaml` (`kind: subagent`) plus its `prompt.md` and `handler.py`
- **THEN** `PluginRegistry.get_subagent("planner")` MUST return a record whose entrypoint resolves to a builder, and calling the builder MUST return a `SubagentDefinition` whose `name` is `"planner"` and whose `instruction` equals the `prompt.md` contents

#### Scenario: Subagent declaring the wrong kind for its directory aborts startup
- **WHEN** a `plugin.yaml` under `subagent/` declares `kind` other than `subagent`
- **THEN** startup MUST fail (the existing loader's kind/directory check), naming the offending file

### Requirement: Bundled Planner, Executor, and Critic Subagents

The worker SHALL bundle three subagent plugins — `planner`, `executor`, and `critic` — each with its own `prompt.md` carrying the role instruction that drives the corresponding phase of the step loop. These plugins MUST be registered by the standard loader at startup (no special-casing) and MUST be resolvable via `PluginRegistry.get_subagent(<role>)`.

#### Scenario: The three role subagents load by default
- **WHEN** the worker starts with the bundled plugins present
- **THEN** `get_subagent("planner")`, `get_subagent("executor")`, and `get_subagent("critic")` MUST each return a registered record that resolves to a `SubagentDefinition` for that role

### Requirement: Subagent Resolution Helper

The worker SHALL provide a `resolve_subagents(registry, names)` helper that maps a sequence of subagent names to their resolved `SubagentDefinition`s by looking each up in the `PluginRegistry` and invoking its builder. A requested name with no registered subagent MUST raise an error (not silently skip), so a misconfiguration surfaces at build/startup rather than at first message.

#### Scenario: Resolving a known set returns their definitions
- **WHEN** `resolve_subagents(registry, ("planner", "executor", "critic"))` is called with the bundled plugins registered
- **THEN** it MUST return a mapping from each name to its `SubagentDefinition`

#### Scenario: Resolving an unknown subagent raises
- **WHEN** `resolve_subagents(registry, ("nonexistent",))` is called and no such subagent is registered
- **THEN** it MUST raise an error naming the missing subagent rather than returning an empty or partial mapping
