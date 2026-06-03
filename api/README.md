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
| `DEV_TENANT_ID` / `DEV_USER_ID` | 占位 UUID | `/auth/login` 成功后签发 token 所用的 principal（真实用户表落地前的单一身份，见 `add-api-user-store`）；运行期身份由 JWT claims 经中间件注入 context |
| `AUTH_JWT_SECRET` | — | HS256 签名/校验密钥，**必填**（fail-fast，与 `OSS_*` 同路径）；**绝不写入日志/响应** |
| `AUTH_JWT_TTL` | `24h` | 签发 token 的有效期；`exp` 与 `expires_at` = 签发时刻 + TTL |
| `AUTH_DEV_EMAIL` | `dev@example.com` | MVP 登录凭证邮箱 |
| `AUTH_DEV_PASSWORD` | — | MVP 登录凭证口令，**必填**（无默认，避免出厂弱口令后门）；**绝不写入日志/响应** |
| `EVENT_CONSUMER_PREFETCH` | 16 | 事件消费者（`q.task.events`）的 AMQP prefetch（QoS）；多副本竞争同一队列，无需选主 |
| `OSS_ENDPOINT` | — | S3 兼容对象存储端点，**必填**；与 worker 共用同名变量（SeaweedFS/MinIO 均走 path-style） |
| `OSS_BUCKET` | — | 产物 bucket 名，**必填** |
| `OSS_ACCESS_KEY_ID` | — | OSS access key id，**必填** |
| `OSS_ACCESS_KEY_SECRET` | — | OSS access key secret，**必填**（注意是 `..._KEY_SECRET`，与 worker 一致，非 AWS 习惯的 `..._SECRET_ACCESS_KEY`） |
| `OSS_REGION` | `us-east-1` | 签名用 region |
| `OSS_USE_PATH_STYLE` | `true` | path-style 寻址（SeaweedFS/MinIO 必须为 true） |
| `OSS_PRESIGN_TTL` | `5m` | 产物下载预签名 URL 有效期；泄露的链接在此之后过期 |
| `WS_ALLOWED_ORIGINS` | 空（同源） | 实时网关握手的 `Origin` 允许列表（逗号分隔），关闭 CSWSH；空表示仅允许同源。dev SPA 形如 `http://localhost:5173` |
| `WS_SEND_BUFFER` | 128 | 每连接出站缓冲帧数；满则驱逐慢客户端（不阻塞 fan-out），客户端重连并按 `seq` 回补 |
| `WS_READ_DEADLINE` | `60s` | 服务端读超时，**必须 > 客户端 25s 心跳间隔**；超时未收到任何帧的半开连接被回收 |
| `WS_READ_LIMIT` | 32768 | 单条入站消息字节上限 |
| `WS_MAX_TOPICS_PER_CONN` | 64 | 单连接可订阅 topic 上限 |
| `WS_MAX_SUBSCRIBE_TOPICS` | 32 | 单个 `subscribe` 帧 `topics` 数组上限（每个 topic = 一次归属探测，防止放大 DB 查询） |
| `WS_FANOUT_PREFETCH` | 32 | 每实例 fan-out 消费者的 AMQP prefetch |

> 上述四个 `OSS_*` 必填项与 worker 共用同一套配置（见 `worker/worker/core/config.py`）；API 仅用它们签发产物下载的预签名 URL（不经 API 传输字节）。缺失任一项会在启动时 fail-fast（与 `DATABASE_URL` 同路径），`api migrate` 子命令例外（只需 `DATABASE_URL`）。凭据不会写入日志或响应。
>
> 本地 `make run` 对接 `docker-compose.dev.yml` 的 seaweedfs：`OSS_ENDPOINT=http://localhost:9000 OSS_BUCKET=worker-bucket OSS_ACCESS_KEY_ID=dev-access-key OSS_ACCESS_KEY_SECRET=dev-secret-key`（仅 dev 凭据）。

