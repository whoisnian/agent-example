## Context

`POST /api/v1/tasks` 当前在 domain 层从 prompt 首行确定性派生标题（≤64 rune / ≤200 字节，兜底 `Untitled task`），可读性差。AGENTS 边界规定 `api/` 不调 LLM、`worker/` 所有 LLM 调用必须过 `core/cost_meter.py`、Worker 不得直写 `tasks` 主表 —— 因此语义化标题只能由 Worker 生成、经 `task.events` 事件回写、由 API ingest 落库。

既有可复用链路：Worker `EventPublisher.publish_event(kind, payload, seq)`（confirms + `<run_id>:<seq>` 幂等键）；API ingest 单事务"事件落库 + 状态翻转"、未知 kind 仅持久化；Realtime Gateway 按 `event.#` 通配 fan-out；web Task Detail 对任意 `task:` frame 失效 task 查询。

## Goals / Non-Goals

**Goals:**
- 用户未显式提供 title 时，任务获得一条 LLM 生成的语义化标题，异步回写并实时反映到 Task Detail。
- 标题生成全程 best-effort：LLM 失败/超时不影响任务执行，占位标题兜底。
- 生成调用的成本计入既有 `cost.llm` 链路，无新 cost kind。

**Non-Goals:**
- 不做标题重命名 API、不在 iterate/rollback 时重新生成标题。
- 不做 Task List 页的标题实时推送（返回列表时 React Query 重取即可）。
- 不改 DB schema、`tasks.title` 约束、realtime-gateway 与 web 各规格。

## Decisions

### D1 触发方式：execute 消息显式 `gen_title` 标记，由 API 判定

`POST /api/v1/tasks` 在 title 为派生路径时向 execute outbox payload 写 `gen_title: true`。`gen_title` 是 **create-only 白名单语义**：payload 缺省即 `false`，唯一产生者是 create 的派生路径；其它 execute 产生方（iterate、rollback，以及未来若按 ARCHITECTURE §6.3 落地的 resume 重发）一律不设置该字段、无需契约改动 —— API 任何重发都不得重置 `gen_title: true`。

备选：Worker 按 `version_no==1` 自行判定 —— 会覆盖用户显式标题（execute 消息不含 title，Worker 无从比较），否决。显式标记让"谁需要生成"由唯一知情方（API）决定，Worker 无需感知标题语义。

### D2 生成时机：claim run 后、agent 调度前，同步一次，10s 超时；fresh-run 守卫防重复生成

消费者在发出 `status=running` 事件后、`ExecutionDispatcher.dispatch` 前同步调用标题生成，整体 10s 超时（`asyncio.wait_for`）。

调用前置条件（任一不满足即跳过，零 LLM 调用）：
1. `gen_title=true`；
2. **fresh-run 守卫**：`ctx.checkpoint_store.latest()` 为 `None`。crash 重投、stale-heartbeat 接管、attempt>1 等再消费路径均已有 checkpoint，不得重新生成（沿用 `worker-artifact-inheritance` 的 "skip when not fresh" 先例）。残余窗口：首个 checkpoint 写入前 crash 的重投会再生成一次，接受 —— ingest 侧 last-write-wins，标题最多翻面一次，多烧一次小模型调用；
3. `ctx.cancel_token` 未置位（cancel 后不再烧 LLM，计 skip 不计 failure）；
4. `AgentRegistry` 中存在该 `task_type` 的 agent（注定走 `unimplemented` → DLX 的消息不白烧调用）。

备选：与 agent 并发的 asyncio task —— 与 `RunContext` 的 cost_seq / event seq 计数器并发竞争，测试不确定，为省 ~1-2s 不值得；run 结束后生成 —— 标题在任务执行全程缺失，违背 UX 初衷。同步前置最简单且事件 seq 全程单调可断言。

### D3 模型与成本：`ModelFactory.get(title_model_key)` + `ctx.cost_meter` 回调

