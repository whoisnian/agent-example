# Design — add-worker-code-agent

## Context

The worker scaffold (`init-worker-scaffold`) already owns the hard parts of the runtime: the `TaskConsumer` claim→dispatch→publish→ack pipeline, `RunContext` construction, `CostMeter` (a `BaseCallbackHandler`), `CheckpointStore`, `EventPublisher` / `CostEventPublisher`, the `PluginRegistry` + loader, and cooperative `CancelToken` / `PauseToken`. The single missing piece is the agent: `ExecutionDispatcher.dispatch` raises `AgentNotImplementedError`, and `agents/__init__.py` is empty by design.

The consumer's success path is already wired (`consumer.py:268-282`): if `dispatch` returns without raising, the run is marked `succeeded`, a `status=succeeded` event is published, and the message is acked. So this change adds the agent layer *behind* the existing seam — it does not touch the consumer pipeline.

Constraints from `AGENTS.md` / `docs/ARCHITECTURE.md`:
- §4.2: agents reach infra only through `RunContext`; no direct MQ/DB/OSS imports. Workers may write only `cost_events`, `task_runs` heartbeat, `task_checkpoints`, `artifacts`.
- §3.3.1: per-task assembly of planner / executor / critic subagents + a filesystem tool mounted on the task's OSS prefix.
- §3.3.2: each step writes an idempotent checkpoint (`step_seq` unique); resume replays from the latest, not from zero.
- §6: never log or commit provider API keys.

User decisions for this proposal: full planner/executor/critic orchestration; both `code-gen` and `research` task types; LLM calls go through an injectable model factory with a scripted fake in tests (no network in CI).

## Goals / Non-Goals

**Goals:**
- Replace the placeholder dispatcher with a registry-backed one that runs a real agent per `task_type`, preserving the unknown-type → DLX behavior.
- Deliver a reusable base deep-agent (planner/executor/critic) and two concrete agents (`code-gen`, `research`).
- Close the loop: plan → per-step execute → critic → checkpoint → `step`/`plan` events → artifact upload → `succeeded`, with cost events emitted throughout.
- Make the whole loop testable in CI with a fake model: no API key, no network, deterministic.
- Support resume from the latest checkpoint when `attempt_no > 1`.

**Non-Goals:**
- Sandboxing / code execution isolation (`sandbox.py`, seccomp, microVM) — `[Post-MVP]` per ARCHITECTURE §8.4. The MVP code-gen agent produces files/artifacts; it does not run untrusted generated code.
- Skill plugins (§8.1 marks Skill as Post-MVP). Only `tool` (and the built-in subagents) are in scope.
- Plugin versioning / canary lane routing (§8.5).
- Cost Service settlement, pricing, and the `tasks.status` state machine — owned by other capabilities. This change only *emits* cost events and marks `task_runs` terminal via the existing consumer path.
- Pause/resume control-plane wiring beyond honoring the existing `PauseToken` / `CancelToken` at step boundaries (the listener already exists in `core/control.py`).

## Decisions

### D1 — `Agent` protocol + `AgentRegistry`, dispatcher does lookup only
`agents/base.py` defines an `Agent` protocol with `task_type: str` and `async def run(self, ctx: RunContext, message: TaskExecuteMessage) -> None`. `agents/registry.py` maps `task_type → Agent` (or an agent *factory* — see D2). `ExecutionDispatcher` is constructed with the registry; `dispatch` does `agent = registry.get(message.task_type)`; if `None`, raise `AgentNotImplementedError(task_type)` (unchanged DLX path); else `await agent.run(ctx, message)`.
- *Why a registry, not `if/elif`*: matches the placeholder docstring's promise ("future agent proposals will replace this with a registry lookup") and keeps adding a task type to a one-line registration.
- *Alternative rejected*: reuse `PluginRegistry` for agents. Agents are not plugins (no `plugin.yaml`, not LLM-callable tools); conflating them muddies the §8 contract.