> **鉴权（`api-auth`）**：除 `/healthz`、`/readyz`、`/metrics`、`POST /api/v1/auth/login` 外，所有 `/api/v1/*` 路由都要求 `Authorization: Bearer <jwt>`，无效/过期 token 返回 `401 unauthenticated`；WS 的 `?token=<jwt>` 同样被校验，失败关闭 `4001`。本地登录拿 token：
> ```sh
> curl -sX POST localhost:8080/api/v1/auth/login \
>   -H 'content-type: application/json' \
>   -d '{"email":"dev@example.com","password":"'"$AUTH_DEV_PASSWORD"'"}'
> # → {"code":0,"data":{"token":"<jwt>","expires_at":"...","user":{...}}}
> curl -s localhost:8080/api/v1/tasks -H "authorization: Bearer <jwt>"
> ```
> MVP 仅校验配置的单一 dev 凭证（`AUTH_DEV_EMAIL`/`AUTH_DEV_PASSWORD`）；真实用户表/口令哈希/refresh token/SSO 是后续工作（`add-api-user-store` 等）。

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

## 任务成本端点（`task-cost-api`）

`add-task-cost-api` 在 `add-task-read-api` 之后再添四个只读端点，覆盖 ARCHITECTURE §5.1 的成本查询面。响应继续走统一 `{code, message, data, trace_id}` 信封；任务/版本未拥有时返回 `404 task_not_found` / `version_not_found`（不返回 `403`，不泄露存在性）。

| 方法 | 路径 | 查询参数 | 说明 |
|------|------|----------|------|
| `GET` | `/api/v1/tasks/{task_id}/cost` | — | 任务级聚合 + 按版本拆分，`data = {task_id, total, by_version}`；尚未结算事件的版本以零成本出现（LEFT JOIN）。 |
| `GET` | `/api/v1/versions/{version_id}/cost` | — | 单版本成本 + 归属 `task_id`，`data = {version_id, task_id, version_no, cost, updated_at}`；尚无 `task_costs` 行时 `updated_at = null` 且金额恒零。 |
| `GET` | `/api/v1/me/cost` | `from`、`to`、`group_by ∈ {day, task_type, model}` | 无 `group_by` 时返回 `data = {total}`；有时返回 `data = {group_by, items[]}`，`key` 升序。 |
| `GET` | `/api/v1/pricing` | — | 当前生效价格表，按 `(resource_kind, resource_name, unit)` 升序，`data = {items[]}`。**owner-agnostic**：所有调用者看到相同响应；只读，价格变更走 SQL 加新行（AGENTS.md §6）。 |

要点：

- **金额一律为十进制字符串**（如 `"1.72000000"`、`"0.01500000"`），保留 8 位小数，沿用 `numericToDecimalString`，避免 `float64` 丢精度。`amount_usd`、`unit_price_usd` 都是字符串。
- **`/me/cost` 数据源是 `cost_events`**（不是 `task_costs`），按 `occurred_at` 过滤（事件时间，而非结算时间——避免回填事件落到错误的桶里）。逐列 SQL 镜像 cost-ingest 的 per-kind 聚合：`tool_calls = SUM(COALESCE(calls,1)) FILTER (kind='tool')`、`wall_time_ms = SUM(COALESCE(duration_ms,0)) FILTER (kind IN ('llm','tool'))`，`amount_usd` 跨 kind 全部累加。`amount_usd` 与 `task_costs` 的总和按位重合（Cost Service 单一事务写两边）。
- **`day` 桶 UTC 锚定**：SQL 用 PG16+ 三参数 `date_trunc('day', occurred_at, 'UTC')`，桶边界不受连接会话 `TimeZone` 影响。
- **窗口规则**：`group_by` 存在时若 `from`/`to` 均缺省则默认 `to = now()`、`from = to - 30d`；`group_by` 存在时强制 `to - from ≤ 366d`（防止单次请求展开成上千个桶）。无 `group_by` 时窗口是 unbounded passthrough。`?group_by=`（空值）等同 absent，沿用 `task-read-api` 的 `status` 过滤约定。
- **400 失败规则**：非法 `group_by`、非法 RFC3339 `from`/`to`、`from >= to`、`group_by` 下窗口超 366d 均返回 `400 invalid_input`，错误信封带 `data.field` 指明出错字段。
- **跨 kind seq=1 不冲突**：由 cost-ingest 的 `(run_id, kind, seq)` 索引保证；本读取层不感知，但事件历史的形状会反映在 `/me/cost?group_by=model` 的桶上。
- **/me/cost 空结果不 404**：返回 `data = {total: zeroCost()}`（无 `group_by`）或 `data = {group_by, items: []}`（有 `group_by`）。

