# Review — add-worker-code-agent

整体评价：提案质量高，Why/What/Capabilities 边界清晰，design 的决策（D1–D11）有理有据，
spec 场景可测、tasks 切片合理。下面按严重程度整理改进意见，**带行号的均已对照当前
`worker/` 脚手架源码核实**。

---

## A. 与现有代码不符的事实性错误（建议落地前修正）

### A1. 插件目录路径写错（多处）
提案、impact、tasks 一律写成 `worker/plugins/tool/...`，但脚手架里插件实际位于
**`worker/worker/plugins/tool/`**（已存在 `worker/worker/plugins/tool/noop_tool/`，
`plugin.yaml` + `handler.py`）。
- 受影响处：proposal.md「What Changes」最后一条、Impact「Code」、tasks 6.1（`worker/plugins/tool/oss_fs/`）、tasks 7.1（`worker/plugins/tool/web_search/`）。
- 建议：统一改为 `worker/worker/plugins/tool/`，否则 loader 扫描不到，且与 §4.2 约定不一致。

### A2. `artifacts` 写入方法**已存在**，Open Q#1 / task 6.3 可直接结案
design「Open Questions #1」与 tasks 6.3 把"artifacts 行写入器"列为待定（新建方法 or `ArtifactStore`），
但 `worker/worker/core/persistence.py:349` 已经有 `insert_artifact(...)`，且 persistence 层已把
`artifacts` 标为 INSERT-only 白名单（`persistence.py:11,38`）。
- 建议：把 Open Q#1 从"待确认是否有列/方法"改为"复用现有 `Persistence.insert_artifact`"，
  task 6.3 改为"复用"而非"新增方法"，避免重复实现。

### A3. `artifacts` 列名与 spec/design 描述不一致
现有表/方法列为 `kind, oss_key, mime, bytes, sha256`（见 `insert_artifact`，`persistence.py:363`），
但 design Open Q#1 与 spec「Artifact Upload on Success」写的是 `type`、`size`、"hash"。
- 建议：spec/design 统一用真实列名（`kind` / `mime` / `bytes` / `sha256`），
  避免实现时再来回纠偏，也便于契约测试断言。

### A4. "checkpoint 幂等"与实际 store 行为相矛盾（重要）
spec 多处要求"write an **idempotent** checkpoint"，design D5/Risk 也称
"Checkpoints are idempotent on `(run_id, step_seq)` (existing store guarantee)，所以重投递可安全 re-run"。
但实际 `CheckpointStore.write` 对重复 `(run_id, step_seq)` **抛 `CheckpointConflictError`**
（`checkpoint.py:9,65,93`，`persistence.py:85`），并非静默幂等。
- 影响：crash-在-checkpoint-写入之后、ack-之前的重投递场景下，loop 若盲目重写同一 `step_seq` 会抛异常。
- 建议：
  1. 把 spec/design 的措辞从"idempotent checkpoint"改为准确表述：
     "store 对重复 step_seq 抛 `CheckpointConflictError`，loop 必须捕获并视作'该步已持久化'继续"；
  2. tasks 5.1/8.1 显式加一句：resume 时遇到 `CheckpointConflictError` 走"已完成"分支，不当作错误；
  3. 增加一条 spec 场景：重投递且当前 step 的 checkpoint 已存在 → 不重复执行、不报错。

---

## B. 设计层面需澄清/补强

### B1. 计划（plan）状态如何在 `step_seq=0` checkpoint 中恢复
D5 说"从 `step_seq=0` 还原 plan、不重跑 planner"。`CheckpointRecord` 有 `state: dict`（`checkpoint.py:30`），
可以承载 plan，但需要明确：plan 的序列化结构（步骤列表/各步状态）写在 tasks 里，
并在 5.2 与 8.1 间保持读写一致。建议在 design 增一小节定义 `step_seq=0` 的 state schema。

