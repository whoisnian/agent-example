## Context

The worker's plugin machinery (`worker/plugins/`) is generic over three kinds — `tool`, `subagent`, `skill` — and the loader/registry already handle `subagent`:

- `plugins/loader.py` scans `PLUGIN_KIND_DIRS = ("tool", "subagent")` for `*/plugin.yaml`, validates against `PluginManifest`, and registers `(kind, name, version)`.
- `plugins/registry.py` exposes `get_subagent(name, version=None)` and `list_by_task_type`.
- `plugins/schema.py`'s `PluginManifest` requires `kind`, `name`, `version`, `entrypoint` (`module:callable`), with optional `schema`, `permissions`, `applies_to`, `resources`.

The only missing piece is **content + consumption**: `plugins/subagent/` has no plugins, and the agent loop doesn't read any. The planner/executor/critic loop (`agents/loop.py`) hardcodes its role instructions:

```python
_PLANNER_INSTRUCTION  = "You are the PLANNER. ..."
_EXECUTOR_INSTRUCTION = "You are the EXECUTOR. ..."
_CRITIC_INSTRUCTION   = "You are the CRITIC. ..."
```

`AgentSpec` already has a `subagents: tuple[Any, ...]` field (intended for the `deepagents.create_deep_agent` path, which the MVP `LoopAgent` does not take), but it is never populated. The richer-reasoning `build_deep_agent` path passes `spec.subagents` straight to `create_deep_agent`; the MVP loop ignores it.

Constraints carried in:
- AGENTS §3: modifying the worker core loop must go through OpenSpec (this proposal).
- AGENTS §4.2: agents reach infrastructure only via `RunContext`; plugins register at startup from `plugin.yaml`; the loader/registry are the registration path.
- AGENTS §7: do not implement `[Post-MVP]` items (Skill kind; per-subagent model/tools richer form).
- `worker-execution-runtime` "Plugin Loader" already specifies subagent scanning + `get_subagent`; this change does NOT alter the loader — it authors plugins and consumes them.

## Goals / Non-Goals

**Goals:**
- Author the first real subagent plugins (planner / executor / critic) under the existing loader, proving the `kind: subagent` path end-to-end.
- Make the step loop source its role instructions from those plugins, with startup validation that a declared subagent exists.
- Keep the planner/executor/critic loop behavior, per-step checkpoint/event cadence, and the fake-model determinism exactly as today.

**Non-Goals:**
- Per-subagent `model_key` / `tool_names` (the §8.3 richer form). MVP role subagents share the parent agent's model and tool set; `SubagentDefinition` stays prompt-only, with room to grow.
- The `skill` plugin kind (Post-MVP).
- Switching `LoopAgent` to `deepagents.create_deep_agent` (the `build_deep_agent` path stays available but unused; `AgentSpec.subagents` is left intact for it).
- New task types, new roles, or per-task-type role variants (planner/executor/critic are shared across code-gen and research for MVP).

## Decisions

### Decision 1 — Subagent plugin shape: `plugin.yaml` + `prompt.md` + `handler.py`

Each role lives at `worker/plugins/subagent/<role>/`:

```
worker/plugins/subagent/planner/
├── plugin.yaml      # kind: subagent, name: planner, version, entrypoint, applies_to
├── prompt.md        # the role instruction text
└── handler.py       # entrypoint: build() -> SubagentDefinition
```

`plugin.yaml` example:

```yaml
kind: subagent
name: planner
version: 0.1.0
entrypoint: worker.plugins.subagent.planner.handler:build
applies_to:
  task_types: [code-gen, research]   # declarative-only for MVP — subagents are
                                     # selected by explicit subagent_names, not
                                     # list_by_task_type (no routing yet)
```

(`resources` is omitted: a prompt-only subagent has no execution the loader times out — the parent model call carries its own deadline. The field stays available in the schema but is meaningless here, so the bundled role plugins do not set it.)

`handler.py`:

```python
from pathlib import Path
from worker.plugins.subagent_spec import SubagentDefinition, load_prompt

def build() -> SubagentDefinition:
    return SubagentDefinition(name="planner", instruction=load_prompt(__file__))
```

