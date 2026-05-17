# AGENTS.md

给参与本仓库工作的 AI 编码代理（Claude Code / Copilot Agent / 其它）的协作指南。

> 人类协作者也可阅读本文，但本文档侧重为"代理"提供：项目语境、边界约束、工作流、注意事项。

---

## 1. 项目语境（Context）

- **目标**：构建一个 LLM Agent 任务平台 MVP：用户提交任务 → 后端编排 → Worker（deepagents）执行 → 迭代/回滚 + 成本统计。
- **当前阶段**：**MVP 设计完成，代码尚未实现**。仓库中只有架构文档与 OpenSpec 框架；任何模块的首次实现都应先通过 OpenSpec 提案。
- **唯一架构事实来源**：[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)。任何与该文档冲突的实现必须先更新文档或在 OpenSpec 提案中显式声明偏离原因。
- **历史背景**：[`docs/HISTORY.md`](docs/HISTORY.md) 记录每轮迭代的用户原始诉求，可用于回溯"为什么做成这样"。

---

## 2. 仓库地图

```
docs/         架构、历史、ADR 等纯文档
web/          前端：React + TS + Vite
api/          后端 API：Golang
worker/       Worker：Python + deepagents
openspec/     变更与规格管理
.claude/      Claude Code 配置（commands / skills）
.github/      GitHub 集成（含 Copilot prompts / skills）
```

各模块的内部布局见 [`docs/ARCHITECTURE.md §3`](docs/ARCHITECTURE.md)。

### 边界约定
- `api/` 不直接调用 LLM；调用 LLM 是 Worker 的职责。
- `worker/` 不直接对外暴露 HTTP；只通过 RabbitMQ 与 API 交互，必要时通过 OSS 与 DB 共享状态。
- `web/` 不持有业务逻辑；服务端状态走 React Query，本地 UI 状态走 Zustand。
- `docs/` 只放纯文档；不放代码。所有可执行内容（示例脚本、CI 工具）放在对应模块或独立 `scripts/`。

---

## 3. OpenSpec 工作流（强制）

本仓库使用 OpenSpec 管理"非 trivial 变更"。**新增模块、修改公共接口、调整数据模型，必须先提案再实现。**

### 何时必须走 OpenSpec
- 新增/删除/重命名公共 API 路由、MQ 主题、DB 表字段
- 新增插件类型、修改 Worker 核心循环
- 新增前端页面或全局状态
- 修改成本结算口径、互斥规则等会影响业务语义的逻辑

### 何时可以跳过
- 类型注解、内部重命名、纯文案修复
- 测试新增或拆分（不改公共契约时）
- 文档勘误、依赖小版本升级

### 命令
- `/opsx:explore` — 调研，仅产出 exploration 笔记
- `/opsx:propose <change-name>` — 一次性产出 proposal + design + tasks
- `/opsx:apply <change-name>` — 按 tasks 落地代码
- `/opsx:archive <change-name>` — 完成后沉淀到 `openspec/specs/`

### 命名约定
- 变更名 kebab-case，动词开头：`add-task-cost-api`、`refactor-worker-plugin-loader`
- 一个变更聚焦一件事，避免"大杂烩"提案

---

## 4. 模块协作约定

### 4.1 后端 API（`api/`，Golang）
- 框架：Gin 或 Echo（提案时确定，之后保持一致）
- 分层：`interfaces/` ↔ `application/` ↔ `domain/` ↔ `infrastructure/`
- SQL 通过 sqlc 生成；禁止在 handler 里直接拼 SQL
- 所有跨服务边界的操作走 **Outbox**：DB 事务内写业务表 + outbox，由 Relayer 投递到 MQ
- 错误：使用 `pkg/errors` 包装，对外用统一 `{code, message, trace_id}` 结构
- 日志：`slog` 结构化，必须带 `trace_id` / `task_id`
- 任何状态翻转走 Domain Service 的状态机方法，禁止裸 UPDATE

### 4.2 Worker（`worker/`，Python）
- 包管理：`uv`（首选）或 `poetry`
- Agent 框架：LangChain `deepagents`
- MQ 客户端：`aio-pika`（异步）
- 所有 LLM/tool 调用必须经过 `core/cost_meter.py` 包装，保证成本事件发出
- 任何 step 结束必须写 `task_checkpoints`，保证可恢复
- 插件目录 `worker/plugins/` 下；新增插件以 `plugin.yaml` 声明，启动期自动注册
- 禁止在 Worker 中直接持久化业务状态到 DB —— 只能写：cost_events / task_runs heartbeat / task_checkpoints / artifacts。其它状态翻转必须通过事件让 API/Cost Service 处理

### 4.3 前端（`web/`，React）
- 包管理：`pnpm`
- 框架：Vite + React 18 + TypeScript 严格模式
- 服务端状态：React Query；本地 UI 状态：Zustand。**不要混用**
- 实时：`features/realtime/` 下的 WS 客户端；不要在组件里直连 WS
- 任务级互斥反映在 UI：`task.status` 活跃时禁用迭代/回滚-branch 按钮，并显示原因
- 成本展示：TaskDetail、VersionTree、CostDashboard 三处复用 `features/costs/` 提供的 hooks

---

## 5. 通用代码与提交约定

- 语言：代码注释默认中文 OK，但**公共 API、错误码、日志字段名必须英文**
- 提交信息：`<scope>: <verb> <subject>`，scope 用 `api` / `worker` / `web` / `docs` / `openspec` 等
  - 例：`api: add /tasks/{id}/cost endpoint`
  - 例：`worker: wrap LLM calls in cost_meter`
- 一个 commit 一个逻辑变更；避免 "wip" / "fix various" 这种混合提交
- 不要在代码中留 TODO 而不落 Issue / OpenSpec change

---

## 6. 安全与红线

- **不要**把 LLM API Key、数据库口令、OSS Access Key 写入仓库或日志
- **不要**让 Worker 沙箱以 root 跑代码生成任务
- **不要**绕过任务级互斥唯一索引（任何"修复"互斥问题的方案要先讨论）
- **不要**修改 `pricing` 表已生效记录的单价；变更价格通过新增带 `effective_at` 的行
- **不要**在 PR 中夹带"顺手优化的无关重构"
- **不要**让 Worker 直接写 `tasks` / `task_versions` 主表（见 §4.2）

---

## 7. 给代理的具体行为指引

- **先读后写**：实现任何模块前先读 `docs/ARCHITECTURE.md` 对应小节 + `openspec/changes/<change>/design.md`
- **保持小步**：单次 PR 控制在 500 行以内（生成代码 / 测试除外）；超出请拆
- **测试优先**：API/Worker 的领域逻辑必须有单测；HTTP 接口必须有契约测试
- **可观测性**：每新增一条状态翻转或外部调用，至少同步加一个 metric/log 字段
- **不要假设**：遇到 ARCHITECTURE.md 未覆盖的细节，优先在 OpenSpec design.md 中先决策，再编码
- **保护 MVP 边界**：不主动实现标注 `[Post-MVP]` 的内容，即使技术上很容易顺手做

---

## 8. 常用入口

- 架构总览：[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- 演进历史：[`docs/HISTORY.md`](docs/HISTORY.md)
- 变更提案模板：`/opsx:propose`
- 已归档规格：`openspec/specs/`

如本文档与 `docs/ARCHITECTURE.md` 冲突，以 ARCHITECTURE 为准；如发现冲突，请在下一个 OpenSpec change 中同步修正。
