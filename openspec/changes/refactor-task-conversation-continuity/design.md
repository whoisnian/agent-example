# Design: refactor-task-conversation-continuity

## Context

Iterate / rollback-branch 在数据层已有完整的"延续"链路：`task_versions.parent_id` 构成版本链，execute payload 携带 `parent_version_id`，Worker 把父版本产物 server-side copy 进新版本前缀（`worker-artifact-inheritance`）。但模型输入侧完全断裂：`run_agent_loop` 的 planner 只收到本次 `message.prompt`（`loop.py:194`），executor 输入是 `Overall task: {本次prompt}\nCurrent step: {title}`（`loop.py:259`），且是单次 JSON 调用、无读文件工具——父版本的 prompt、结论、产物内容对 LLM 全部不可见。

已有可复用的先例：`gen_title` → Worker 发 `kind=title` 事件 → API 事件消费端在同事务、幂等（`(run_id, seq)`）地回写 `tasks.title`。本设计把同一模式扩展到"版本结果摘要"，并以版本父链为对话历史的唯一事实来源。

约束（AGENTS.md §4.2 / §6）：Worker 不得直接写 `tasks` / `task_versions`；状态翻转必须经事件由 API 处理；Worker 只通过 MQ 与 API 交互。

## Goals / Non-Goals

**Goals:**
- Iterate / rollback-branch 的模型输入包含：本 task 的对话历史（各轮 prompt + 结果摘要）与继承产物的可见性（清单 + 有界内容节选）。
- 历史与 rollback 分支语义天然一致：从任一 base 版本继续，历史就是该版本的父链。
- 全链路兼容：新增字段/列均可选，新旧 API / Worker 可以任意顺序上线。
- 保持 MVP loop 的确定性（fake-model 测试不依赖网络、不引入工具调用循环）。

**Non-Goals:**
- 不引入 task 级可变共享产物目录（版本快照是 rollback 的基础，见 D5）。
- 不给 executor 增加 read_file/list_files 工具调用循环（Post-MVP，见 Open Questions）。
- 不做跨轮的滚动压缩摘要（rolling summary）；超出预算的早期历史直接截断。
- 不改前端契约；TaskDetail 展示 version summary 属可选跟进，不在本变更内。

## Decisions

### D1: 对话历史以版本父链为唯一事实来源（API 组装，payload 下发）

历史 = 从 base 版本沿 `parent_id` 走到根，取每个版本的 `(version_no, prompt, summary, status)`，旧→新排列，由 API 在 iterate / rollback-branch 构建 execute payload 时组装为 `history` 字段。`status` 携带版本终态（failed 版本是合法的 base 与链节点），Worker 渲染时给非 succeeded 轮显式标记，避免模型把失败轮当成功前置。history 在写 outbox 行时一次性组装定格；同 version 的任何重发（retry / redelivery / 未来 §6.3 resume 重发）复用原 payload，不重组、不丢弃。

- **为什么不是独立的对话存储（如 task 级 history.jsonl / 新表）**：父链天然分支感知——rollback-branch 选了 v2 做 base，链就是 v1→v2，v3 的"被回滚分支"自动不出现在历史里；独立存储则需要在回滚时做截断/分叉维护，引入第二份需要同步的事实。且 Worker 追加写历史会触碰"Worker 不写业务状态"红线。
- **为什么 API 组装而不是 Worker 自查 DB**：Worker 与 API 的契约是 MQ payload（AGENTS.md §2 边界）；Worker 读业务表会建立新的隐式耦合。payload 体积风险由 D3 的上限约束。

### D2: 版本结果摘要走 `kind=summary` 事件回写 `task_versions.summary`

Worker 在 run 成功收尾时（artifact 上传之后、返回之前）发出一条 `kind=summary` 的 `task.events` 事件，payload 为 `{summary}`；API 事件消费端在与 `task_events` 插入相同的事务中、幂等地写入新增的 `task_versions.summary`（可空 TEXT），规则完全对齐 `kind=title`（去重 `(run_id, seq)`、空值跳过但事件行仍落库、不受任务终态门控、长度截断）。

