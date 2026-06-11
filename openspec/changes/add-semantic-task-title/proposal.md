## Why

`add-task-title-autogen`（archive/2026-06-10）实现了创建任务时由 API 从 prompt 首行截断派生标题，但截断结果可读性差（prompt 以代码、长句或寒暄开头时尤甚）。该变更的 design 已明确预留方向：语义化标题应由 Worker 通过 LLM 生成、经事件异步回写，API 不直接调用 LLM 的边界不可破。现按此路径落地。

## What Changes

- `POST /api/v1/tasks`：标题派生逻辑保留为**占位标题**；当标题为派生而非用户显式提供时，execute outbox payload 新增 `gen_title: true` 标记（用户显式提供 title、iterate、rollback 均不带该标记）。
- Worker：`TaskExecuteMessage` 新增可选字段 `gen_title`（缺省 `false`，create 派生路径专属的白名单标记）。为 `true` 且 run 为 fresh（无既有 checkpoint，防重投/接管重复生成）、未被 cancel、`task_type` 有注册 agent 时，Worker 在 claim run 后、调度 agent 前，经 `ModelFactory` 取小模型对 prompt 做一次 LLM 调用生成语义化标题（必须挂 `ctx.cost_meter` 回调，成本走既有 `cost.llm` 事件），并经 `EventPublisher` 发出新事件 `kind="title"`（payload `{title}`）。生成失败为 best-effort：WARN + metric，不影响任务执行，占位标题保留。
- API event ingest：消费 `kind="title"` 事件，经 Domain Service 方法更新 `tasks.title`（trim、按既有 64 rune / 200 字节规则截断、空串跳过），与事件落库同事务，幂等键复用 `(run_id, seq)`。
- Realtime Gateway 与 web 无**代码**变更：gateway 按 `event.#` 通配透传任意 kind；Task Detail 已有"任意 `task:` frame → 失效 task 查询"机制，标题自动实时刷新。但 `realtime-gateway` 规格的事件帧 kind 枚举是规范性条文，需一个最小 delta 将 `title` 纳入（顺带补上既有遗漏的 `plan`）。
- `docs/ARCHITECTURE.md §5.3`：execute 消息契约增加 `gen_title` 字段，WS 推送 kind 枚举增加 `title`。

无 BREAKING：`gen_title` 为可选新增字段，旧消息缺省 `false`；`kind="title"` 对旧消费者是未知 kind（ingest 既有行为为"持久化但不转移状态"，gateway 透传，web 仅触发失效）。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `task-write-api`: "Create Task Endpoint" requirement —— 派生标题降级为占位语义；派生发生时 outbox execute payload 须含 `gen_title: true`，显式 title 时不含。
- `worker-messaging`: "Task Execute Consumer" requirement —— `TaskExecuteMessage` 字段表增加可选 `gen_title`。
- `worker-execution-runtime`: 新增 "Semantic Title Generation" requirement —— 标题生成器组件（ModelFactory + CostMeter、best-effort、`kind="title"` 事件）。
- `task-event-ingest`: 新增 "Title events update the task title" requirement —— `kind="title"` 事件经状态机以外的专用 Domain Service 方法更新 `tasks.title`。
- `realtime-gateway`: "Subscription Protocol" requirement —— 事件帧 kind 枚举改为"worker kind 原样透传"措辞并纳入 `title`（补 `plan` 欠账），代码不变。

## Impact

- **代码**：
  - `api/internal/domain/task/`（service：execute payload 组装加 `gen_title`；新增 `ApplyGeneratedTitle` 域方法）、`api/internal/application/ingest/`（title 事件分支）。
  - `worker/core/`（`TaskExecuteMessage` 字段、title generator、consumer 接线）。
  - `docs/ARCHITECTURE.md §5.3`。
- **测试**：API 契约测试（outbox payload 断言）、ingest 集成测试（title 事件落库 + 标题更新 + 幂等）、worker 单测（FakeModelFactory 驱动标题生成、失败不阻断、成本事件发出）。
- **不改**：DB schema、`tasks.title` 约束、realtime-gateway 代码（仅规格枚举措辞）、web 各规格、cost 事件 kind 集合。
- **下游**：Task List 页标题不实时刷新（返回列表时 React Query 重取已覆盖），不在本变更范围。