请求/响应契约见 [`openspec/specs/task-cost-api/spec.md`](../openspec/changes/add-task-cost-api/specs/task-cost-api/spec.md)。

## 任务控制端点（`task-control-api`）

`add-task-control-api` 落地了用户停-控任务的入口：`POST /api/v1/tasks/{task_id}/control` 接受 `{action, reason?}`，其中 `action ∈ {pause, resume, cancel}`，`reason` 可选，去尾空格后上限 200 字符（与 `task.title` 校验一致）。**API 永远不直接改 `tasks.status` / `task_versions.status`**——它只往 `outbox` 写一行控制消息；最终状态翻转由 Worker 在事件流中上报、`task-event-ingest` 写入，保留"事件消费是唯一状态写者"的不变量。

| 响应 | 含义 |
|------|------|
| `202 Accepted`（`code=0`） | 请求已落到 `outbox`，将异步生效；`data = {accepted, action, task_id, effective}`，其中 `effective = "queued"` 表示该任务已有 active run，Worker 会在秒级收到；`effective = "best_effort"` 表示尚未 claim（pre-claim 阶段），broker 可能因为没有绑定而丢弃消息（前端可重试） |
| `400 invalid_input` | `action` 缺失或非法、`reason` 超 200 字符、JSON 解析失败、`task_id` 非 UUID。`data.field` 指明出错字段 |
| `404 task_not_found` | 任务不存在或不属于调用者（owner-scoped，与 `task-read-api` 一致；不返回 `403`，不泄露存在性） |
| `409 invalid_state` | 当前 `tasks.status` 不允许该动作。`message` 字段携带当前状态供前端展示（如 `cannot pause task in status "paused"`） |

状态机前置条件：

| action  | 允许的 `tasks.status` |
|---------|------------------------|
| pause   | `pending` / `running` |
| resume  | `paused`               |
| cancel  | 非终态（即不在 `cancelled` / `succeeded` / `failed`） |

要点：

- **幂等性**：API **不** dedupe——重复 pause 会产出两条 outbox 行；Worker 通过内存 flag 在自己一端去重。
- **并发**：同一任务的并发请求在 `LockTaskForControl` 的 `FOR UPDATE` 行锁上序列化，第二个请求看到的是第一个事务提交后的状态。
- **outbox 路由**：每行带 `exchange = "task.control"`、`topic = "task.<task_id>"`。由迁移 `0006_outbox_exchange` 引入的 `outbox.exchange` 列让 Relayer 按行选择目标交换机。
- **拓扑演进**：`task.control` 由 `direct` 改成 `topic`（worker 后续按 `task.*` 模式绑定）。`DeclareTopology` 启动时通过 `retypableExchanges` 列表预删除并重声明；该列表 **append-only**，未来版本不得移除条目，否则跨版本回滚会撞 FAIL-FAST。
- **指标**：`task_control_requests_total{action ∈ {pause,resume,cancel,unknown}, outcome ∈ {accepted,conflict,not_found,invalid}}`。`unknown` 标签只与 `invalid` 配对，用于无法解析的 action。

请求/响应契约见 [`openspec/specs/task-control-api/spec.md`](../openspec/changes/add-task-control-api/specs/task-control-api/spec.md)。

## 实时网关（`realtime-gateway`）

