## Why

仓库目前只有架构文档（`docs/ARCHITECTURE.md`），`api/` 是空目录。后续每一条业务能力（创建任务、互斥校验、成本查询等）都依赖统一的服务骨架：配置加载、日志/追踪/指标、PostgreSQL 连接与迁移、RabbitMQ 拓扑与 Outbox 投递。本提案一次性建立这套地基，避免后续每个特性提案重复引入基础设施。

本提案 **不交付任何业务端点**，只交付一个能够 `go run` 起来、健康检查能过、能连上本地 PG 与 RabbitMQ 的最小骨架。

## What Changes

- 新建 `api/` 目录及 DDD 分层骨架（`cmd/api`、`internal/{interfaces,application,domain,infrastructure}`、`pkg/`），见 ARCHITECTURE §3.2
- 选定 HTTP 框架：**Gin**（足够轻、生态成熟、与 MVP 复杂度匹配）
- 选定 SQL 工具链：**sqlc**（类型安全、避免运行时反射），迁移工具：**golang-migrate**
- 选定 RMQ 客户端：`github.com/rabbitmq/amqp091-go`
- 引入：`slog`（标准库结构化日志）、`go.opentelemetry.io/otel`（trace + metric）、`prometheus/client_golang`（/metrics endpoint）
- 实现服务级骨架能力：
  - 配置加载（环境变量 + 可选 yaml；按 12-factor 风格）
  - 进程生命周期：启动、健康检查 `/healthz`、就绪检查 `/readyz`、优雅关停（含 in-flight 请求 drain）
  - 统一响应封装 `{code, message, data, trace_id}` 与错误映射（HTTP status ↔ 业务 code）
  - 中间件：request id、tracing、metrics、panic recovery、access log
- PostgreSQL 持久化基础：连接池（`pgxpool`）、迁移加载、`outbox` 表 schema、sqlc 代码生成接入
- RabbitMQ 消息基础：声明 ARCHITECTURE §3.6 中规划的 exchange / queue / DLX 拓扑（幂等声明）、publisher 抽象、Outbox Relayer 主循环（扫描 → 发布 → 标记）
- 引入 `Makefile`（或 `Taskfile`）封装 `make run / migrate / sqlc / lint / test`
- 引入 `docker-compose.dev.yml` 起 PG 14+ 与 RabbitMQ 3.12+（management plugin）用于本地开发
- 引入基础 CI：`go vet`、`golangci-lint`、`go test`

非目标（显式不做，由后续提案承担）：
- 任何 `/tasks`、`/versions`、`/cost` 等业务路由
- 任务级互斥逻辑（属于 `add-task-create-api` 提案）
- 鉴权（JWT 校验放到 `add-api-auth` 提案；本提案仅在中间件链中预留位置）
- 真实的 Realtime Gateway / WebSocket Hub（独立提案）

## Capabilities

### New Capabilities

- `api-bootstrap`: API 服务的进程级骨架 —— 配置加载、结构化日志、OTel tracing、Prometheus 指标、统一错误/响应封装、健康与就绪检查、优雅关停
- `api-persistence`: PostgreSQL 数据访问基础 —— 连接池、迁移管理、sqlc 代码生成约束、`outbox` 表结构
- `api-messaging`: RabbitMQ 通讯基础 —— exchange/queue/DLX 幂等声明、publisher 抽象、Outbox Relayer 投递循环

### Modified Capabilities

（无 —— 这是首个 API 提案，`openspec/specs/` 为空）

## Impact

- **代码**：`api/` 从空目录变为可运行骨架；新增约 1.5k–2k 行 Go 代码（含生成代码）
- **依赖**：引入 Gin、pgx、sqlc-generated、amqp091-go、slog、OTel、prom-client、golang-migrate
- **本地开发**：新增 `docker-compose.dev.yml`；需要本地 Docker / Podman
- **CI**：新增 GitHub Actions workflow（lint + test + build）；预计 < 2 min
- **文档**：`api/README.md` 升级为含本地启动步骤；`docs/ARCHITECTURE.md` 无需改动（实现对齐既有设计）
- **下游解锁**：后续 `add-task-create-api`、`add-task-cost-api`、`add-task-control-api` 等业务提案均依赖本骨架；`init-worker-scaffold`、`init-web-scaffold` 可与本提案并行推进
