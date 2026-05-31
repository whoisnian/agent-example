# api

后端 API 代码根目录（Golang + Gin + pgx + sqlc + amqp091）。

- **技术栈**：Go 1.26+ / Gin / pgx v5 + pgxpool / sqlc / golang-migrate / amqp091-go / slog / OpenTelemetry (v1.43+) / prometheus/client_golang
- **职责**：任务 CRUD、状态机推进、版本管理、互斥校验、成本查询聚合、Outbox 投递、Realtime Gateway 与 Worker 编排
- **目录规划**：见 [`../docs/ARCHITECTURE.md §3.2`](../docs/ARCHITECTURE.md)

> MVP 骨架由 OpenSpec 变更 `init-api-scaffold` 引入；任务域 / 成本域 schema 由 `add-task-domain-schema` 引入。当前提供"能启动 / 能连 PG / 能连 RabbitMQ / 能优雅关停 / Outbox Relayer 可投递 / 业务表 schema 就绪"的最小可用骨架；尚未交付任何业务路由。

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
| `DEFAULT_LANE` | `default` | 创建/迭代任务时，请求未提供 `lane` 字段时回退使用的 lane（写入 `outbox.topic = execute.<task_type>.<lane>`） |
| `DEFAULT_TASK_DEADLINE` | `60m` | execute payload 中 `deadline_ts` 与 `now()` 的偏移量 |
| `DEV_TENANT_ID` / `DEV_USER_ID` | 占位 UUID | 鉴权中间件接入前，task 表 `tenant_id` / `user_id` 的兜底写入值；接入 JWT 后由 middleware 注入并废弃 |
| `EVENT_CONSUMER_PREFETCH` | 16 | 事件消费者（`q.task.events`）的 AMQP prefetch（QoS）；多副本竞争同一队列，无需选主 |

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

- **任务级互斥由 DB 唯一部分索引 `one_active_version_per_task` 兜底**（迁移 `0002_init_task_domain`）。索引建在 `task_versions(task_id) WHERE is_active` 上，`is_active` 是 `STORED` 生成列。任何"创建活跃版本"的操作（create / iterate / rollback-branch）应用层再做一次显式检查只为更友好的 409，DB 索引才是真相之源。
- 所有跨服务边界的事件走 **Outbox 模式**：DB 事务写业务表 + outbox，由 Relayer 异步发布到 RabbitMQ；Relayer 通过 `pg_try_advisory_lock` 做单活跃选主。
- 任何状态翻转通过 Domain Service 的状态机方法完成，禁止裸 SQL UPDATE。
- 错误返回结构：`{code, message, data, trace_id}`，互斥冲突使用 `409 active_version_exists`（业务码由具体业务提案补充）。
- `infrastructure/persistence/sqlc/` 之外的业务层禁止直接使用 `pgx` / `database/sql`；唯一例外是 Outbox Relayer（架构 design D2）。

## 业务表 schema

`add-task-domain-schema` 引入了以下表（参见 [`docs/ARCHITECTURE.md §4`](../docs/ARCHITECTURE.md)）：

| 域 | 表 | 备注 |
|---|---|---|
| 任务域（迁移 0002） | `tasks` / `task_versions` / `task_runs` / `task_events` / `task_checkpoints` / `artifacts` | `task_versions.is_active` 是 generated stored column；建在其上的 partial unique 索引就是任务级互斥的来源 |
| 成本域（迁移 0003） | `pricing` / `cost_events` / `task_costs` | 历史价格行不可改（约定 + code review）；`task_costs` 由 Cost Service 独占 UPSERT |

sqlc 已基于 `queries/*.sql` 生成 CREATE + READ 路径的类型化代码至 `internal/infrastructure/persistence/sqlc/`。状态机 UPDATE 类查询等到引入状态机的提案再加。

## 任务写入端点（`task-write-api`）