`add-realtime-gateway` 落地了 WebSocket 实时通道的服务端：`GET /api/v1/ws`，Web 客户端（`web/src/services/ws.ts`）零改动接入。连接 URL 用 `?token=<jwt>` 携带令牌；缺失/为空则以 close code **4001**（客户端"鉴权过期"信号）关闭。鉴权身份仍走 stub（`DEV_TENANT_ID` / `DEV_USER_ID`），直到 JWT 落地。

协议（客户端 → 服务端文本帧）：`{op:"subscribe"|"unsubscribe", topics:[...]}` 与 `{op:"ping"}`；topic 形如 `task:<uuid>` / `version:<uuid>`。服务端 → 客户端事件帧固定为 `{topic, kind, seq, ts, payload}`（`kind ∈ {status,log,step,artifact,error}`）。`{op:"ping"}` 回 **应用层** `{op:"pong"}` 文本帧（协议级 pong 不触发浏览器 `onmessage`，空闲连接会误重连）。

要点：

- **每实例 fan-out**：每个网关进程声明自己的 **exclusive / auto-delete / 非持久** 队列，绑定 `task.events`（`event.#`，匹配 worker 的 3 段 key `event.<task_type>.<kind>`），独立于做 DB ingest 的共享 `q.task.events`。该队列随 AMQP **连接**销毁，故消费者在每次（重）连接时 **重新声明 + 重新绑定**（区别于持久队列的 `Consumer`）。网关 **只读**，不写任何表。
- **归属作用域订阅**：`subscribe` 在订阅时按 `(tenant_id, user_id)` 经应用层 `OwnershipChecker` 端口校验（网关不 import `domain/task`）。不存在 ≡ 不属于（都返回 `error` 帧，不泄露存在性）。订阅时校验一次，fan-out 热路径不再查 DB。
- **`ts` 透传**：转发 worker 在事件上盖的权威 `ts`（仅当缺失才回退到接收时间）；客户端按 `seq` 排序，`ts` 仅用于展示。
- **背压**：每连接出站缓冲有界（`WS_SEND_BUFFER`），满则服务端主动关闭慢客户端；客户端重连并经 `GET /versions/{id}/events?after_id=` 按 `seq` 回补，驱逐对用户无损。
- **CSWSH**：因 token 在查询串，握手对 `Origin` 做允许列表校验（`WS_ALLOWED_ORIGINS`）。另设读上限、单帧 topic 数上限、单连接 topic 上限、读超时（回收半开连接）。
- **优雅关停**：关停时先停 fan-out 消费者并以 **1001（going away）** 关闭所有连接（在 drain 窗口内），客户端重连到下一个实例。
- **指标**：`ws_connections_active`、`ws_subscriptions_active`（gauge）、`ws_events_fanned_total{outcome}`、`ws_client_dropped_total{reason}`、`ws_fanout_consumer_connected`（gauge，镜像 `event_consumer_connected`）。token 永不写日志（标准中间件只记 `URL.Path`；网关自身也不记 `RawQuery`）。

> **负载均衡器**：`/ws` 需要 WebSocket 升级透传——放行 `Connection` / `Upgrade` 头、调大空闲超时（要长于 25s 心跳）、**不要把完整 `/ws` URL（含 `?token=`）写进访问日志**。

契约见 [`openspec/specs/realtime-gateway/spec.md`](../openspec/changes/add-realtime-gateway/specs/realtime-gateway/spec.md)。

## 集成测试

`make test-integration` 用 testcontainers 启 PostgreSQL 18.4（artifacts 另起 MinIO、realtime-gateway 另起 RabbitMQ），跑 schema 结构断言、迁移 up→down→up 圈、互斥并发回归、`(run_id, seq)` 幂等性、pricing 不变量、产物 presign 字节往返、WS fan-out 投递 / 归属隔离 / 连接断开后重声明等。需要本机 Docker。CI 仅在 `main` 分支推送时触发 `integration-tests` job，PR 默认 lane 不跑（也不按时间调度执行）。

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