- **摘要内容如何产生**：由 loop 对各步 executor 的 `summary` 做确定性拼接（`step_seq. summary` 按行连接），整体在 rune 边界截断至 ≤ 2048 字节。**不增加额外 LLM 调用**——保持 fake-model 测试确定性、零额外成本；步骤摘要本来就是 critic 评审过的产物描述。备选"用一次廉价 LLM 调用生成摘要"留作 Post-MVP 优化（与 gen_title 同路径）。
- **resume 安全**：当前 checkpoint 的 `_plan_state` 只存 `{idx, title, done}`，attempt>1 时前序步骤摘要不在内存中。因此每步 checkpoint 的 plan-state 条目须记录该步的 executor summary（沿用既有 `_SUMMARY_CAP=500` 截断），resume 后从恢复的 checkpoint 重组全量步骤摘要（含前一 attempt 执行的步骤）。
- **为什么不是 Worker 直写列**：红线（§4.2 / §6）；事件模式还免费获得重投幂等与乱序容忍。
- **被否决的更简替代——API 侧聚合 step 事件**：`kind=step` 事件 payload 已携带 500 字符截断的 `summary` 且逐步落库，理论上 iterate 时可由 API 聚合 `task_events` 免去新事件与新列。否决理由：历史组装需对链上最多 20 个版本各解析"最近一次成功 run"的全部 step 事件（多行 JSONB 解析 vs 单列读取），把 iterate 写路径耦合到事件 payload 形态；且 step 摘要会随事件保留策略（未来归档/清理）失效，而 `task_versions.summary` 是版本的长期属性。D7 修复 seq 语义后，事件方案的可靠性顾虑也已消除，列方案胜在读取 O(1) 与契约清晰。
- 失败的 run 不发 summary 事件：error 事件已携带失败信息；历史组装对 `summary IS NULL` 的版本降级为只含 prompt 的条目（见 D3）。
- 读侧暴露：`GET /api/v1/versions/{id}` 的规格措辞是 "full version row"，新列随行自然暴露，`task-read-api` 无需 delta；realtime-gateway 的 frame 契约明文枚举事件 kind 列表，需 delta 把 `summary` 加入枚举（透传行为本就不拒未知 kind，仅为枚举保鲜）。

### D3: 历史有界：≤ 20 轮、总预算 16 KiB、旧端截断

组装规则（API 侧，常量集中定义）：
- 沿父链最多读 **20** 个版本（这是 DB 遍历深度上限，不是保底轮数）；每条 `summary` 在 rune 边界截断至 ≤ **1024 字节**；`prompt` 同样 ≤ **1024 字节**。
- 序列化后的 `history` 总体 > **16 KiB** 时，从最旧端整条丢弃直至达标（保留最近上下文优先）。注意算术含义：满额轮 ~2.2 KiB，16 KiB 实际保留最近 ~7 个满额轮；短 prompt/summary 的链才可能保满 20 轮。16 KiB 是权威上限，20 只约束 DB 走链开销。
- `summary` 为 NULL（失败版本、本变更上线前的存量版本、摘要事件尚未消费）时，条目照常进入历史，`summary` 置 null——prompt 链本身已是最有价值的连续性信号。
- create 路径 `history` 恒为空（省略字段，解析端缺省空列表，沿用 `gen_title` 的"缺省即假"白名单语义）。

约束动机：outbox payload 是 JSONB 行 + MQ 消息，必须有硬上限；16 KiB 的量级对 LLM 上下文与 MQ 都安全。

### D4: Worker 注入——conversation-context 块进 planner 与 executor 输入

`TaskExecuteMessage` 增加可选 `history: list[HistoryTurn]`（缺省 `[]`，解析器容忍缺失与未知字段，不构成 poison）。loop 在组装角色输入前构建一个 conversation-context 文本块：

1. **对话历史段**：按旧→新渲染每轮 `[v{version_no}] user: {prompt}` / `result: {summary|（无摘要）}`；`status != succeeded` 的轮带显式失败/取消标记；
2. **继承产物段**（仅当本 run 发生了产物继承）：全部继承文件的 `path (size)` 清单；对小型文本产物（按 MIME/扩展名判定，单文件 ≤ 8 KiB）附内容节选，节选总预算 ≤ 24 KiB——入选顺序确定：按字节大小升序、同大小按路径字典序，预算耗尽后其余只列清单。清单以 `inherit_parent_artifacts` 实际复制返回的 key 集为准（内容经 `ctx.oss_client` 读取），**不得**重新 list 本 run 前缀——那会把本 run 自身的输出误计为继承产物。

**组装一次、随 checkpoint 持久化**：context 块在 fresh run 继承完成后、planning 之前组装一次，存入 `step_seq=0`（plan）checkpoint 的 state；resume（存在 checkpoint）时从恢复的 state 取用，不重组、不重读 OSS。这同时解决两个 resume 缺陷：继承在 resume 时被跳过导致清单无来源，以及重新 list 会混入上一 attempt 的输出。大块 checkpoint 由既有的 OSS offload（`checkpoints/<n>.bin`）承接。

注入位置：planner 输入 = context 块 + 本次 prompt；executor 输入的 `Overall task:` 前同样前置 context 块。critic 输入不变（它只评审步骤结果）。`history` 为空且无继承时，context 块整体省略，行为与现状逐字节一致——这是兼容性的关键。

- **为什么不给 executor 加读文件工具**：MVP loop 是"每角色单次 JSON 调用"的确定性设计（`worker-agent-orchestration` 的 D2b 决策）；引入工具循环改变 checkpoint/事件节奏与测试基座，属另一个变更。
- **为什么内容节选而非全量**：上下文与成本预算；大文件由清单告知存在，模型可在输出中选择覆盖或保留。

### D5: 产物目录保持"每版本快照 + 物理继承"，单一目录是逻辑视图