新增 Worker 配置 `WORKER_TITLE_MODEL_KEY`（未设置时回退**该消息 `task_type` 所注册 agent 的 model key**——D2 已保证此时 agent 必已注册，可直接取其 spec）。调用时挂 `ctx.cost_meter` 为 callback，成本自动以 `kind="llm"` 发出（resource_name 为实际模型名），不触碰 `CostEventPublisher`。禁止直接 import provider SDK（沿用 Model Factory requirement）。

### D4 生成输入与输出净化：双端截断，复用既有标题规则

- 输入：prompt 截取前 2000 字符送入模型（控成本、防长 prompt 撑爆上下文）；system 指令要求"用 prompt 同语言输出一行简洁标题，不加引号/句号，≤15 词"。
- 输出净化（纯函数，可单测）：取首个非空行 → 去包裹引号 → 折叠空白 → rune 边界截断，**最终串（含截断时追加的 `…`）≤64 rune 且 ≤200 字节**（与 ingest 侧口径一致，避免二次截断）→ 结果为空则**跳过发事件**（占位标题保留）。

### D5 事件契约：`kind="title"`，payload `{title}`，占用 run 正常事件 seq

经 `EventPublisher` 发出，routing key `event.<task_type>.title`，幂等键 `<run_id>:<seq>`。Gateway / web 零改动：通配 fan-out + frame 失效 task 查询天然生效。`docs/ARCHITECTURE.md §5.3` 的 WS kind 枚举与 execute 消息契约同步更新。

### D6 Ingest 落库：专用 Domain Service 方法，非状态机路径

ingest 对 `kind="title"` 新增分支：调用 `task.Service` 新方法 `ApplyGeneratedTitle(taskID, title)` —— trim、截断（含 `…` 后 ≤64 rune 且 ≤200 字节）、空串静默跳过 —— 与 `task_events` 落库同一事务。幂等边界要说清楚：`(run_id, seq)` 冲突（同一事件重投）整体 no-op；但接管进程**重新生成**的 title 事件会携带新 seq，`(run_id, seq)` 挡不住 —— 防重复生成靠 D2 的 fresh-run 守卫，ingest 对新 `(run_id, seq)` 的 title 事件一律 last-write-wins 正常落库（残余窗口下标题最多翻面一次，接受）。不做 terminal 守卫：任务即使已 `succeeded`（快任务），标题仍应更新。禁止裸 UPDATE，遵守 AGENTS 状态翻转约定（本方法非状态翻转，但同样收口在 Domain Service）。

### D7 上线顺序：Worker 先于 API

Worker 的 `TaskExecuteMessage` 解析必须将 `gen_title` 作为可选字段（缺省 `false`）且容忍未知字段，避免旧消息 / 新字段触发 poison-message DLX。部署顺序：先 Worker（认识 `gen_title`），后 API（开始发标记）。回滚只需回滚 API（停发标记），Worker 兼容停留。

## Risks / Trade-offs

- [每个新建任务多一次 LLM 调用的成本] → 输入截 2000 字符 + 可配置小模型；成本走 cost_meter 可观测，超预期可调 `WORKER_TITLE_MODEL_KEY`。
- [LLM 输出不可控（注入、空串、超长）] → D4 纯函数净化 + 截断 + 空串跳过；web 端标题始终按纯文本渲染。
- [标题事件晚于任务终态到达] → 接受，D6 明确不做 terminal 守卫。
- [首个 checkpoint 前 crash 的重投会重复生成一次标题] → fresh-run 守卫覆盖绝大多数再消费路径；残余窗口 last-write-wins、成本为一次小模型调用，接受（同 `worker-artifact-inheritance` 的处理思路）。
- [生成阻塞 run 启动最多 10s] → 仅 LLM 故障时达到上限，WARN + `worker_title_generation_failures_total` 计数后放行 agent，任务本体不受损。
- [strict 解析器遇到新字段触发 DLX] → D7 部署顺序 + 解析容忍未知字段写入 worker-messaging spec delta。
