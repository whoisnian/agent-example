# 评审意见: refactor-task-conversation-continuity

> 评审基线：proposal.md / design.md / specs/ 全部 7 份 delta / tasks.md，对照 `openspec/specs/` 现行规格、`docs/ARCHITECTURE.md` §5.2/§5.3/§6.3/§6.4 与 `api/internal/domain/task/`、`worker/worker/` 现状代码。

---

## [P1] 必须修复

### P1-1: resume/redelivery 路径下 `kind=summary` 事件会被静默丢弃（或触发 publisher 异常），tasks.md 4.3 的前提不成立

- **所在文件**：`tasks.md` 4.3、`specs/worker-agent-orchestration/spec.md`（"Run Summary Event"）、`design.md` D2
- **问题**：tasks.md 4.3 写"确认 resume 路径不重复发 summary（事件 seq 单调由既有 runtime 保证）"。这个前提是错的：`event_seq` 是 `RunContext` 字段、初值 0（`worker/worker/core/run_context.py:108`），而 `RunContext` 在每次消费消息时全新构造（`worker/worker/core/consumer.py:220` 起），没有任何跨 attempt 的 seq 高水位恢复。后果分两种：
  - **跨进程重投**：attempt 2 从 seq=1 重新计数，其 step/summary/最终 status 事件的 `(run_id, seq)` 与 attempt 1 已落库的事件冲突，被 ingest 的 `ON CONFLICT (run_id, seq) DO NOTHING`（`task-event-ingest` "Idempotent event persistence"）当作重复**静默丢弃**。resume 的 run 执行的步骤数通常少于 attempt 1 已发的事件数，所以收尾的 summary 事件几乎必然落入已占用的 seq 区间——`task_versions.summary` 永远写不上，且无任何报错。
  - **同进程重投**：`_SeqRegistry.admit` 记住了该 `run_id` 的最大 seq（`worker/worker/core/publisher.py:36-49`），重发 seq=1 直接抛 contract violation，run 失败。
- **依据**：上述代码位置；delta 要求 "exactly one `kind=summary` event ... MUST be emitted"，但在 resume 场景该要求与既有 dedup 机制正面冲突。（注：这其实是既有 runtime 的隐患——resume 后连 `status=succeeded` 事件都可能被吞——但本提案把一个关键回写建在这条断裂的链路上，并在 tasks.md 中写下了错误断言，必须在本变更内澄清。）
- **建议**：三选一并写进 design/spec：(a) resume 时从 DB/checkpoint 恢复 seq 高水位（如 `MAX(seq) FROM task_events WHERE run_id=?` 或在 checkpoint state 里记录 last_seq）；(b) 在 design Risks 显式接受"resume 后 summary 可能丢失，降级为 prompt-only 历史"，同时改写 tasks.md 4.3 的断言与测试预期；(c) 把 summary 回写改为不依赖新事件（见 P3-12）。当前文字状态下 4.3 的"补一条 redelivery 测试"按 spec 预期是写不绿的。

### P1-2: resume 后 summary 的内容契约无法满足——前序步骤的 executor summary 已不在内存

- **所在文件**：`specs/worker-agent-orchestration/spec.md`（"Run Summary Event"）、`design.md` D2
- **问题**：spec 要求 `payload.summary` 是 "a deterministic concatenation of **the run's** per-step executor summaries (one line per step)"。但 `attempt_no > 1` 时 attempt 1 已完成步骤的 summary 只存在于各步 checkpoint 的 `state.current.result_summary`（且被 `_SUMMARY_CAP = 500` 截断，`loop.py:91,145`），而 `CheckpointStore` 只暴露 `latest()`（`worker/worker/core/checkpoint.py:103`），没有读全链 checkpoint 的 API。resume 的 run 在内存里只有本 attempt 执行的步骤，无法拼出"整个 run"的摘要。
- **依据**：`loop.py` `_load_or_create_plan` 恢复 plan 与 `ctx.step`，不恢复历史步骤 summary；spec scenario "Successful run emits one summary event" 假设 3 步 summary 全部可得。
- **建议**：在 requirement 中明确二选一：摘要只覆盖"本 attempt 实际执行的步骤"（接受 resume 后摘要不完整，加一个 resume scenario 固定语义）；或要求从 checkpoint 链重建（需给 CheckpointStore 增加按 run 列取的能力，工作量进 tasks.md）。

