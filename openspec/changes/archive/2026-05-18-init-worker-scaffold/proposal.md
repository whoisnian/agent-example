## Why

`worker/` 当前是空目录。每一条 Worker 业务能力（code-gen agent、research agent、新插件等）都依赖统一的进程骨架：配置、日志、tracing、RabbitMQ 消费、心跳、成本计量包装、checkpoint 读写、插件加载、OSS 访问。本提案一次性建立这套地基，使后续业务提案能聚焦"agent / plugin 本身"而非每次重新铺基础设施。

本提案 **不交付任何 agent 实现**；只交付一个可以 `uv run worker` 启动、能连接 RMQ / PG / OSS、能消费 `task.execute` 队列、能正确发出 `task.events` / `cost.events` 但 **业务执行直接返回 `unimplemented` 错误** 的最小骨架。

## What Changes

- 新建 `worker/` 目录及包结构（`core/`、`agents/`、`plugins/`、`tests/`），见 ARCHITECTURE §3.3
- 选定包管理：**uv**（更快、锁文件简单，与 Python 3.14 + LangChain 生态匹配）
- 选定核心库：
  - LLM agent 框架：`langchain` + `deepagents`（按 ARCHITECTURE 选型）
  - RMQ：`aio-pika`（asyncio 风格）
  - PG：`asyncpg`（仅写 `task_runs.last_heartbeat` / `task_checkpoints` / `artifacts` / `cost_events` 这几张 worker-only 的表；不碰 `tasks` / `task_versions`）
  - OSS：`aioboto3`（S3 兼容客户端，OSS endpoint 可配）
  - 配置：`pydantic-settings`
  - 日志：`structlog` + JSON
  - Observability：`opentelemetry-sdk` + `prometheus-client`
- 实现进程级骨架能力：
  - 配置加载（env + 可选 yaml；失败 fail-fast）
  - 进程生命周期：启动、就绪上报、`SIGTERM` 优雅退出（拒绝新消息、把当前消息要么完成要么 nack-requeue、关闭连接）
  - 结构化日志（强制带 `task_id` / `run_id` / `step` / `trace_id`）
  - OTel tracing：每条消息一个根 span，所有 LLM/tool 调用作为子 span
  - Prometheus `/metrics`（独立端口 `9090`，因 Worker 无业务 HTTP）
- RMQ 消费基础：
  - 通用 consumer 抽象（`prefetch=1`、手动 ack、幂等键去重、长任务心跳）
  - 控制信号双通道：`q.task.control.<worker_id>` + Redis pub/sub fast-path（Redis 客户端 `redis-py.asyncio`，骨架仅留接口）
  - 事件发布器（`task.events` + `cost.events`），统一携带 `idempotency_key = run_id + seq`
- 执行运行时基础：
  - Run 上下文 `RunContext`（包含 `task_id` / `run_id` / OSS prefix / cancel token / cost meter / logger）
  - **Cost Meter 包装**：所有 LLM 调用 (通过 LangChain `BaseCallbackHandler`) 与 Tool 调用 (装饰器) 必须经此采集 token / duration → 发 `cost.<kind>` 事件
  - **Checkpoint 读写**：小状态写 DB（asyncpg），大状态写 OSS；`(run_id, step_seq)` 幂等
  - **Heartbeat**：5 秒一次 `UPDATE task_runs SET last_heartbeat = now() WHERE id = $1`
  - **Plugin Loader 骨架**：扫描 `worker/plugins/{tool,subagent}/<name>/plugin.yaml` → 注册到内存表；MVP 不包含实际插件（除一个用于自测的 `noop_tool`）
- 引入 `Makefile`（`make run / lint / test / type / fmt`）
- 引入 `pyproject.toml`、`uv.lock`、`.python-version`（pin Python 3.14）
- 复用根目录 `docker-compose.dev.yml`（由 `init-api-scaffold` 引入）；如该提案尚未合入则在本提案中先行新增
- CI workflow `.github/workflows/worker-ci.yml`：lint (`ruff`) + type (`mypy --strict`) + test (`pytest -x`) + build (`uv sync --frozen`)

非目标：
- code-gen / research 任意 agent 实现（各自独立提案）
- 真实工具（web_search、run_node、oss_fs 等）实现（独立提案）
- 真实 sandbox（子进程 / Docker / microVM）——MVP scaffold 只跑在 Worker 进程内
- 与 API 端 Cost Service 的真实结算交互（Worker 只发 cost event，Cost Service 在 API 端落库）

## Capabilities

### New Capabilities

- `worker-bootstrap`: Worker 进程骨架 —— 配置加载、结构化日志、OTel tracing、Prometheus 指标、健康上报、优雅关停、插件注册表初始化
- `worker-messaging`: RabbitMQ 消费与发布抽象 —— `task.execute` 消费（prefetch=1、幂等去重、ack 语义）、控制信号监听、`task.events` / `cost.events` 发布
- `worker-execution-runtime`: 单次 task run 的运行时支撑 —— RunContext、Cost Meter 包装、Checkpoint 读写、Heartbeat、Plugin Loader

### Modified Capabilities

（无 —— `openspec/specs/` 仍为空）

## Impact

- **代码**：`worker/` 从空目录变为可运行骨架；新增约 1.2k–1.6k 行 Python 代码 + 测试
- **依赖**：引入 langchain、deepagents、aio-pika、asyncpg、aioboto3、structlog、opentelemetry-sdk、prometheus-client、pydantic-settings、redis(.asyncio)
- **本地开发**：复用 / 新增 `docker-compose.dev.yml`（含 PG、RabbitMQ、Redis、SeaweedFS 作为 OSS 兼容后端，含一次性 `seaweedfs-init` 桶引导容器）；新增 `.python-version`
- **CI**：新增 GitHub Actions workflow（lint + type + test）；预计 < 3 min
- **文档**：`worker/README.md` 升级为含本地启动步骤 + 插件目录约定；`docs/ARCHITECTURE.md` 无需改动
- **跨服务契约**：
  - 发布到 RMQ 的事件结构必须与 `init-api-scaffold` 提供的消费者契约一致（envelope `{msg_id, idempotency_key, payload, occurred_at}`）；本提案与 `init-api-scaffold` 并行推进时，事件 schema 在两份 spec 中显式重述以避免漂移
  - 直接写入 PG 的表仅限：`task_runs` 的 `last_heartbeat`、`task_checkpoints`、`artifacts`、`cost_events`；其余表只读或不碰
- **下游解锁**：`add-worker-code-agent`、`add-worker-research-agent`、各 `add-tool-*` 与 `add-subagent-*` 提案均依赖本骨架