### D2 — Build the deep agent per run, not once at startup
The deep agent (LangGraph graph from `create_deep_agent`) closes over per-run state (the OSS-prefixed filesystem, the `CostMeter` callback bound to *this* `ctx`, cancel/pause tokens). So the registry holds an **`AgentSpec`** (static: system prompt, subagent defs, tool names, model key, limits) and a `build(ctx)` that instantiates the graph for the run. The static spec is validated once at startup (fail fast on a bad prompt path / unknown tool); the graph is built per message.
- *Why*: the `CostMeter` callback must be wired to the run's `RunContext` so `on_llm_end` emits cost events with the right `run_id`/seq. A process-singleton agent would leak one run's callback/token state into the next.
- *Concurrency premise*: the consumer is `prefetch=1` (`consumer.py:92`) — one message in flight per worker process, runs are sequential within a process; concurrency is across worker processes. Per-run build is required even for *sequential reuse* (a singleton would carry the previous run's `cost_meter`), so the decision holds regardless of in-process parallelism.
- *Trade-off*: graph construction cost per message. Acceptable — dominated by LLM latency; spec assembly is cheap.

### D2b — MVP loop drives the model directly per role (implementation finding, slice B)
Implementation revealed that `deepagents.create_deep_agent` cannot be driven by a scripted fake model: its compiled graph calls `bind_tools` (unsupported by LangChain's simple fakes) and makes a non-deterministic number of internal model calls with provider-specific middleware. That collides head-on with this change's other requirements — an explicit, checkpoint-per-step loop and deterministic fake-model tests. So the **MVP loop invokes the chat model directly once per role** (planner / executor / critic), each with a role-specific prompt, which keeps step boundaries, checkpoints, events, and pause/cancel fully under our control and makes the fake-model tests deterministic. `build_deep_agent` (D2) is retained as the documented richer-reasoning path for a later iteration but is **not** on the MVP hot path. The `ModelFactory` seam, cost-meter attachment, and fake-model testing (the spec's real intent) are unchanged. The spec requirement "Deep Agent Assembly via Model Factory" is correspondingly relaxed from "MUST build on `create_deep_agent`" to "MAY"; assembly via the factory + per-run binding remain mandatory.

### D2a — The step loop lives in its own module
The outer loop (D4) is the core of this change and is shared by both agents, so it lives in `agents/loop.py`, not inside `base.py`. Keeps `base.py` (assembly) focused, lets the loop be unit-tested in isolation with the fake model, and helps the per-slice 500-line budget (Risk #1).

### D3 — Model injection via `ModelFactory` (the test seam)
`agents/model.py` defines `ModelFactory.get(model_key: str) -> BaseChatModel`. Production factory maps `model_key` → a `langchain-openai` `ChatOpenAI` using config + env API key, with an optional `base_url` so the same seam targets OpenAI or any OpenAI-compatible gateway (broader provider reach than a single vendor SDK). Agents never import a provider SDK; they call `ctx`-independent `self._models.get(self.model_key)`. Tests pass a `FakeModelFactory` returning a **scripted** `FakeChatModel` whose responses drive a deterministic plan→execute→critic transcript.
- *Why scripted fake over recorded cassettes (VCR)*: the user chose injection. A scripted fake exercises the *orchestration* (state transitions, checkpoint/event emission, artifact write) which is the logic we own; it needs no API key, no network, and no re-recording when prompts change. Real-model behavior is validated manually / out of CI.
- The `CostMeter` callback still fires against the fake (LangChain invokes `on_llm_start/end` regardless of model), so cost-event emission is covered in tests too.

### D4 — Orchestration is an explicit step loop owning checkpoints & events
Rather than letting `deepagents` run opaquely end-to-end, the agent runs an explicit outer loop the worker controls, so the platform's invariants (checkpoint per step, `step` event per step, pause/cancel at boundaries) are enforced in *our* code, not the framework's:

```
plan = planner.invoke(prompt, ctx)            # → emit "plan" event, then checkpoint_write(step_seq=0, plan)
for step in plan.steps[resume_from:]:
    if ctx.cancel_token.is_set(): raise asyncio.CancelledError   # consumer requeues
    await ctx.pause_token.wait_if_paused()     # only reached after the previous step's checkpoint is durable
    result = executor.invoke(step, ctx)        # deep-agent / subagent does the work + tool calls
    verdict = critic.invoke(step, result, ctx) # advance | retry(≤N) | finish
    ctx.step += 1
    checkpoint_write(step_seq=ctx.step, ...)   # see D5a: CheckpointConflictError on replay → treat as already-done
    events.publish_event(kind="step", seq=ctx.next_event_seq(), payload={...})
    if verdict == finish: break
upload_artifacts(ctx); write artifacts rows
# return normally -> consumer marks run succeeded
```

- Planner/executor/critic are `deepagents` subagents (or sub-graphs) — the *intelligence* is the framework's; the *loop control, durability, and observability* are ours.
- *Why explicit loop*: §3.3.2 demands per-step idempotent checkpoints and resume; the heartbeat watchdog cancels via `cancel_token`; pause must release execution only after a checkpoint is durable (§6 control flow). None of that is expressible by handing the whole job to an opaque `agent.invoke`.

### D5 — Resume semantics (`attempt_no > 1`)
At run start the agent calls `ctx.checkpoint_store.latest()`. If present, it restores `plan` + completed-step state and sets `resume_from = latest.step_seq`, replaying only the next step.
- The plan itself is checkpointed at `step_seq=0` so resume does not re-invoke the planner (avoids non-deterministic re-planning and wasted tokens).

#### D5a — Checkpoints are NOT silently idempotent; the loop catches the conflict
The existing `CheckpointStore.write` raises `CheckpointConflictError` on a duplicate `(run_id, step_seq)` (`checkpoint.py:9,55`; `persistence.py:85`) — it is *not* an upsert. So the loop MUST treat that exception as the signal "this step is already durably persisted" and continue (skip re-executing / advance `resume_from`), rather than letting it abort the run. This matters in the crash-after-checkpoint-before-ack window: redelivery replays a `step_seq` that already exists. We rely on the conflict (not a silent overwrite) as the replay detector. *Alternative rejected*: making the store an upsert — would mask double-execution bugs and lose the clean replay signal the scaffold deliberately exposes.

#### D5b — `step_seq=0` (plan) state schema
The plan checkpoint's `state` dict (`CheckpointRecord.state`, `checkpoint.py:30`) carries:
`{"plan": [{"idx": int, "title": str, "done": bool}, ...], "step_count": int}`. Per-step checkpoints (`step_seq>=1`) carry `{"idx": int, "title": str, "verdict": str, "result_summary": str}`. Resume reads `step_seq=0` for the plan and the max `step_seq` to derive `resume_from`. Writers (task 5.2) and the resume reader (task 8.1) MUST agree on this shape.

### D6 — Cost emission stays inside existing wrappers
LLM cost: the agent attaches `ctx.cost_meter` (the `CostMeter` callback) to every model `.invoke`/graph run via LangChain `config={"callbacks": [ctx.cost_meter]}`. Tool cost: tools are wrapped with the existing `cost_metered_tool(tool_name)` decorator. Agents never call `cost_publisher` directly — satisfies §3.3.3 and keeps the publisher boundary clean.

### D7 — Artifacts: upload to OSS prefix, write `artifacts` rows, on success only
On `finish`, the agent uploads produced files under `ctx.oss_prefix` (the task/version-scoped prefix from `compute_oss_prefix`) via `ctx.oss_client`, then writes `artifacts` rows (the sole business table workers may write). If artifact upload fails, the run fails (error event + `nack(requeue=false)`) — a "succeeded" run with no durable artifact is a lie.
- The writer already exists: `Persistence.insert_artifact(version_id, kind, oss_key, mime, bytes_size, sha256)` (`persistence.py:349`), and `artifacts` is in the persistence layer's `ALLOWED_WRITE_TABLES` (INSERT-only). The agent reuses it; no new method, no raw SQL. Real columns are `kind / oss_key / mime / bytes / sha256` — design/spec/tests use these names, not "type/size/hash".

### D8 — Two task types, shared base, different specs
`code_agent.py` and `research_agent.py` each export an `AgentSpec` differing only in system prompt, tool set, subagent mix, and model key (per §8.6 example). Both reuse the base builder and the step loop. `registry.py` registers `{"code-gen": code_spec, "research": research_spec}`.
- MVP tool set: `code-gen` → OSS filesystem tool (read/write task workspace) + a stub `run_tests`-style tool only if it stays sandbox-free; `research` → OSS filesystem + `web_search` (the existing/stub tool). Concrete minimal tools chosen in tasks; the filesystem tool is the one hard requirement.

### D9 — Model & API-key configuration
Worker config gains `code_agent_model` / `research_agent_model` (env `CODE_AGENT_MODEL` default `claude-opus-4-7`, `RESEARCH_AGENT_MODEL` default `claude-sonnet-4-6` per §8.6) and the provider API key via env/secret. The `ModelFactory` reads these; the key is never logged (structlog field redaction) nor written to the repo (§6).

### D10 — `task.events` `kind` values: `plan`, `step`
Agents emit `kind="plan"` once (payload: ordered step list) and `kind="step"` per completed step (payload: `step_seq`, title, critic verdict, brief result summary — no secrets, bounded size). These ride the existing `TaskEvent` envelope; the DB/Realtime consumers treat `kind` as an open string, so no contract change. `status` events stay owned by the consumer.

### D11 — Observability
Add `agent_runs_total{task_type, outcome}` (outcome = succeeded|failed|cancelled), `agent_steps_total{task_type}`, and `agent_step_duration_seconds` histogram into the worker's existing metrics registry. Each step logs `task_id`/`run_id`/`version_id`/`step`/`verdict`.

## Risks / Trade-offs

- **[Scope: single PR > 500 lines]** Full planner/executor/critic + two agents + tools + model seam + tests exceeds the `AGENTS.md §7` 500-line guidance. → `tasks.md` is sliced so implementation lands in reviewable PRs: (A) dispatcher+registry+model seam+base loop with a trivial fake, (B) code-gen agent + filesystem tool + artifacts, (C) research agent + web_search, (D) resume + pause/cancel hardening. Each slice is independently green.
- **[Opaque framework behavior]** `deepagents` may internally loop/branch in ways that bypass our per-step checkpoint cadence. → We drive the *outer* loop ourselves (D4); subagents handle one step's reasoning, and we checkpoint between steps. We do not delegate the whole task to one `invoke`.
- **[Fake model ≠ real behavior]** Scripted fakes prove orchestration, not prompt quality. → Accept for CI; document a manual smoke path with a real key. Out-of-CI eval is a future proposal.
- **[Cost-event loss under crash]** Cost events are fire-and-forget (buffered publisher); a crash mid-step can drop some. → Matches §3.3.3 fault tolerance ("cost loss doesn't fail the task"); checkpoint replay does not double-bill because re-run emits new seqs and Cost Service dedupes on `idempotency_key=run_id+seq`. Acceptable for MVP.
- **[Artifact-write boundary creep]** Workers writing `artifacts` is permitted but must not bleed into other tables. → Persistence method is artifact-only; reviewed against §4.2.

## Migration Plan

- Additive. The dispatcher swap is behind the stable `dispatch(ctx, message)` signature; `main.py` constructs the real dispatcher. No DB schema change; `artifacts` table already exists.
- Deploy order: workers can ship after `add-task-create-api` (already landed) since execute messages already flow. No API/web coordination required.
- Rollback: revert `main.py` to inject the placeholder `ExecutionDispatcher` (or register zero agents) → behavior returns to unimplemented→DLX with no other code change. The agent modules can remain dormant.

### D13 — Retry budget is per-run, not persisted across redelivery
The `max_step_retries` counter (D-loop) lives in memory for the duration of one delivery. On crash + redelivery, resume reuses the latest checkpoint but the retry counter resets — a redelivery gets a fresh budget for the resumed step. *Why*: persisting the counter would need a checkpoint write per failed attempt and complicate the state schema; a fresh budget per delivery is simpler and bounded overall by the broker's redelivery/DLX policy. Acceptable for MVP.

### D14 — Deadline enforced at step boundaries (whole-run)
`deadline_ts` (message field) is checked at each step boundary; if exceeded, the loop fails the run (error event + `nack(requeue=false)`) rather than starting another step. Per-step token budgeting (`max_tokens_per_step`, §8.6) is deferred. This is now a normative spec scenario, not an open question.

## Open Questions

1. **`run_tests` / code-execution tool**: any tool that executes generated code needs the Post-MVP sandbox. For MVP, do we ship code-gen *without* an execution tool (produce files only), or include a no-op/echo placeholder? Leaning produce-files-only to stay inside the no-sandbox boundary; confirm at slice B.
2. **Plan persistence target**: §3.3.1 says plan is "persisted to DB". For MVP we persist the plan via `task_checkpoints` (step_seq=0) + the `plan` event, not a dedicated `task.plan` column. Is a first-class plan store wanted later? Defer to a read-API proposal.

> Resolved during review (was Open Q): **artifact writer** — reuse `Persistence.insert_artifact` (D7); **deadline granularity** — whole-run at step boundary (D14).
