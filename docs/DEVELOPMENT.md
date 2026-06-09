# 本地开发环境启动说明

本文档说明如何在本机把整套 MVP 链路（依赖栈 + API + Worker + 前端）跑起来，做端到端联调。

> 这是一份**编排层**指南，只负责"按什么顺序、用哪些共享配置把各模块串起来"。
> 各模块的命令清单、完整环境变量、实现细节见对应子目录 README：
> [`api/README.md`](../api/README.md)、[`worker/README.md`](../worker/README.md)、[`web/README.md`](../web/README.md)。
> 架构背景见 [`ARCHITECTURE.md`](ARCHITECTURE.md)。

---

## 1. 前置工具

| 工具 | 版本 | 用途 |
|---|---|---|
| Docker + Docker Compose | 近期版本 | 跑依赖栈（Postgres / RabbitMQ / Redis / SeaweedFS） |
| Go | 1.26+ | `api/` |
| `uv` | 近期版本 | `worker/`，会自动拉取 Python 3.14 |
| Node | 24+ + npm 11 | `web/`（**不使用 pnpm / nvm**） |

可选：`golangci-lint` v2+、`sqlc`（仅改 `api/queries/*.sql` 时）。

---

## 2. 一键启动依赖栈

所有有状态依赖都在仓库根目录的 [`docker-compose.dev.yml`](../docker-compose.dev.yml) 里，凭据均为 **dev-only，禁止用于任何部署环境**。

```sh
# 在仓库根目录
docker compose -f docker-compose.dev.yml up -d

# 可选：附带 Jaeger（trace 可视化，http://localhost:16686）
docker compose -f docker-compose.dev.yml --profile trace up -d

# 查看健康状态
docker compose -f docker-compose.dev.yml ps
```

起来的服务与端口：

| 服务 | 端口 | 说明 |
|---|---|---|
| PostgreSQL | `5432` | 库 `agent_example`，账号 `postgres` / `postgres` |
| RabbitMQ | `5672` / `15672` | AMQP / 管理 UI（`guest` / `guest`） |
| Redis | `6379` | 控制信号 fast-path（Worker 用） |
| SeaweedFS (S3) | `9000` | S3 兼容对象存储，预建桶 `worker-bucket` |
| Jaeger（可选） | `16686` / `4317` / `4318` | UI / OTLP gRPC / OTLP HTTP |

> SeaweedFS 以 `weed mini` 模式启动，按 `S3_BUCKET` 自动建桶，无需 init container。
> S3 API 容器内监听 `8333`，发布到宿主机 `9000`，所以 `OSS_ENDPOINT=http://localhost:9000`。

---

## 3. 共享环境变量

API 与 Worker 共用同一套 OSS / 基础设施配置。建议把下面这段写进 `.env`（已被 `.gitignore` 忽略的本地文件）或直接 `export` 到当前 shell —— **后续步骤假定这些变量已在环境中**。

```sh
# --- 基础设施（对接 docker-compose.dev.yml 默认值）---
export DATABASE_URL=postgres://postgres:postgres@localhost:5432/agent_example?sslmode=disable
export RABBITMQ_URL=amqp://guest:guest@localhost:5672/
export REDIS_URL=redis://localhost:6379/0

# --- 对象存储（api / worker 共用，注意是 ..._KEY_SECRET，非 AWS 习惯）---
export OSS_ENDPOINT=http://localhost:9000
export OSS_BUCKET=worker-bucket
export OSS_ACCESS_KEY_ID=dev-access-key
export OSS_ACCESS_KEY_SECRET=dev-secret-key

# --- API 鉴权（MVP 单一 dev 账号；无默认值，必须显式设置）---
export AUTH_JWT_SECRET=dev-only-jwt-secret-change-me
export AUTH_DEV_EMAIL=dev@example.com
export AUTH_DEV_PASSWORD=dev-password

# --- 可观测（可选；未设则 noop exporter）---
export OTLP_ENDPOINT=http://localhost:4318               # api
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 # worker
```

> 缺少任一 `OSS_*` / `AUTH_JWT_SECRET` / `AUTH_DEV_PASSWORD` 会让 API 启动时 fail-fast。
> 完整变量表（含 WS、连接池、超时等）见各模块 README 的"配置"小节。

---

## 4. 启动后端 API

```sh
cd api
go mod download
make migrate-up   # 应用业务表 schema（首次或迁移更新后）
make run          # 监听 :8080
```

