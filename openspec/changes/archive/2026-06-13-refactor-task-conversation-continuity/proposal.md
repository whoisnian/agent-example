# Refactor Task Conversation Continuity

## Why

Iterate 在产品语义上是"继续对话"，但当前实现把每个 version 当作孤立任务执行：agent loop 只把本次 iterate 的 prompt 喂给模型（`worker/agents/loop.py`），父版本的 prompt 链、执行结论、已生成产物对 LLM 完全不可见；`inherit_parent_artifacts` 只做 OSS 物理复制，不进入模型输入。结果是 Iterate 的输出与前置内容无关，用户感知为"对话断裂"。本变更让同一 task 维持一份对话历史与上下文，使 Iterate / rollback-branch 真正在前置内容基础上继续。

## What Changes

- **版本结果摘要回写**：Worker 在 run 成功结束时发出 `kind=summary` 的 `task.events` 事件（复用 `gen_title`→`kind=title` 的回写模式）；API 事件消费端把摘要落到新增的 `task_versions.summary` 列。该摘要是后续轮次对话历史中"assistant 说了什么"的素材。
- **对话历史随 execute 消息下发**：API 在 iterate / rollback-branch 构建 execute payload 时，沿 base 版本的 `parent_id` 链（天然分支感知，与 rollback 语义一致）组装有界的 `history: [{version_no, prompt, summary, status}]`（旧→新），写入 outbox payload。create 路径不带 history（首轮无历史）；同 version 的任何重发复用原 payload 中的 history。
- **Worker 上下文注入**：`TaskExecuteMessage` 增加可选 `history` 字段（缺省空；结构非法时降级为空 + 指标，不毒化消息）。agent loop 把"对话历史 + 继承产物清单（路径/大小）+ 小型文本产物内容节选（有预算上限）"组装成 conversation-context 块，注入 planner 与 executor 的输入；context 块随 plan checkpoint 持久化，resume 时恢复而非重组。
- **resume 事件序列修复（前置依赖）**：checkpoint state 记录事件 seq 高水位与各步摘要，resume 时恢复——修复"新 attempt 事件 seq 从头计、被 `(run_id, seq)` 幂等静默丢弃"的存量缺陷，使 summary 事件（及 step/artifact 事件）在 resume 后仍可靠。
- **产物目录语义澄清（非破坏）**：保留按版本前缀的快照式产物目录与物理继承（rollback 依赖快照）；"同一 task 一份产物目录"以版本链的逻辑目录呈现——任一版本前缀即该时点的目录状态。不引入可变的 task 级共享目录（理由见 design.md）。
- **文档同步**：`docs/ARCHITECTURE.md` §5.2 事件 kind 增加 `summary`，§5.3 execute 消息契约增加 `history` 字段。

无 **BREAKING** 变更：`history` 与 `summary` 均为新增可选字段/列，旧消息与旧行为均兼容。

## Capabilities

### New Capabilities

- `task-conversation-history`：同一 task 的对话历史契约——历史的来源（版本父链）、组装规则（顺序、深度与字节上限、缺摘要时的降级）、execute payload 中的 `history` 字段形态，以及 Worker 侧注入模型输入的最低要求。

### Modified Capabilities

- `task-write-api`：iterate 的 execute outbox payload 必须携带按父链组装的 `history`。
- `task-rollback-api`：rollback-branch 的 execute payload 同样携带 `history`（沿所选 base 版本的父链，分支语义自动正确）。
- `task-event-ingest`：新增 `kind=summary` 事件处理——同事务幂等落 `task_versions.summary`，规则对齐 `kind=title`。
- `task-data-model`：`task_versions` 新增可空 `summary` 列（迁移）。
- `worker-messaging`：`TaskExecuteMessage` 解析新增可选 `history` 字段，缺省空列表；结构非法的 history 降级为空（不毒化）。
- `worker-agent-orchestration`：loop 必须把 history 与继承产物清单注入 planner/executor 输入（context 块随 checkpoint 持久化）；run 成功结束时发出 `kind=summary` 事件；checkpoint 记录事件 seq 高水位与步骤摘要，resume 时恢复。
- `realtime-gateway`：frame 契约的事件 kind 枚举加入 `summary`（透传行为不变，仅枚举保鲜）。

## Impact

- **api/**：`domain/task/payload.go`（payload 增加 history）、`service.go` / `rollback_service.go`（父链查询）、`event_sync.go`（summary 应用）、sqlc 查询与迁移（`task_versions.summary`、按链取历史）。`GET /versions/{id}` 的 "full version row" 自然带出新列，读 API 无契约改动。
- **worker/**：`core/messages.py`（payload 字段）、`core/run_context.py`（seq 高水位恢复）、`agents/loop.py`（上下文组装与注入、checkpoint state 扩展、summary 生成与事件发出）、`agents/prompts/`（角色指令适配）。
- **web/**：无契约变更；TaskDetail 可顺带展示 version summary（可选，不在本变更强制范围）。
- **docs/**：`ARCHITECTURE.md` §5.2 / §5.3。
- **依赖/系统**：无新增外部依赖；payload 体积受历史上限约束（见 design.md）。
