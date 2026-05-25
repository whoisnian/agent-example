# Proposal — add-worker-code-agent

## Why

The worker scaffold consumes `task.execute` messages, builds a `RunContext`, and has a fully wired success path — but `ExecutionDispatcher.dispatch` always raises `AgentNotImplementedError`, so every task lands on `task.dlx`. Now that `POST /tasks` and `/iterate` mint real execute messages (`add-task-create-api`), the platform produces work that no one can complete. This change replaces the placeholder dispatcher with real `deepagents`-based agents for the two MVP task types, closing the first end-to-end loop: user submits → API enqueues → worker plans, executes, self-critiques, checkpoints each step, emits cost/progress events, uploads artifacts, and marks the run `succeeded`.

## What Changes

- Replace the placeholder `ExecutionDispatcher` with a registry-backed dispatcher: it looks up an `Agent` by `task_type` in an `AgentRegistry`, builds the agent on first use, and runs it against the `RunContext`. `AgentNotImplementedError` is raised only when no agent is registered for the `task_type` (preserving the existing DLX path for unknown types).
- Add a base deep-agent builder (`agents/base.py`) on top of `deepagents.create_deep_agent`, assembling the planner / executor / critic subagents and the task-scoped filesystem + plugin tools described in `docs/ARCHITECTURE.md §3.3.1`.
- Add two concrete agents: `agents/code_agent.py` (`task_type=code-gen`) and `agents/research_agent.py` (`task_type=research`), each declaring its system prompt, subagent set, tool set, and per-step limits. Register both in `agents/registry.py` keyed by `task_type`.
- Implement the **planner → executor → critic** loop as the agent's orchestration: planner decomposes the prompt into steps (persisted as a `plan` event), executor runs each step, critic decides advance / retry / finish. Every completed step writes a `task_checkpoints` row via the existing `CheckpointStore` and emits a `task.events` `step` event; the loop resumes from the latest checkpoint on `attempt_no > 1`.
- Wrap all LLM calls with the existing `CostMeter` LangChain callback and all tool calls with `cost_metered_tool`, so `cost.events` are emitted without agent code touching the publisher directly. Respect `ctx.cancel_token` (cancel) and `ctx.pause_token` (pause/resume after checkpoint) at step boundaries.
- On success, upload produced artifacts to the task's OSS prefix and write the corresponding `artifacts` rows (the one business table workers may write, per `AGENTS.md §4.2`), then return normally so the consumer marks the run `succeeded`.
- Introduce a **model-factory injection seam** (`agents/model.py`): agents obtain their chat model through a `ModelFactory` resolved from config, never by importing a provider SDK directly. Tests inject a scripted fake model so the full plan→execute→critic→checkpoint→event→artifact loop runs in CI with no API key and no network.
- Add the MVP tool plugins the agents need under `worker/worker/plugins/tool/` (e.g. an OSS-backed filesystem tool, alongside the existing `noop_tool/`) following the existing `plugin.yaml` + `handler.py` convention, registered through the existing Plugin Loader.

## Capabilities

### New Capabilities
- `worker-agent-orchestration`: the deep-agent assembly and execution model for the worker. Owns the `AgentRegistry` / `Agent` contract, the base deep-agent builder, the concrete `code-gen` and `research` agents, the planner/executor/critic step loop with checkpoint-and-resume, the `ModelFactory` injection seam, the cost/event emission discipline inside agents, the cancel/pause cooperation at step boundaries, and artifact upload on success. Scope is strictly the agent layer invoked by the dispatcher; messaging, run-context construction, and the cost/checkpoint primitives themselves remain owned by `worker-messaging` / `worker-execution-runtime`.

### Modified Capabilities
- `worker-execution-runtime`: the **Execution Dispatcher** requirement changes from "MUST always raise `AgentNotImplementedError`" (placeholder) to "MUST resolve an agent from the registry and run it; raise `AgentNotImplementedError` only when no agent is registered for the `task_type`". The unknown-type → DLX scenario is preserved; a new registered-type → success scenario is added. No other requirement in this capability changes (Run Context, Heartbeat, Cost Meter, Checkpoint Store, Plugin Loader are reused as-is).

## Impact

- **Code**
  - `worker/worker/core/dispatcher.py` — replace placeholder body with registry lookup + agent invocation; keep `AgentNotImplementedError` type and `dispatch(ctx, message)` signature stable so the consumer call site is unchanged.
  - `worker/worker/agents/` — new modules: `base.py`, `code_agent.py`, `research_agent.py`, `registry.py`, `model.py`, plus shared step-loop helpers. Exact split tracked in `tasks.md`.
  - `worker/worker/plugins/tool/` — new MVP tool plugin(s) the agents require (OSS filesystem tool at minimum), each a `plugin.yaml` + `handler.py` discovered by the existing loader.
  - `worker/worker/main.py` — construct the `AgentRegistry` (and `ModelFactory` from config) at startup and pass the real dispatcher into `TaskConsumer` instead of the placeholder.
- **Public contract**
  - No HTTP/MQ wire-format changes. `task.events` gains two emitted `kind` values produced by agents — `plan` and `step` — within the existing `TaskEvent` envelope (Realtime Gateway / DB consumer already treat `kind` as open). `cost.events` shapes unchanged.
  - `artifacts` table — workers begin writing rows here on success (already permitted by `AGENTS.md §4.2`; no schema change).
- **Dependencies**: add `deepagents` (and its transitive LangChain deps) to `worker/pyproject.toml`; add the provider chat-model package behind the `ModelFactory` — `langchain-openai` (`ChatOpenAI` speaks the OpenAI protocol and takes a `base_url`, so it also fronts OpenAI-compatible gateways/proxies for other providers). No new infra. Tests add no network dependency thanks to the fake model.
- **Configuration**: introduce worker env for model selection per task type (e.g. `CODE_AGENT_MODEL`, `RESEARCH_AGENT_MODEL`) and the provider API key (read from env/secret, never logged or committed per `AGENTS.md §6`). Defaults follow `docs/ARCHITECTURE.md §8.6`.
- **Observability**: add counters for agent runs by `task_type` and `outcome`, and a step-duration histogram; agent step boundaries log `task_id` / `run_id` / `version_id` / `step`.