This mirrors the existing **tool**-plugin shape exactly (`worker/plugins/tool/oss_fs/{plugin.yaml,handler.py}`), so it reuses the loader/registry/lazy-resolve path with zero loader changes. The schema's mandatory `entrypoint` is satisfied by `build`, and the prompt lives in a sibling `prompt.md`, loaded by `load_prompt(__file__)`. (ARCHITECTURE §8.2 sketches a `subagents/critic/{plugin.yaml,prompt.md}` layout, but uses **plural** kind dirs; the implemented loader scans **singular** `subagent/` — `PLUGIN_KIND_DIRS = ("tool", "subagent")` — so the bundled plugins follow the singular `tool/`-plugin precedent, and task 5.1 reconciles the doc.)

- **Alternative considered — extend `PluginManifest` with `prompt`/`model`/`tools` fields:** rejected. The manifest is shared with tools and is `extra="forbid", frozen`; adding subagent-only fields muddies the shared schema. The `entrypoint`-returns-a-definition form keeps the manifest untouched and matches how tools already expose a callable.

### Decision 2 — `SubagentDefinition` contract lives in the plugins layer

```python
# worker/plugins/subagent_spec.py
@dataclass(frozen=True, slots=True)
class SubagentDefinition:
    name: str
    instruction: str

def load_prompt(handler_file: str) -> str:
    return (Path(handler_file).resolve().parent / "prompt.md").read_text(encoding="utf-8")
```

Placing it in `worker/plugins/` (not `worker/agents/`) keeps the dependency direction clean: subagent handlers import only from `plugins` (no `plugins → agents` edge), and the agents layer imports it the same direction it already imports tool handlers (`agents → plugins`). Prompt-only for MVP; `model_key`/`tool_names` are deliberately omitted (Decision: Non-Goals) and can be added later without breaking callers.

### Decision 3 — Loop sources role instructions from a `RoleInstructions`, defaulting to today's constants

`run_agent_loop` gains an optional `roles: RoleInstructions` parameter:

```python
@dataclass(frozen=True, slots=True)
class RoleInstructions:
    planner: str
    executor: str
    critic: str

DEFAULT_ROLE_INSTRUCTIONS = RoleInstructions(
    planner=_PLANNER_INSTRUCTION, executor=_EXECUTOR_INSTRUCTION, critic=_CRITIC_INSTRUCTION
)
```

`_plan` / `_execute` / `_critic` read from `roles.*` instead of the module constants. `run_agent_loop(..., roles: RoleInstructions = DEFAULT_ROLE_INSTRUCTIONS)` — so the ~8 existing direct test call sites that don't pass `roles` keep their exact behavior. In production, `LoopAgent` passes the plugin-sourced instructions.

The module constants are retained as the documented default/fallback (used by direct callers and as the canonical text the bundled `prompt.md` files carry). To prevent drift between the constants and the bundled prompts, a unit test asserts the loop actually runs with the plugin-sourced instructions (not the fallback). **This requires a capturing test model**: the existing `tests/support/fake_model.py::scripted_model` returns langchain's `FakeMessagesListChatModel`, whose `_generate` discards the input `messages` — so the role text the loop puts in the `SystemMessage` (`loop.py` `_invoke_json`) is unobservable today. The drift guard therefore adds a small `BaseChatModel` subclass (e.g. `CapturingScriptedChatModel`) to `fake_model.py` that records each invocation's `messages` on a public list before replaying the scripted response; the test passes a custom `RoleInstructions` and asserts each role's text appears in the captured `SystemMessage` content. (A structural check on the constructed `RoleInstructions` at the build boundary is kept as a cheaper complementary assertion, but the capturing model is what proves end-to-end threading.)

- **Alternative considered — delete the constants, require `roles`:** rejected for MVP; it churns ~8 test call sites for no behavior gain. The default-param keeps the diff focused while still making plugins authoritative in production.

### Decision 4 — `AgentSpec.subagent_names` + startup validation

`AgentSpec` gains `subagent_names: tuple[str, ...] = ()`. `code_agent.py` and `research_agent.py` declare `subagent_names=("planner", "executor", "critic")`. `agents/registry.py::validate_spec` extends to:

```python
for name in spec.subagent_names:
    if plugins.get_subagent(name) is None:
        raise AgentValidationError(f"agent {spec.task_type!r}: declared subagent {name!r} is not a registered plugin")
```

So a misdeclared subagent fails startup non-zero, identical to the existing unknown-tool guard. `AgentSpec.subagents` (the deepagents passthrough) is left as-is to avoid disturbing `build_deep_agent`.

### Decision 5 — Resolution happens once in `build_agent_registry`, threaded into the builders

`build_agent_registry` receives the `PluginRegistry` (param `plugins`) but today passes nothing about subagents to the builders (`build_code_agent(model_factory, persistence, settings, metrics)`) — this wiring is **net-new**. It will read each agent spec's `subagent_names` (importing `CODE_AGENT_SPEC` / `RESEARCH_AGENT_SPEC`), resolve them, and pass the result into the builder:

```python
def resolve_subagents(plugins, names) -> dict[str, SubagentDefinition]:
    out = {}
    for name in names:
        rec = plugins.get_subagent(name)
        if rec is None:
            raise AgentValidationError(f"subagent {name!r} is not a registered plugin")
        out[name] = rec.resolve()()           # entrypoint -> build() -> SubagentDefinition
    return out
```

`build_code_agent` / `build_research_agent` gain a **keyword-only, trailing** `subagents: Mapping[str, SubagentDefinition] | None = None` — placed **after** the existing `metrics` param (e.g. `def build_code_agent(model_factory, persistence, settings, metrics=None, *, subagents=None)`). This is load-bearing: two integration call sites pass `metrics` as the 4th *positional* arg (`test_code_agent.py:294-296, 327-329`), so inserting `subagents` before `metrics` would silently bind `metrics` into `subagents`. When `subagents` is `None` (the positional test calls), `LoopAgent` falls back to `DEFAULT_ROLE_INSTRUCTIONS`; when provided, it builds a `RoleInstructions` from the plugin defs. This keeps every existing call site compiling while making the production path plugin-driven.

**Ordering / error type (resolver vs `validate_spec`).** In the production flow `resolve_subagents` runs *first* — inside the builder during `build_agent_registry`, before `AgentRegistry.register` calls `validate_spec`. So an unknown subagent actually aborts from the resolver, not from `validate_spec`. Both MUST raise `AgentValidationError` (not a bare error) with a consistent "agent / missing subagent" message, so the "malformed spec aborts startup non-zero naming the agent" guarantee holds at whichever site fires first. The `validate_spec` check is kept too (defense for any spec built without going through `resolve_subagents`).

## Risks / Trade-offs

- **[Prompt drift: `loop.py` constants vs bundled `prompt.md`]** → the constants are explicitly the *fallback*; production uses the plugins. The guard needs a **capturing** test model (the stock `FakeMessagesListChatModel` discards inputs and cannot observe which prompt was sent — see Decision 3): with a `CapturingScriptedChatModel` recording the `messages`, a unit test asserts the loop ran with the supplied `RoleInstructions` text, so a divergence that matters (production silently falling back to the constants) is caught. Without the capturing model the test would pass regardless and guard nothing.
- **[Two unused extension fields now coexist: `AgentSpec.subagents` (deepagents) and `subagent_names` (loop roles)]** → documented: `subagents` feeds the optional `build_deep_agent` path; `subagent_names` feeds the MVP loop. Kept separate so neither path is disturbed.
- **[Plugins → definition coupling via `entrypoint`]** → `SubagentDefinition` in the plugins layer keeps imports one-directional (`agents → plugins`); handlers never import `agents`. Lazy `resolve()` means a broken subagent handler surfaces at build/startup, not at scan time — consistent with tool plugins.
- **[Scope creep toward per-subagent model/tools]** → explicitly Non-Goal; `SubagentDefinition` is prompt-only so the temptation is structurally absent for this change.

## Migration Plan

Pure worker-internal change; no schema, MQ, or API contract moves. Ship the plugins + wiring together. Rollback = revert the worker commits; the loader tolerates an empty `subagent/` dir (it did so before), and the loop's default constants still drive the loop if the plugins are absent.

## Open Questions

- None blocking. Per-subagent model/tools and the research-specific summarizer subagent (ARCHITECTURE §8.6) are deferred to a later change once a concrete need appears.