健康检查：

```sh
curl localhost:8080/healthz   # 200 {"status":"ok"}
curl localhost:8080/readyz    # 200 当 PG / RMQ 都通；否则 503 + 失败依赖列表
curl localhost:8080/metrics   # Prometheus 文本
```

> 迁移半途失败会把 schema 置 `dirty`，恢复流程见 [`api/README.md`](../api/README.md#迁移-dirty-状态恢复)。

---

## 5. 启动 Worker

另开一个终端（确保第 3 步的环境变量同样可见）：

```sh
cd worker
make sync   # 安装依赖（自动拉取 Python 3.14）
make run    # 启动消费者；/metrics 在 :9090
```

> 当前 Worker 是 **MVP 脚手架**：能消费任务并跑通 plan→execute→critic→checkpoint→event→artifact 闭环（fake model 无需 API key）。
> 接真实模型需另配 `OPENAI_API_KEY` / `OPENAI_BASE_URL`，详见 [`worker/README.md`](../worker/README.md)。

---

## 6. 启动前端

```sh
cd web
npm install
npm run dev   # http://localhost:5173，HMR
```

前端默认走同源，由 Vite dev proxy 把 `/api`（含 `/api/v1/ws` 的 WS 升级）转发到 `http://localhost:8080`。
如后端不在该地址，设 `VITE_DEV_PROXY_TARGET` 或 `VITE_API_BASE_URL`（见 [`web/README.md`](../web/README.md#环境变量)）。

> 前端**无后端也能跑**：vitest + msw 完整覆盖，登录页接受任意非空 token。

---

## 7. 端到端冒烟

四个组件都起来后，验证"登录 → 建任务 → Worker 执行 → 看到结果"闭环：

```sh
# 1. 登录拿 token
TOKEN=$(curl -sX POST localhost:8080/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d "{\"email\":\"$AUTH_DEV_EMAIL\",\"password\":\"$AUTH_DEV_PASSWORD\"}" \
  | sed -E 's/.*"token":"([^"]+)".*/\1/')

# 2. 建一个任务
curl -sX POST localhost:8080/api/v1/tasks \
  -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"title":"smoke test","task_type":"code-gen","prompt":"hello","lane":"default"}'

# 3. 列任务，看 status 是否随 Worker 执行推进
curl -s localhost:8080/api/v1/tasks -H "authorization: Bearer $TOKEN"
```

或直接在浏览器打开 `http://localhost:5173`，用 `AUTH_DEV_EMAIL` / `AUTH_DEV_PASSWORD` 登录，在 `/tasks/new` 提交任务并在详情页观察实时事件流。

> 数据流转：API 写 `tasks` + `outbox` → Relayer 投递 `execute.*` → Worker 消费执行 → 回发 `task.events` / `cost.events` → API 事件消费者写库并驱动状态机 → 前端经 WS（兜底轮询）刷新。详见 [`ARCHITECTURE.md §6`](ARCHITECTURE.md)。

---

## 8. 停止与清理

```sh
# 停依赖栈（保留数据卷）
docker compose -f docker-compose.dev.yml down

# 连同数据卷一起清掉（重置 PG / RMQ / OSS 状态）
docker compose -f docker-compose.dev.yml down -v
```

API / Worker / 前端进程在各自终端 `Ctrl-C` 即可；API 与 Worker 都做优雅关停（等待 in-flight 任务/请求 drain）。

---

## 9. 常见问题

- **API 启动即退出**：多半是必填环境变量缺失（`DATABASE_URL` / `RABBITMQ_URL` / `AUTH_JWT_SECRET` / `AUTH_DEV_PASSWORD` / 四个 `OSS_*`）。确认第 3 步的变量已 `export` 到**当前** shell。
- **`readyz` 返回 503**：依赖栈未就绪。`docker compose ... ps` 看 health；Postgres / RabbitMQ 首次启动需要几秒。
- **迁移报 dirty**：见 [`api/README.md`](../api/README.md#迁移-dirty-状态恢复)。
- **前端登录请求 404**：dev proxy 没指向后端，检查 `VITE_DEV_PROXY_TARGET` 或后端是否在 `:8080`。
- **任务卡在 `pending`**：Worker 没起或没连上 RabbitMQ；看 Worker 日志与 `:9090/metrics`。
</content>
</invoke>