不引入 task 级可变工作目录。rollback-switch 需要任意历史版本的产物原样可取，rollback-branch 需要从任意版本分叉——两者都依赖版本前缀的不可变快照。"同一 task 一份产物目录"在本设计中成立于逻辑层：任一版本前缀 = 该时点的目录全量状态（继承保证父文件齐全，agent 输出覆盖其上），版本链即目录的演进历史。备选"单一可变目录 + 写时快照"被否决：需要自建快照/GC 机制，且与现有 `worker-artifact-inheritance` 规格冲突，MVP 收益为零。

### D6: 部署顺序无关；非法 history 降级而非毒化

- 旧 Worker + 新 API：payload 多出 `history` 字段——Worker 解析器本就"容忍未知字段"（`worker-messaging` 既有要求），忽略之，行为同现状。
- 新 Worker + 旧 API：payload 无 `history` → 缺省空列表 → context 块省略；Worker 发出的 `kind=summary` 事件在旧 API 落 `task_events` 行（kind 未识别时仅记录，不翻转状态，`task-event-ingest` 既有行为）。
- DB 迁移（加可空列）先行，无锁风险。
- **结构非法的 `history` 不按 poison 处理**：降级为空列表 + warning 日志 + `worker_invalid_history_total` 指标，run 以无上下文方式照常执行。理由：history 是增强信号，缺失只是回到现状行为；若按 poison 进 DLX，API 组装端一个 bug 会让所有 iterate 消息全军覆没——可用性代价远大于静默降级，且指标保证降级可见。信封本身解析失败仍维持既有 poison→DLX 语义。

### D7: resume 的事件 seq 与摘要可重组（修复既有缺陷）

现状缺陷（本设计依赖其修复）：`RunContext.event_seq` 每次投递从 0 起算（`run_context.py:109`），而事件落库以 `(run_id, seq)` 幂等去重——resume 后新 attempt 发出的事件 seq 与 attempt 1 已落库的冲突，被 ingest 静默丢弃（同进程接管则直接触发 publisher 的 non-increasing seq 异常）。这影响 resume 后的**所有**事件（step/artifact/status），并非 summary 独有，但 summary 事件在 run 末尾发出，受影响概率最高。

修复：每次写 checkpoint 时把当前事件 seq 高水位记入 checkpoint state；resume 恢复 checkpoint 后、发出任何事件前，用高水位初始化 `event_seq`。配合 D2 的"步骤摘要入 checkpoint"，resume 后的 run 既能续上事件序列，又能重组全量摘要。fresh run 行为不变（无 checkpoint → seq 从 1 起）。该修复落在本变更内是因为 summary 事件的正确性直接依赖它；它同时顺带修复了 step/artifact 事件在 resume 后丢失的存量问题。

## Risks / Trade-offs

- [历史被截断后丢失早期意图] → 旧端截断保最近上下文；prompt 条目优先保留（截 summary 先于丢整条不做——规则保持简单：超预算丢整条最旧轮）。Post-MVP 以滚动摘要补足。
- [步骤摘要拼接质量不稳，误导后续轮次] → summary 仅是辅助信号，产物清单/节选提供第二信息源；后续可无缝替换为 LLM 生成摘要（事件契约不变）。
- [context 块注入推高输入 token 成本] → 上界封套：context 块 ≤ 16 KiB（history）+ 24 KiB（节选）≈ 40 KiB ≈ 12k tokens，注入 planner 1 次 + executor 每步 1 次；10 步 run 新增输入 ≈ 130k tokens 上界（典型远低于此：history 与节选很少同时打满）。成本经 `cost_meter` 全程计量可见；预算常量集中可调。Post-MVP 可做 executor 侧瘦身（仅首步全量、后续步只带清单）。
- [payload 体积膨胀拖慢 outbox/MQ] → D3 硬上限（16 KiB）+ 集中常量便于调参；监控可复用 outbox 既有指标。
- [继承产物节选读取增加 run 启动延迟] → 预算上限（24 KiB）+ 仅小文本文件；OSS 读取在 fresh run 内串行一次（resume 走 checkpoint 不重读），量级毫秒级。
- [summary 事件晚于下一次 iterate 到达（快速连环迭代）] → 历史条目降级为 prompt-only，不阻塞 iterate；与 title 事件"迟到仍应用"的既有取舍一致。
- [checkpoint state 体积增大（context 块 + 步骤摘要 + seq 高水位）] → context 块 ≤ 40 KiB、步骤摘要每条 ≤ 500 字符，超阈值的 checkpoint 本就走 OSS offload（`checkpoints/<n>.bin`），DB 行不膨胀。

## Migration Plan

1. 迁移：`task_versions` 加可空 `summary TEXT` 列（含 sqlc 重新生成）。
2. 上线 API：事件消费端支持 `kind=summary`；iterate / rollback-branch payload 组装 `history`。
3. 上线 Worker：解析 `history`、注入 context 块、成功收尾发 `kind=summary`。
4. 回滚策略：任一组件回退均安全（字段/列可选、事件 kind 未识别仅落库），无数据回填需求；存量版本 `summary` 为 NULL，按 D3 降级。

## Open Questions

- executor 读文件工具（替代内容节选注入）与滚动压缩摘要均明确 Post-MVP，待本变更落地后按实际效果评估，不阻塞本设计。