### B2. cancel 语义：`CancelToken` 无 `is_set()` 之外的等待用法
spec「Cooperative Pause and Cancel」要求 cancel 时 raise `asyncio.CancelledError`。
现有 `CancelToken` 提供 `is_set()`/`wait()`（`run_context.py`）。建议在 D4 伪代码里把
`ctx.cancel_token -> if set: raise` 明确为 `if ctx.cancel_token.is_set(): raise asyncio.CancelledError`，
与现有 API 对齐（当前伪代码 `ctx.cancel_token ->` 略含糊）。

### B3. 整体 vs 单步超时（Open Q#4）建议在本提案内定调
`deadline_ts` 已是消息/DB 既有字段（ARCHITECTURE §ll.643、§8.6 `total_timeout_min`）。
"whole-run check at each step boundary"是合理 MVP 选择，建议直接写进 spec 一条场景
（超 deadline → 在 step 边界 fail），而不是停留在 Open Question，避免实现时无依据。

### B4. 并发与单进程模型
spec「Per-run binding under concurrency」假设同一进程并发处理多条消息。
请确认 consumer 当前 prefetch/并发度（D2 的 per-run 构建论证依赖于此）。若 MVP 是单条串行消费，
该场景仍正确但优先级可下调，建议在 design 注明当前并发前提。

---

## C. tasks / 范围建议

### C1. 切片 A 的"trivial fake agent"与 task 4.4 闭环
task 4.4 用"trivial registered fake agent"打通 consumer success 路径很好——
这正好覆盖了脚手架里目前标注 `unreachable in scaffold` 的成功分支（`consumer.py:269`）。
建议在 4.4 明确断言：`mark_run_terminal(status="succeeded")` + `kind="status"` 事件 + `ack` 三者齐发。

### C2. `web_search` 在无网络 CI 下的形态
task 7.1 写"stub deterministic result acceptable"。建议明确：MVP 默认即 stub（不引入真实搜索依赖），
真实搜索作为后续 change，否则容易在实现时顺手引外部 SDK，违反"无网络 CI"目标与 §7 MVP 边界。

### C3. retry 计数的持久化
spec「Retry budget is bounded」+ task 5.4 用 `max_step_retries`。需澄清 retry 次数是否随 checkpoint 持久化——
若进程在第 k 次 retry 后崩溃重投递，resume 应从该 step 重新计数还是续算？建议 design 一句话定调
（建议：retry 计数不跨重投递持久化，重投递视为新预算，简单且 MVP 足够）。

### C4. 缺少"显式拆 loop 到独立文件"的决断
task 5.1 写"in `base.py` or `agents/loop.py`"。鉴于 loop 是本 change 的核心且要被两个 agent 复用，
建议直接定为独立 `agents/loop.py`，避免 `base.py` 过胖、也利于单测聚焦（design Risk #1 的 500 行约束）。

---

## D. 细节 / 文字

- D1. proposal「Configuration」与 design D9 的默认模型（`claude-opus-4-7` / `claude-sonnet-4-6`）
  已与 ARCHITECTURE §8.6（行 996/973）一致 ✓，无需改动，仅记录已核实。
- D2. spec「Agent Observability」的 outcome 枚举 `succeeded|failed|cancelled` 与 consumer 现有
  `messages_consumed_total{outcome=success|error|cancelled}` 标签命名不完全一致（`success` vs `succeeded`）。
  建议 agent 指标与 consumer 指标的 outcome 取值对齐，便于看板聚合。
- D3. proposal 提到 `cost.events` 新增 `kind=llm`/`tool` 由 wrapper 产生——确认这是既有 `CostMeter`
  行为（`cost_meter.py:41`），本 change 不改其形状，建议在 spec 注明"复用既有 kind，不新增 cost kind"，
  与"`task.events` 新增 plan/step"区分清楚，避免读者误以为也新增了 cost kind。

---

## 结论

无阻断性的方向问题；A 类（尤其 A1 路径、A4 幂等措辞、A2/A3 artifacts 复用与列名）建议在
`/opsx:apply` 前先回写到 proposal/design/spec/tasks，可显著减少实现期返工。B/C 多为澄清，
建议顺手补进 design 的 Decisions / Open Questions 收敛。