`add-task-create-api` 落地了首批写端点：

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/api/v1/tasks` | 新建任务 + v1；同一事务里插入 `tasks` / `task_versions` / `task_runs` / `outbox`，由 Relayer 异步投递 `execute.<task_type>.<lane>` |
| `POST` | `/api/v1/tasks/{task_id}/iterate` | 基于 `base_version_id`（缺省取 `tasks.current_version`）派生新版本；活跃中返回 `409 active_version_exists`，DB 唯一索引 `one_active_version_per_task` 是真相之源 |

请求/响应契约见 [`openspec/specs/task-write-api/spec.md`](../openspec/specs/task-write-api/spec.md)。鉴权中间件目前是 stub，`tenant_id` / `user_id` 取自 `DEV_TENANT_ID` / `DEV_USER_ID`，正式 JWT 接入后由 middleware 注入。

## 任务读取端点（`task-read-api`）

`add-task-read-api` 落地了 owner-scoped 的只读端点（无事务、无 Outbox、不调用 LLM）。所有响应走统一 `{code, message, data, trace_id}` 信封；未拥有的资源一律返回 `404`（绝不 `403`，不泄露存在性）。

| 方法 | 路径 | 查询参数 | 说明 |
|---|---|---|---|
| `GET` | `/api/v1/tasks` | `page`（默认 1，<1 截到 1）、`page_size`（默认 20，截到 [1,100]）、`status`（六个任务态之一，非法→400） | 分页列表，每行含成本摘要；`data = {items, page, page_size, total}` |
| `GET` | `/api/v1/tasks/{task_id}` | — | 任务详情 + 当前版本摘要 + 成本摘要；`data = {task, current_version, cost}` |
| `GET` | `/api/v1/tasks/{task_id}/versions` | — | 扁平版本树（按 `version_no` 升序，节点带 `parent_id`），每节点含成本；`data = {items}` |
| `GET` | `/api/v1/versions/{version_id}` | — | 版本详情 + runs（按 `attempt_no` 升序）+ 成本；`data = {version, runs, cost}` |
| `GET` | `/api/v1/versions/{version_id}/events` | `after_id`（默认 0，全局 `task_events.id` 游标，非 `seq`）、`limit`（默认 200，截到 [1,1000]） | WS 断线补齐；`data = {items, next_after_id}` |

要点：

- **成本摘要**内嵌于列表 / 详情 / 版本节点，来自 `task_costs` 表。`amount_usd` 是保留 8 位小数的**十进制字符串**（如 `"0.62000000"`），避免 `float64` 丢精度；Cost Service 尚未实现时恒为 `"0.00000000"`，读取永不因成本缺失而失败。成本明细端点（`/cost`）属于 `add-task-cost-api`。
- **事件游标**用全局 `task_events.id`；每个事件同时暴露 `id` 与 `seq` 供实时客户端对齐（详见提案 design D7 / Open Question #3）。
- 同写端点，`tenant_id` / `user_id` 取自 `DEV_TENANT_ID` / `DEV_USER_ID`。

请求/响应契约见 [`openspec/specs/task-read-api/spec.md`](../openspec/specs/task-read-api/spec.md)。

## 事件消费 / 状态同步（`task-event-ingest`）

`add-event-ingest-status-sync` 落地了 API 侧的事件消费者：订阅 `q.task.events`（绑定 `event.#`），消费 Worker 上报的 `event.<task_type>.<kind>` 流，在**单事务**内幂等写入 `task_events`（`ON CONFLICT (run_id, seq) DO NOTHING`）并驱动状态机。这让"提交→执行→看到结果"闭环真正跑通——在此之前读端点恒返回 `pending` + 空事件流。

要点：

- **写入边界（关键）**：消费者是 `task_versions.status` + `tasks.status` 的**唯一权威写者**（偿还 `add-task-create-api` Open Question #4 的 `tasks.status` writer 债务）。`task_runs.status` 仍归 **Worker 所有**（架构 §6.1：Worker claim run 时写 `running`、结束时写终态），消费者从不触碰它。
- **事件 → 状态映射**：`kind=status` 按 `payload.status` 驱动版本状态；`kind=error` 一律视为 `failed`（Worker 错误路径只发 `error`、不发尾随的 `status:failed`）；`plan` / `step` / 未识别状态仅落库不翻转。版本与任务状态域不同（版本允许 `queued`/`cancelling`，任务不允许），故 `queued`→任务 `pending`、`cancelling`→跳过任务更新。
- **终态守卫 + 真实翻转守卫**：状态翻转走 SQL 层 CAS（`WHERE status NOT IN (终态) AND status IS DISTINCT FROM $新值`）。前者保证终态版本永不被乱序/迟到事件改写，后者保证重投的同态事件影响 0 行，使 `event_status_transitions_total` 计数准确。
- **任务态仅由当前版本驱动**：`UPDATE tasks ... WHERE current_version = $version`，超期版本的事件不会改写已迭代到新版本的任务态。
- **投递语义**：解码失败 / 缺字段 → `nack(requeue=false)` 进 DLQ；可重试 DB 错误（连接 `08*` / 资源 `53*` / `40001` / `40P01` / deadline）→ `nack(requeue=true)`；其余（含约束违反 `23xxx`）默认 → DLQ，杜绝毒消息无限重投。
- **无选主**：`q.task.events` 是工作队列，RabbitMQ 在多副本间负载均衡；消费者幂等，故无需 Outbox Relayer 那样的 advisory-lock 选主。

