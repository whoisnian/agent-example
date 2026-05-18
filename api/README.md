# api

后端 API 代码根目录（Golang + Gin + pgx + sqlc + amqp091）。

- **技术栈**：Go 1.26+ / Gin / pgx v5 + pgxpool / sqlc / golang-migrate / amqp091-go / slog / OpenTelemetry (v1.43+) / prometheus/client_golang
- **职责**：任务 CRUD、状态机推进、版本管理、互斥校验、成本查询聚合、Outbox 投递、Realtime Gateway 与 Worker 编排
- **目录规划**：见 [`../docs/ARCHITECTURE.md §3.2`](../docs/ARCHITECTURE.md)

> MVP 骨架由 OpenSpec 变更 `init-api-scaffold` 引入。当前提供"能启动 / 能连 PG / 能连 RabbitMQ / 能优雅关停 / Outbox Relayer 可投递"的最小可用骨架；尚未交付任何业务路由。

## 本地启动

依赖：Go 1.26+、Docker / docker-compose、`golangci-lint` v2+（可选）、`sqlc`（可选，仅在改 `queries/*.sql` 时需要）。

```sh
# 1. 在仓库根目录启动依赖（Postgres + RabbitMQ）
docker compose -f docker-compose.dev.yml up -d postgres rabbitmq

# 2. 安装依赖
cd api && go mod download

# 3. 应用迁移
make migrate-up

# 4. 启动服务
make run

# 健康检查
curl localhost:8080/healthz   # 200 {"status":"ok"}
curl localhost:8080/readyz    # 200 当 PG / RMQ 都通；503 + 失败依赖列表否则
curl localhost:8080/metrics   # Prometheus 文本格式
```

## 配置

通过环境变量配置（12-factor 风格），可选 `--config <path>` YAML 覆盖。环境变量优先级高于 YAML。完整列表见 `internal/infrastructure/config/config.go`，关键项：

| 环境变量 | 默认值 | 含义 |
|---|---|---|
| `HTTP_ADDR` | `:8080` | HTTP 监听地址 |
| `DATABASE_URL` | — | PG DSN，**必填** |
| `DB_MAX_CONNS` | 20 | pgxpool MaxConns |
| `DB_MIGRATE_ON_BOOT` | `false` | 启动时自动跑 up 迁移（dev 设 `true` 方便） |
| `RABBITMQ_URL` | — | AMQP URL，**必填** |
| `LOG_LEVEL` | `info` | slog 级别 |
| `OTLP_ENDPOINT` | 空（noop） | OTLP HTTP 导出地址，例如 `http://localhost:4318` |
| `SHUTDOWN_DRAIN_TIMEOUT` | `30s` | 优雅关停 in-flight 请求等待时长 |

## 常用命令

| 命令 | 作用 |
|---|---|
| `make run` | 本地启动服务 |
| `make build` | 构建二进制到 `bin/api` |
| `make test` | 单元测试（不含 `//go:build integration`） |
| `make test-integration` | 集成测试（需要 Docker / testcontainers） |
| `make lint` | `golangci-lint run`（配置：`.golangci.yml`） |
| `make vet` | `go vet ./...` |
| `make sqlc` | 重新生成 sqlc 代码（修改 `queries/*.sql` 后） |
| `make migrate-up` / `migrate-down` | 应用 / 回滚迁移 |
| `make migrate-force VERSION=N` | 从 dirty 状态恢复，强制设置版本号 |
| `make tidy` | `go mod tidy` |

## 迁移 dirty 状态恢复

`golang-migrate` 在迁移半途失败会把 `schema_migrations.dirty` 设为 `true`，后续 `migrate up` 拒绝继续。恢复流程：

```sh
# 1. 查看当前版本
go run ./cmd/api migrate version

# 2. 检查 DB 实际 schema，确认应该停在哪个版本（例如 1）
# 3. 修复任何残留 schema 错误（手动 psql）

# 4. 强制设置版本（清 dirty 标记）
make migrate-force VERSION=1

# 5. 继续
make migrate-up
```

## 关键不变量（实现层面）

- 任何"创建活跃版本"的操作（create / iterate / rollback-branch）必须在事务内做互斥检查，并依赖 DB 唯一索引 `one_active_version_per_task` 兜底（**业务表由后续提案引入**；本骨架仅含 `outbox`）。
- 所有跨服务边界的事件走 **Outbox 模式**：DB 事务写业务表 + outbox，由 Relayer 异步发布到 RabbitMQ；Relayer 通过 `pg_try_advisory_lock` 做单活跃选主。
- 任何状态翻转通过 Domain Service 的状态机方法完成，禁止裸 SQL UPDATE。
- 错误返回结构：`{code, message, data, trace_id}`，互斥冲突使用 `409 active_version_exists`（业务码由具体业务提案补充）。
- `infrastructure/persistence/sqlc/` 之外的业务层禁止直接使用 `pgx` / `database/sql`；唯一例外是 Outbox Relayer（架构 design D2）。

## 目录结构

```
api/
├── cmd/api/main.go              进程入口 + 生命周期编排
├── internal/
│   ├── interfaces/http/         HTTP 层（server、middleware、health、envelope、errors、recovery）
│   ├── application/             用例编排（脚手架阶段为空）
│   ├── domain/                  领域模型（脚手架阶段为空）
│   ├── infrastructure/
│   │   ├── config/              配置加载（env + 可选 yaml）
│   │   ├── observability/       logger / tracing / metrics
│   │   ├── persistence/         pgxpool / migrate / outbox 直 pgx 访问 / sqlc 生成目录
│   │   └── messaging/           connection / topology / publisher / outbox_relayer
│   └── pkg/                     共享小工具
├── migrations/                  golang-migrate SQL 文件
├── queries/                     sqlc 输入
├── sqlc.yaml
└── Makefile
```
