# Tasks: refactor-task-conversation-continuity

## 1. 数据模型与迁移（api/）

- [x] 1.1 新增迁移：`task_versions` 加可空 `summary TEXT` 列（up/down 双向干净），更新 schema 注释引用 `kind=summary` 事件
- [x] 1.2 sqlc：重新生成 `task_versions` 相关查询；新增"沿 parent_id 取父链（含 version_no/prompt/summary/status，限深 20）"查询（递归 CTE 或循环单查，常量集中定义）

## 2. API：summary 事件落库（api/）

- [x] 2.1 Domain Service 新增 `ApplyVersionSummary`：trim + rune 边界截断（≤2048 字节含 `…`）、空值跳过；禁止 ad-hoc UPDATE
- [x] 2.2 `event_sync.go` 接入 `kind=summary`：与 `task_events` 插入同事务，`(run_id, seq)` 重复时不重放，不受终态门控（对齐 `kind=title` 规则）
- [x] 2.3 单测 + 集成测试：正常应用 / 终态后仍应用 / 重投不重放 / 空 payload 跳过但事件行落库

## 3. API：history 组装进 execute payload（api/）

- [x] 3.1 实现历史组装器：沿 base 版本父链取轮次（旧→新，含 `status` 终态字段），逐字段 1024 字节 rune 截断、走链深度 ≤20、序列化 >16 KiB 时旧端整条丢弃；NULL summary 降级为 prompt-only 轮
- [x] 3.2 `payload.go`：execute payload 增加可选 `history` 字段；`service.go` iterate 路径与 `rollback_service.go` branch 路径调用组装器填充；create 路径不带该字段；history 随 outbox 行一次性定格（重发复用原 payload）
- [x] 3.3 单测 + 集成测试：v1-only 单轮历史 / 多轮链顺序 / failed 版本携带 status / rollback-branch 排除被弃分支 / 深链与超大 summary 的边界截断 / outbox payload JSON 形态契约
- [x] 3.4 写端可观测性：组装耗时与丢弃轮数的结构化日志字段（带 trace_id/task_id）

## 4. Worker：resume 事件序列修复（worker/，前置依赖）

- [x] 4.1 checkpoint state 增加事件 seq 高水位：每次 `_safe_checkpoint` 写入当前 `event_seq`；resume 恢复 checkpoint 后、发任何事件前用高水位初始化 `RunContext.event_seq`；fresh run 行为不变
- [x] 4.2 checkpoint plan-state 条目记录各完成步骤的 executor summary（沿用 `_SUMMARY_CAP=500` 截断），resume 后可重组全量步骤摘要
- [x] 4.3 redelivery 测试：attempt 1 完成 2 步（发事件至 seq=N）后崩溃 → attempt 2 resume 后续发事件 seq>N、ingest 不丢弃；恢复的 plan-state 含前序步骤摘要

## 5. Worker：消息解析与 summary 发出（worker/）

- [x] 5.1 `core/messages.py`：`TaskExecuteMessage` 增加 `history: list[HistoryTurn]`（`{version_no, prompt, summary|null, status}`，缺省空）；结构非法的 history 降级为空列表 + warning 日志 + `worker_invalid_history_total` 指标（不毒化）；单测覆盖缺省 / 合法 / 非法降级三态
- [x] 5.2 loop 成功收尾发 `kind=summary` 事件：从 checkpoint state 重组全量步骤摘要按 `step_seq. summary` 行拼接、rune 截断 ≤2048 字节；失败/取消不发；artifact 上传之后、返回之前发出；fake-model 单测验证事件内容与时序
- [x] 5.3 resume 端到端测试：跨 attempt 的 run 成功后，summary 事件包含两个 attempt 各自执行步骤的摘要，且 seq 续接（依赖任务组 4）

## 6. Worker：conversation-context 注入（worker/）

- [x] 6.1 实现 context 块组装器：历史段（旧→新渲染，null summary 显式标记，非 succeeded 轮带失败/取消标记）+ 继承产物段（清单以 `inherit_parent_artifacts` 返回的复制 key 集为准，path/size 全列；≤8 KiB 文本文件内容节选，按大小升序、同大小路径字典序入选，总预算 24 KiB），全程确定性、无 LLM 调用
- [x] 6.2 context 块在 fresh run 继承完成后组装一次，存入 `step_seq=0`（plan）checkpoint state；resume 从恢复的 state 取用，不重 list OSS、不重读内容；与 `LoopAgent.run` / `inherit_parent_artifacts` 的衔接（返回值需从 count 改为复制 key 列表）
- [x] 6.3 `loop.py`：planner 输入 = context 块 + 本次 prompt；executor 的 `Overall task:` 前前置 context 块；critic 输入不变；history 为空且无继承时逐字节等同现状（回归测试断言）
- [x] 6.4 fake-model 端到端测试：带 history（含 failed 轮）+ 继承产物的 run，断言 planner/executor 实际收到的消息包含历史轮次、失败标记与文件节选、预算截断与入选顺序生效；resume 场景断言 context 块来自 checkpoint
- [x] 6.5 worker 可观测性：context 块字节数 / 注入历史轮数 / 节选文件数的结构化日志字段；`worker_summary_events_total` 计数（AGENTS.md §7：新增外部调用与状态产出至少同步一个 metric/log）

## 7. 文档与归档准备

- [x] 7.1 `docs/ARCHITECTURE.md`：§5.3 execute 契约增加 `history` 字段（含 `status`、边界与"重发复用原 payload"规则要点）、§5.2 事件 kind 增加 `summary`；§4.2 数据模型的 `task_versions` DDL 补 `summary` 列
- [x] 7.2 全量回归：`api` 测试套件、`worker` 测试套件通过；确认新旧组件混布兼容性论证（design D6）与实现一致