请求/响应契约见 [`openspec/specs/task-event-ingest/spec.md`](../openspec/changes/add-event-ingest-status-sync/specs/task-event-ingest/spec.md)。

## 成本结算（`task-cost-ingest`）

`add-cost-service` 落地了第二个消费者，订阅 `q.cost.events`（绑定 `cost.#`），按 `pricing` 表结算 Worker 上报的 `cost.<kind>` 事件并 UPSERT `task_costs`——读端点的 `amount_usd` 从此不再恒为 `"0.00000000"`。

要点：

- **唯一写者**：消费者是 `task_costs` 的唯一权威写者（spec `task-cost-data-model` §"Task Costs Aggregation Table"），同时是 `cost_events` 的唯一写者。Worker 只发事件，不直接持久化。
- **幂等边界**：`(run_id, kind, seq)` 三列唯一（迁移 `0004_cost_events_kind_unique` 把原 `(run_id, seq)` 重做为三列，以匹配 Worker 的"per-run-per-kind"`seq` 命名空间）。重投递走 `ON CONFLICT DO NOTHING RETURNING`，零行返回即跳过聚合 UPSERT，无双计数。
- **结算公式**（spec §"Cost Event Settlement Math"）：
  - `llm`：`(input_tokens/1000)×per_1k_input_tokens + (output_tokens/1000)×per_1k_output_tokens + (cached_tokens/1000)×per_1k_cached_tokens`
  - `tool`：`(calls ?? 1)×per_call + (duration_ms/1000)×per_second`
  - `compute`：`(duration_ms/1000)×per_second`
  - 全部用 `*big.Rat` 精确有理数运算，绑回 `NUMERIC(18,8)`。
- **缺价非致命**：找不到 `pricing` 行时 `amount_usd=0`、`pricing_id=NULL`，事件仍落库、token 列仍 UPSERT，便于将来回填；同时 `cost_pricing_missing_total{kind,resource}` +1。启动期会输出 `cost_pricing_coverage` INFO 日志，列出当前生效的 `(kind, resource_name)`，是抗 `resource_name` 拼写错误的运维护栏。建议告警：`increase(cost_pricing_missing_total[10m]) > 5`。
- **`task_id` 不可变**：消费者结算前用 `task_versions` 反查 `version_id` 的所属 `task_id`，不匹配视为永久错误进 DLQ；`task_costs` UPSERT 的 `DO UPDATE SET` 也不包含 `task_id`。
- **聚合列映射**：`compute_seconds = floor(duration_ms/1000)`（亚秒事件贡献 0，`amount_usd` 仍精确）；`wall_time_ms` 仅 llm/tool 累加；`tool_calls` 仅 tool 累加，与是否匹配到 `per_call` 价无关。
- **投递语义**：解码失败 / 未知 `kind` / `task_id` 不匹配 → DLQ；可重试 DB 错误（与 event-ingest 共享 `isRetryable`）→ 重排；其余永久错误 → DLQ。
- **指标**：`cost_events_consumed_total{kind}` / `cost_events_settled_total{kind,result}` / `cost_pricing_missing_total{kind,resource}` / `cost_amount_settled_usd_total` / `cost_event_settle_duration_seconds` / `cost_consumer_connected`（与 `event_consumer_connected` 独立）。
- **配置**：`COST_INGEST_PREFETCH`（默认 64）。

请求/响应契约见 [`openspec/specs/task-cost-ingest/spec.md`](../openspec/changes/add-cost-service/specs/task-cost-ingest/spec.md)。

## 集成测试

`make test-integration` 用 testcontainers 启 PostgreSQL 18.4，跑 schema 结构断言、迁移 up→down→up 圈、互斥并发回归、`(run_id, seq)` 幂等性、pricing 不变量等。需要本机 Docker。CI 仅在 `main` 分支推送时触发 `integration-tests` job，PR 默认 lane 不跑（也不按时间调度执行）。

## 目录结构

```
api/
├── cmd/api/main.go              进程入口 + 生命周期编排
├── internal/
│   ├── interfaces/http/         HTTP 层（server、middleware、health、envelope、errors、recovery）
│   ├── application/             用例编排（`task/` 由 add-task-create-api 引入）
│   ├── domain/                  领域模型（`task/` 由 add-task-create-api 引入）
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
