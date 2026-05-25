# Tasks — add-worker-code-agent

> Sliced for reviewable PRs (design Risk #1). Slice A = §§1–4, B = §§5–6, C = §7, D = §8.
> Each slice must be independently green (`make lint test` in `worker/`).

## 1. Dependencies & config

- [ ] 1.1 Add `deepagents` + `langchain-anthropic` (and required transitive LangChain deps) to `worker/pyproject.toml`; run `uv lock`.
- [ ] 1.2 Extend `worker/worker/core/config.py` with `code_agent_model` (env `CODE_AGENT_MODEL`, default `claude-opus-4-7`), `research_agent_model` (env `RESEARCH_AGENT_MODEL`, default `claude-sonnet-4-6`), provider API-key field (env, never logged), and `max_step_retries` (default 2).
- [ ] 1.3 Add a config unit test asserting defaults + env overrides; assert the API-key field is excluded from any `repr`/log rendering.

## 2. Model factory (test seam)

- [ ] 2.1 Create `worker/worker/agents/model.py` with `ModelFactory` protocol + `get(model_key) -> BaseChatModel`.
- [ ] 2.2 Implement `ProviderModelFactory` resolving `model_key` → provider chat model from config; no provider import outside this module.
- [ ] 2.3 Implement `FakeModelFactory` + scripted `FakeChatModel` (deterministic responses, drives plan/execute/critic) under `worker/tests/` support helpers.
- [ ] 2.4 Unit test: `FakeModelFactory` returns a model that triggers LangChain `on_llm_start/on_llm_end` callbacks (so `CostMeter` fires).

## 3. Agent contract & registry

- [ ] 3.1 Create `worker/worker/agents/base.py`: `Agent` protocol (`task_type`, `async run(ctx, message)`), `AgentSpec` (system-prompt path, subagent defs, tool names, model_key, limits), and `build_deep_agent(spec, ctx, model_factory)` using `deepagents.create_deep_agent`.
- [ ] 3.2 Create `worker/worker/agents/registry.py`: `AgentRegistry` with `register(spec)` / `get(task_type)`; startup validation (prompt path resolvable, declared tools present in `PluginRegistry`) that raises on bad specs.
- [ ] 3.3 Unit test: registry resolves a registered type, returns `None` for unknown, and raises on a spec with a missing prompt file / unknown tool.

## 4. Dispatcher swap & wiring

- [ ] 4.1 Replace `worker/worker/core/dispatcher.py` body: construct with `AgentRegistry`; `dispatch` resolves agent, raises `AgentNotImplementedError` only when none registered, else `await agent.run(ctx, message)`. Keep the exception type and method signature stable.
- [ ] 4.2 Update `worker/worker/main.py` to build the `ModelFactory` + `AgentRegistry` (registering both agents) and pass the real dispatcher into `TaskConsumer`.
- [ ] 4.3 Update/extend the existing dispatcher unit tests for: unknown type → `AgentNotImplementedError`; registered type → `agent.run` awaited; agent exception propagates unchanged.
- [ ] 4.4 Integration test (existing consumer end-to-end harness + a trivial registered fake agent): registered type runs → assert all three of `mark_run_terminal(status="succeeded")`, a `kind="status"` `succeeded` event, and `ack` (covers the `unreachable in scaffold` success branch at `consumer.py:269`); unknown type still → `unimplemented` event + DLX.

## 5. Step loop (planner / executor / critic)

- [ ] 5.1 Implement the explicit outer loop in its own module `worker/worker/agents/loop.py` (shared by both agents): plan → per-step execute → critic verdict (advance|retry≤N|finish), owning `ctx.step`, checkpoint writes, and event emission order per spec. Wrap each `CheckpointStore.write` so a `CheckpointConflictError` is caught and treated as "step already persisted" (advance), not an error.
- [ ] 5.2 Emit `kind="plan"` event once + checkpoint at `step_seq=0` carrying the plan per the D5b state schema (`{"plan":[{idx,title,done}],"step_count"}`); then per completed step increment `ctx.step`, write the per-step checkpoint (`{idx,title,verdict,result_summary}`), emit `kind="step"` event (payload: step_seq, title, verdict, bounded summary).
- [ ] 5.3 Attach `ctx.cost_meter` as a callback on every model invocation in the loop; wrap loop tool calls with `cost_metered_tool`.
- [ ] 5.4 Enforce bounded retries (`config.max_step_retries`): exceeding raises to fail the step/run. The retry counter is per-delivery (in-memory) and is NOT persisted across redelivery (design D13) — a resumed delivery gets a fresh budget.
- [ ] 5.6 Enforce run deadline: at each step boundary, if the message's `deadline_ts` is in the past, fail the run (error) before starting the next step (design D14).
- [ ] 5.5 Unit test the loop with `FakeModelFactory` scripts: happy path (plan→steps→finish) asserting plan/step event sequence and checkpoint `step_seq` progression; retry-then-advance; retry-budget-exceeded → raises.

## 6. Code-gen agent + filesystem tool + artifacts

- [ ] 6.1 Add OSS-backed filesystem tool plugin under `worker/worker/plugins/tool/oss_fs/` (`plugin.yaml` kind=tool, `applies_to.task_types: [code-gen, research]`, `handler.py` read/write scoped to `ctx.oss_prefix`).
- [ ] 6.2 Create `worker/worker/agents/code_agent.py`: `AgentSpec` for `task_type=code-gen` (system prompt under `agents/prompts/`, planner/executor/critic subagents, oss_fs tool, `model_key=code`). Produce files only — no code-execution tool (design Open Q#2 / §8.4 sandbox boundary).
- [ ] 6.3 Reuse the existing `Persistence.insert_artifact(version_id, kind, oss_key, mime, bytes_size, sha256)` (`persistence.py:349`; `artifacts` is already in `ALLOWED_WRITE_TABLES`, INSERT-only) — no new method, no raw SQL.
- [ ] 6.4 Implement artifact upload on success: upload under `ctx.oss_prefix`, write `artifacts` rows; upload/row failure raises (run fails, not succeeds). Forbid writes to `tasks`/`task_versions`.
- [ ] 6.5 Integration test `code-gen` end-to-end with fake model: 201-equivalent execute message → plan/step events → artifact uploaded to OSS prefix + `artifacts` row(s) → run `succeeded`. Plus artifact-upload-failure → run failed.

## 7. Research agent

- [ ] 7.1 Add a `web_search` tool plugin under `worker/worker/plugins/tool/web_search/` usable by research. MVP ships a **stub** returning deterministic results — do NOT introduce a real search SDK/network dependency (would break the no-network CI goal and §7 MVP boundary); a real backend is a separate later change.
- [ ] 7.2 Create `worker/worker/agents/research_agent.py`: `AgentSpec` for `task_type=research` (research system prompt, oss_fs + web_search tools, `model_key=research`); register in `registry.py`.
- [ ] 7.3 Integration test `research` end-to-end with fake model: produces a report artifact under the OSS prefix + `artifacts` row → `succeeded`.

## 8. Resume, pause/cancel, observability hardening

- [ ] 8.1 Implement resume: on run start read `checkpoint_store.latest()`, restore plan from the `step_seq=0` checkpoint (D5b schema), set `resume_from`, skip completed steps; fresh run with no checkpoint starts at planning. Redelivery that rewrites an existing `step_seq` → `CheckpointConflictError` caught as "already persisted" (per 5.1).
- [ ] 8.2 Implement step-boundary cancel (`ctx.cancel_token` → raise `asyncio.CancelledError`) and pause (ensure step checkpoint durable, then `pause_token.wait_if_paused()`).
- [ ] 8.3 Add metrics `agent_runs_total{task_type,outcome}` (outcome values `success|error|cancelled`, matching `messages_consumed_total`), `agent_steps_total{task_type}`, `agent_step_duration_seconds` to the worker registry; log `task_id`/`run_id`/`version_id`/`step`/`verdict` per step.
- [ ] 8.4 Integration test resume: redeliver a message with a checkpoint at `step_seq=2` of a 4-step plan → only steps 3–4 execute, planner not re-invoked. Plus a duplicate-`step_seq` write → `CheckpointConflictError` is caught, step not re-executed, run not failed.
- [ ] 8.5 Integration test cancel-at-boundary → `CancelledError` → requeue; pause → checkpoint durable before blocking → resume continues.
- [ ] 8.6 Test metrics: a 3-step success increments `agent_runs_total{...outcome="success"}` by 1 and `agent_steps_total` by 3 (scrape `/metrics`).

## 9. Docs & verification

- [ ] 9.1 Update `worker/README.md` with an "Agents" section: registry, model factory + env vars, the fake-model test seam, and the produce-files-only MVP boundary.
- [ ] 9.2 Run `make lint test` and the integration suite in `worker/` (all slices) clean; `go`/`web` untouched.