### P1-3: `task-conversation-history` 两个 scenario 缺少 WHEN，违反 spec 格式约定

- **所在文件**：`specs/task-conversation-history/spec.md`
- **问题**：Scenario "History follows the parent chain of the base version" 与 "Rollback-branch history excludes abandoned branches" 只有 **GIVEN**/**THEN**，没有 **WHEN**。OpenSpec scenario 至少要 WHEN/THEN 成对（同文件第三个 scenario "Missing summary degrades..." 即是正确写法），归档校验可能直接拦下。
- **建议**：补 `- **WHEN** history is assembled for the new version` 一类的 WHEN 行；提交前跑一遍 `openspec validate refactor-task-conversation-continuity --strict`。

---

## [P2] 建议改进

### P2-1: resume/takeover 时继承产物 inventory 的口径未定义，且"经 oss_client 重新 list"会误计上一 attempt 的残留输出

- **所在文件**：`specs/worker-agent-orchestration/spec.md`（"Conversation Context Injection"）、`tasks.md` 5.2、`design.md` D4
- **问题**：两点。(1) `LoopAgent._maybe_inherit_parent_artifacts` 在存在 checkpoint 时整体跳过继承（`worker/worker/agents/base.py:193`），而 delta 把 inventory 的触发条件写成 "only when inheritance copied at least one object"——于是 attempt 1 的 executor 看得到清单/节选，attempt 2（resume/stale-heartbeat takeover）看不到，同一 run 的角色输入跨 attempt 不一致，spec 对此只字未提。(2) "Inventory data MUST be obtained through `ctx.oss_client` after inheritance completes" 若实现为重新 list 新版本前缀，在 crash-before-first-checkpoint 的重投场景（`worker-artifact-inheritance` 明示存在该窗口）会把 attempt 1 已写出的部分 agent 产物误标为"继承产物"。
- **建议**：(1) 在 requirement 中显式规定 resume 行为（建议接受降级：resume 不重建 inventory，写明理由）；(2) 清单来源改为 `inherit_parent_artifacts` 的返回值——让它返回 `(key, size)` 列表而非 count（`worker/worker/agents/inherit.py:42` 处 list 结果现成），避免二次 list 与误计。

### P2-2: history turn 形状无法区分"失败的版本"，可能误导模型

- **所在文件**：`specs/task-conversation-history/spec.md`、`design.md` D3
- **问题**：turn 形状是 `{version_no, prompt, summary}`；失败的 run 不发 summary（D2），其 turn 的 `summary = null` 与"摘要事件尚未消费/存量版本"不可区分。failed 是终态，可以作为 iterate base，父链中完全可能出现失败版本——模型会把该轮 prompt 当作"已被执行成功的前置诉求"。Worker 渲染端也只规定 "an explicit 'no summary' marker"，无法表达"这一轮失败了"。
- **建议**：turn 增加可选 `status` 字段（或至少 `failed: true`），渲染时标注；若决定不加，请在 design Risks 写明"失败轮与无摘要轮同形"的接受理由。

### P2-3: context 块逐步注入的 LLM token 成本未被论证

- **所在文件**：`design.md` D4 / Risks
- **问题**：context 块上限 = history 16 KiB + 节选 24 KiB ≈ 40 KiB（约 1 万 token），按 D4 既注入 planner、又前置到**每个** executor step 输入。一个 20 步的 plan 一个 run 就多出约 20 万输入 token。Risks 只讨论了 outbox/MQ 体积与 OSS 读延迟，唯一逐 call 复制的大头成本反而没出现。
- **建议**：要么差异化注入（planner 全量；executor 仅 history 或仅清单，不带节选），要么在 Risks 补量化论证并说明接受；预算常量按角色拆开命名（如 `PLANNER_CONTEXT_BUDGET` / `EXECUTOR_CONTEXT_BUDGET`），方便后续调参。

### P2-4: "structurally invalid `history` → poison" 与 design 表述不一致，且对辅助字段过于致命

- **所在文件**：`specs/worker-messaging/spec.md` 与 `design.md` D4
- **问题**：delta 规定非法 `history` 视为 poison → DLX；design D4 只说"解析器容忍缺失与未知字段，不构成 poison"，未提非法即毒，两处文字未对齐。语义上：history 是纯辅助上下文（缺失时本就降级为空），API 组装侧一个序列化 bug 会让**所有** iterate/rollback-branch 消息进 DLX、任务全部卡死并需要人工重放——故障半径远大于字段的价值。
- **建议**：权衡改为"解析失败时降级为空列表 + `worker_invalid_history_total` 计数 + warning 日志"（与 summary 缺失的降级哲学一致）；若坚持 poison（理由：尽早暴露契约 bug），请在 design D4 写明取舍并对齐两处文字。

### P2-5: `kind=summary` 会流经 Realtime Gateway，但 realtime-gateway spec 的 kind 枚举未更新（缺 delta）

- **所在文件**：缺失的 `specs/realtime-gateway/spec.md` delta；连带 `task-read-api`
- **问题**：`openspec/specs/realtime-gateway/spec.md` 明文写 "the currently emitted kinds are `status`, `log`, `plan`, `step`, `artifact`, `error`, and `title`"。gateway 行为上不拒绝未知 kind，不会坏，但该枚举在归档后即过时；ARCHITECTURE §5.2 的同款枚举有 task 6.1 覆盖，spec 这句没有。另外 `task-read-api` 的 `GET /api/v1/versions/{id}` 定义为返回 "the full version row"——`summary` 列加入后它是否随之暴露，提案未表态（proposal 说 web 展示可选，但读接口形状是契约问题，不是展示问题）。
- **建议**：补一个一句话的 realtime-gateway delta 更新枚举；在 proposal Impact 或 task-read-api delta 中明确 version 读接口是否（以及何时）暴露 `summary`。

### P2-6: §6.3 resume 重发 execute 的路径未纳入 history producer 约束

- **所在文件**：`specs/task-conversation-history/spec.md`（"History Field in the Execute Payload"）
- **问题**：ARCHITECTURE §6.3 规定 pause→resume 时 "API 重新 publish execute message"。delta 只要求 "producers that derive a new version from a base (iterate, rollback-branch)" 填 history。将来 resume 路径重发一条 iterate 版本的 execute 时，按现契约可以不带 history——恢复的 run 静默失去对话上下文。现行 `task-write-api` spec 对 `gen_title` 专门写了 republish 行为（"any future API republish such as the §6.3 resume path ... simply never set the field"），本字段恰好需要相反的约定，更应写明。
- **建议**：在该 requirement 加一句：任何为带 `parent_id` 的版本重发 execute 的 producer SHALL 按同一规则重新组装 `history`（按重发时点的数据，允许与原消息不同）。

---

## [P3] 可选优化

### P3-1: D3 常量算术——16 KiB 总上限使"20 轮"在满额场景下名存实亡

- **所在文件**：`design.md` D3
- **问题**：满额一轮 ≈ 1024B prompt + 1024B summary + JSON 包装 ≈ 2.2 KiB，20 轮 ≈ 44 KiB，16 KiB 上限先生效，实际只保 ~7 轮。design 的论证句"20 轮 × ~2 KiB 的量级对 LLM 上下文与 MQ 都安全"与自己的 16 KiB 上限矛盾（按它自己的算术，20 轮满额恰恰不会被保留）。
- **建议**：不必改常量，但把论证改准确："20 轮上限服务于轮次较短的常态；满额轮按 16 KiB 截到约 7-8 轮"。

### P3-2: 节选文件的入选顺序未定义，"deterministic" 不闭合

- **所在文件**：`specs/worker-agent-orchestration/spec.md`（"Conversation Context Injection"）
- **问题**：要求 context 块 "assembled deterministically"，但 24 KiB 预算内**哪些**小文本文件入选、按什么顺序填充没有规定（OSS list 顺序是实现细节）。两次组装可能选中不同文件集合。
- **建议**：规定如"按 path 字节序升序填充至预算"。

### P3-3: 更简替代方案未讨论——步骤摘要已在 `task_events` 里

- **所在文件**：`design.md` D2
- **问题**：loop 每步发出的 `kind=step` 事件 payload 本就携带 `summary`（500 字符截断，`loop.py:157`）并已幂等落库。理论上 API 可在组装 history 时直接聚合该版本成功 run 的 step 事件摘要，完全省掉新事件 kind、新列、worker 改动，且天然免疫 P1-1 的 resume 丢失问题（step 事件逐步落库）。代价是 iterate 时多一次按 version 聚合查询、无去范式化列。design 的备选清单（独立存储、LLM 摘要）没有覆盖这个最近的替代。
- **建议**：在 D2 增补该备选及否决理由（如：读放大、step 摘要粒度太细、希望 summary 形状独立演进）；若 P1-1 决定走"接受丢失"路线，此方案值得重新权衡。

### P3-4: tasks 6.1 文档小节指向不准

- **所在文件**：`tasks.md` 6.1
- **问题**："§5.1（如有数据模型小节）补 `task_versions.summary`"——`task_versions` 的 DDL 在 ARCHITECTURE **§4.2 关键表 DDL**（约 L303 起），§5.1 是 REST API。
- **建议**：改为明确指向 §4.2 的 `task_versions` DDL 与 §4.1 实体关系（如涉及）。

### P3-5: worker 侧可观测性无任务项

- **所在文件**：`tasks.md` §4/§5
- **问题**：AGENTS.md §7 要求"每新增一条状态翻转或外部调用，至少同步加一个 metric/log 字段"。3.4 只覆盖了 API 写端；worker 侧的 context 组装（节选字节数/文件数/是否截断）与 summary 事件发出（成功/失败）没有对应 metric/log 任务（对照 title 路径有 `title_generation_failures_total` 的先例）。
- **建议**：在 5.1/4.2 中各加一句结构化日志字段与计数器要求。

### P3-6: rollback delta 原样延续了 `parent_artifact_root` 的既有误导性表述

- **所在文件**：`specs/task-rollback-api/spec.md`（"Branch Mode..."）
- **问题**：MODIFIED 文本保留了原文 "(carrying the target version's `artifact_root` as the parent artifact root)"，而 ARCHITECTURE §6.4 与 `worker-artifact-inheritance` 明确该列**无写者、API 实际恒发 null**、Worker 收到非空值要告警（`worker/worker/agents/base.py:186-190`）。这是既有矛盾，按"MODIFIED 须完整复制原文"的规则原样保留没有错，但本提案把 history 写进同一个括号里，会让新读者以为两者同样"真实生效"。
- **建议**：本变更不必修它，但建议在 design Context 加一行脚注说明 `parent_artifact_root` 的现状，或登记一个独立的勘误 change。

---

## 总评

提案整体质量较高：问题定位准确（`loop.py:194/259` 的断裂点描述与代码完全相符）、以版本父链为唯一事实来源的 D1 决策干净利落地解决了 rollback-branch 分支语义、复用 title 事件回写模式没有触碰任何 Worker/API 边界红线、MODIFIED requirement 均完整复制原文后修改、新旧混布论证（D6）经代码核实成立（`extra="ignore"` 与 ingest 的 unknown-kind 落库行为均已验证）。

主要风险集中在 **resume/redelivery 路径**：提案对"事件 seq 单调由 runtime 保证"的假设与现状代码不符（P1-1），summary 这个关键回写恰好落在 run 收尾、是最容易被 seq 冲突吞掉的事件；连带 resume 时摘要内容（P1-2）与继承 inventory（P2-1）的口径都未定义。建议在进入 apply 前先把"resume 语义"作为一节补进 design（明确恢复 seq 高水位还是接受降级），其余意见多为局部修订，不影响整体方案成立。

---

## 处理结论（逐条核验后）

核验方式：对照 `worker/worker/core/run_context.py`、`publisher.py`、`agents/loop.py`、`agents/base.py` 及 `openspec/specs/` 原文逐条验证；14 条采纳、1 条不采纳。

| 意见 | 结论 | 落点 |
|---|---|---|
| P1-1 seq 重计吞事件 | **采纳**（代码证实：`run_context.py:109` event_seq 每 RunContext 从 0 起；`publisher.py` _SeqRegistry 拒非递增） | 新增 design D7 + spec "Resume-Safe Event Sequencing" + tasks 任务组 4 |
| P1-2 resume 摘要拼不出 | **采纳**（证实：`_plan_state` 只存 idx/title/done） | 步骤摘要入 checkpoint plan-state（D2 / spec Run Summary Event / tasks 4.2） |
| P1-3 scenario 缺 WHEN | **采纳** | task-conversation-history 两处 scenario 补 WHEN |
| P2-1 resume inventory 口径 | **采纳** | context 块组装一次、随 plan checkpoint 持久化，resume 恢复不重 list（D4 / spec / tasks 6.2）；清单以 inherit 复制 key 集为准 |
| P2-2 失败版本不可区分 | **采纳** | history turn 增加 `status` 字段，渲染带失败标记（D1/D3/各 spec） |
| P2-3 token 成本未论证 | **采纳** | design Risks 补上界封套（≈130k tokens/10 步 run 上界）与 Post-MVP 瘦身方向 |
| P2-4 非法 history 毒化过重 | **采纳** | 降级为空列表 + warning + `worker_invalid_history_total`（D6 / worker-messaging delta） |
| P2-5 realtime-gateway 枚举缺 delta | **采纳** | 新增 specs/realtime-gateway delta（kind 枚举加 `summary` + 透传 scenario）；task-read-api 经核验无需 delta（"full version row" 措辞自然覆盖新列，已记入 D2） |
| P2-6 §6.3 重发路径 | **采纳** | history 随 outbox 行一次性定格、重发复用原 payload（spec "History Field" + D1） |
| P3-1 16 KiB 与 20 轮算术 | **采纳** | D3 改为"20 是走链深度上限，16 KiB 是权威上限（满额轮约保 7 轮）"；spec scenario 限定短轮次前提 |
| P3-2 节选顺序未定义 | **采纳** | 按字节升序、同大小路径字典序（D4 / spec / tasks 6.1） |
| P3-3 更简替代未讨论 | **采纳**（论证后维持原方案） | D2 增"被否决的替代——API 侧聚合 step 事件"及否决理由（O(1) 读取、不耦合事件 payload 形态、不受事件保留策略影响） |
| P3-4 文档小节指向 | **采纳** | tasks 7.1 改指 §4.2 |
| P3-5 worker 可观测性缺任务 | **采纳** | tasks 6.5 |
| P3-6 parent_artifact_root 措辞 | **不采纳**：该表述是 task-rollback-api 现有 spec 原文，MODIFIED delta 必须完整复制原文，本变更不应顺手改无关措辞（AGENTS.md §6"不夹带无关重构"）；可另起小型 docs/spec 勘误变更处理 |
