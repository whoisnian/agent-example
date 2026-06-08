## 1. Subagent plugin contract

- [ ] 1.1 Add `worker/worker/plugins/subagent_spec.py`: frozen `SubagentDefinition{name: str, instruction: str}` and `load_prompt(handler_file: str) -> str` (reads the sibling `prompt.md`)
- [ ] 1.2 Add the `planner` plugin: `worker/worker/plugins/subagent/planner/{plugin.yaml, prompt.md, handler.py}` — `plugin.yaml` declares `kind: subagent`, `name: planner`, `version: 0.1.0`, `entrypoint: worker.plugins.subagent.planner.handler:build`, `applies_to.task_types: [code-gen, research]`; `handler.build()` returns `SubagentDefinition("planner", load_prompt(__file__))`; `prompt.md` carries the planner instruction (current `_PLANNER_INSTRUCTION` text)
- [ ] 1.3 Add the `executor` plugin (same shape; `prompt.md` = current `_EXECUTOR_INSTRUCTION` text)
- [ ] 1.4 Add the `critic` plugin (same shape; `prompt.md` = current `_CRITIC_INSTRUCTION` text)
- [ ] 1.5 Add `worker/tests/unit/test_subagent_plugins.py`: the three plugins load via `load_plugins()`, `get_subagent(<role>)` resolves to a `SubagentDefinition` whose `instruction` equals the `prompt.md`, and a `kind`-mismatch under `subagent/` aborts (reuse existing loader-error pattern)

## 2. Resolver + agent spec

- [ ] 2.1 Add `worker/worker/agents/subagent.py` with `RoleInstructions{planner, executor, critic}` and `resolve_subagents(registry, names) -> dict[str, SubagentDefinition]` that raises `AgentValidationError` (imported from `agents/registry.py`) naming any missing subagent — NOT a bare error — since this runs during build, before `validate_spec`, and is the real first failure site
- [ ] 2.2 Extend `AgentSpec` (`agents/base.py`) with `subagent_names: tuple[str, ...] = ()`; leave the existing `subagents` (deepagents passthrough) field untouched
- [ ] 2.3 Extend `validate_spec` (`agents/registry.py`) to check each `spec.subagent_names` resolves via `plugins.get_subagent(name)`, raising `AgentValidationError` naming the agent + missing subagent (kept as defense even though `resolve_subagents` fires first in the production flow)
- [ ] 2.4 Unit tests: `resolve_subagents` happy path + unknown-name raises `AgentValidationError` (assert the type, so the "fail startup non-zero" guarantee holds at the real site); `validate_spec` rejects an unknown subagent (extend `test_agent_registry.py`)

## 3. Loop sources role instructions from subagents

- [ ] 3.1 In `agents/loop.py`: define `DEFAULT_ROLE_INSTRUCTIONS` (the existing three constants) and add a `roles: RoleInstructions = DEFAULT_ROLE_INSTRUCTIONS` keyword param to `run_agent_loop`; thread it through the intermediate hops (`_load_or_create_plan`, `_run_step`) down to `_plan`/`_execute`/`_critic` (replacing the direct constant references), the same way `system_prompt` is threaded. Existing `run_agent_loop(...)` call sites that omit `roles` MUST behave identically
- [ ] 3.2 In `agents/base.py`: `LoopAgent` accepts the resolved role instructions (a `RoleInstructions`, default `DEFAULT_ROLE_INSTRUCTIONS`) and passes `roles=` into `run_agent_loop`
- [ ] 3.3 Add a capturing test model to `tests/support/fake_model.py` (e.g. `CapturingScriptedChatModel(BaseChatModel)` that appends each call's `messages` to a public list, then replays the scripted responses) — the stock `FakeMessagesListChatModel` discards inputs so it cannot observe the prompt. Unit test (`test_agent_loop.py`): run the loop with a custom `RoleInstructions` and assert the planner/executor/critic text appears in the captured `SystemMessage` content

## 4. Wire production build to the plugins

- [ ] 4.1 `code_agent.py` + `research_agent.py`: declare `subagent_names=("planner", "executor", "critic")` on their specs; `build_*_agent` gains a **keyword-only, trailing** `*, subagents: Mapping[str, SubagentDefinition] | None = None` placed AFTER `metrics` (NOT before — `test_code_agent.py:294-296, 327-329` pass `metrics` 4th-positional), and builds a `RoleInstructions` from it (falling back to `DEFAULT_ROLE_INSTRUCTIONS` when `None`)
- [ ] 4.2 `agents/__init__.py::build_agent_registry`: read each agent's `subagent_names` (import `CODE_AGENT_SPEC`/`RESEARCH_AGENT_SPEC`), `resolve_subagents(plugins, names)`, and pass the result as `subagents=` into `build_code_agent`/`build_research_agent`
- [ ] 4.3 Test: `build_agent_registry` with the real registry resolves the three subagent plugins and the loop runs with the plugin-sourced instructions, asserted via the capturing model from 3.3 (drift guard per design Risk 1); existing positional `build_code_agent(mf, persistence, settings[, metrics])` calls in `test_code_agent.py` still work via the `None` default

## 5. Docs

- [ ] 5.1 Reconcile `docs/ARCHITECTURE.md §8` with the implemented subagent contract — align §8.3's `@register class CriticSubagent(Subagent)` form to the `entrypoint: build()->SubagentDefinition` contract already used by tool plugins, and note the §8.2 plural-dir (`subagents/`) vs implemented singular-dir (`subagent/`) divergence. Both are pre-existing (tool plugins already use `entrypoint:` + singular `tool/`); update the doc rather than silently diverge (AGENTS §1)

## 6. Gates

- [ ] 6.1 From `worker/`: `make lint` (ruff check + format --check), `make type` (`mypy --strict worker/`), `make test` (unit), `make test-int` (integration; Docker)
- [ ] 6.2 `openspec validate add-worker-subagent-plugin --strict` from repo root passes
