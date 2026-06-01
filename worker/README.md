# worker

Worker 服务根目录。

- **技术栈**：Python 3.14 + LangChain `deepagents` 0.6.x + `aio-pika` + `asyncpg` + `aioboto3`
- **职责**：从 RabbitMQ 消费任务 → 调度 deep agent 执行 → 写 checkpoint / 上传 artifact → 发出 `task.events` / `cost.events`
- **目录规划**：见 [`../docs/ARCHITECTURE.md §3.3`](../docs/ARCHITECTURE.md) 以及本仓库 `openspec/changes/init-worker-scaffold/design.md` D11

> 当前实现：**MVP 脚手架**。Dispatcher 永远抛出 `AgentNotImplementedError`，真实 agent 由后续 OpenSpec 提案接入。

## 本地启动

```bash
# 1. 启动依赖栈（postgres + rabbitmq + redis + seaweedfs）
# SeaweedFS 以 `weed mini` 模式启动，按 S3_BUCKET 自动建桶，无需 init container。
docker compose -f ../docker-compose.dev.yml up -d postgres rabbitmq redis seaweedfs

# 2. 安装依赖（自动拉取 Python 3.14）
make sync   # 或: uv sync --extra dev

# 3. 配置环境变量
export RABBITMQ_URL=amqp://guest:guest@localhost:5672/
export DATABASE_URL=postgres://postgres:postgres@localhost:5432/agent_example
export OSS_ENDPOINT=http://localhost:9000      # SeaweedFS S3 API (published as :9000)
export OSS_BUCKET=worker-bucket
export OSS_ACCESS_KEY_ID=dev-access-key        # dev-only creds; see docker-compose.dev.yml seaweedfs.environment
export OSS_ACCESS_KEY_SECRET=dev-secret-key
# 可选
export REDIS_URL=redis://localhost:6379/0
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
export LOG_LEVEL=INFO

# 4. 跑起来
make run    # 或: uv run worker
```

`/metrics` 在 `:9090/metrics` 暴露 Prometheus 指标。

## 常用命令

| 目标 | 用途 |
|------|------|
| `make sync` | 安装依赖（含 dev extras） |
| `make run` | 启动 worker 进程 |
| `make lint` | `ruff check` + `ruff format --check` |
| `make fmt` | 自动格式化 |
| `make type` | `mypy --strict worker/` |
| `make test` | 单元测试（默认跳过 `@pytest.mark.integration`） |
| `make test-int` | 集成测试（需要 Docker / testcontainers） |

## 配置

`pydantic-settings` 同时读取环境变量与 `--config <path>` 给出的 YAML 文件。**env 优先**。

| 必填 |
| --- |
| `RABBITMQ_URL` |
| `DATABASE_URL` |
| `OSS_ENDPOINT` |
| `OSS_BUCKET` |
| `OSS_ACCESS_KEY_ID` |
| `OSS_ACCESS_KEY_SECRET` |

| 选填 | 默认 |
| --- | --- |
| `WORKER_ID` | 自动生成 UUIDv4 |
| `REDIS_URL` | `redis://localhost:6379/0` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 未配置 → noop exporter |
| `LOG_LEVEL` | `INFO` |
| `METRICS_PORT` | `9090` |
| `HEARTBEAT_INTERVAL` | `5` 秒 |
| `CHECKPOINT_INLINE_BYTES` | `8192`（超出走 OSS） |
| `LANE` | `default`（消费 `q.task.execute.<lane>`） |
| `DRAIN_TIMEOUT_SECONDS` | `60` 秒 |
| `CODE_AGENT_MODEL` | `claude-opus-4-7` |
| `RESEARCH_AGENT_MODEL` | `claude-sonnet-4-6` |
| `OPENAI_API_KEY` | 未配置（`SecretStr`，不会出现在日志 / repr） |
| `OPENAI_BASE_URL` | 未配置 → OpenAI 官方；可指向任意 OpenAI 兼容网关 |
| `MAX_STEP_RETRIES` | `2`（每次投递的 critic 重试预算，不跨重投递持久化） |

## Agents

每个 `task_type` 装配一个 agent，由 `ExecutionDispatcher` 按类型查 `AgentRegistry` 路由；未注册的类型抛 `AgentNotImplementedError` → DLX。

- **模型注入（`agents/model.py`）**：agent 通过 `ModelFactory.get(model_key)` 拿模型，绝不直接 import provider SDK。生产用 `ProviderModelFactory`（`ChatOpenAI` + 可选 `base_url`，覆盖 OpenAI 及任意 OpenAI 兼容网关）。`model_key` → 模型名由 `CODE_AGENT_MODEL` / `RESEARCH_AGENT_MODEL` 决定。
- **测试 seam**：测试注入 `FakeModelFactory`（脚本化 `FakeChatModel`），整个 plan→execute→critic→checkpoint→event→artifact 闭环无网络、无 API key 即可跑（见 `tests/support/fake_model.py`）。
- **编排循环（`agents/loop.py`）**：由 worker（而非框架）掌控的 planner→executor→critic 外层循环，逐 step 写 `task_checkpoints`、发 `task.events`（`plan` / `step`），在 step 边界处理 cancel / pause / deadline，从最新 checkpoint 恢复。
  - 实现取舍：`deepagents.create_deep_agent` 需要 `bind_tools` 且内部模型调用次数不确定，无法用脚本化 fake 做确定性逐 step 测试，故 MVP 循环**直接按角色调用模型**；`build_deep_agent` 作为更丰富推理路径保留（详见 design D2b）。
- **MVP 边界**：`code-gen` 只产出文件，不执行代码（无沙箱，ARCHITECTURE §8.4）；`web_search` 为确定性 stub，不引入真实搜索依赖。
- **产物**：成功时执行器产出的文件经 `oss_fs` 工具写入 `ctx.oss_prefix`，并通过 `Persistence.insert_artifact` 记录 `artifacts` 行；上传/落库失败则整 run 失败。

## 关键不变量

- 所有 LLM / tool 调用必须经过 `core/cost_meter.py` 包装（spec: worker-execution-runtime）。
- 每个 step 结束写入 `task_checkpoints`，小状态 (≤ `CHECKPOINT_INLINE_BYTES`) 入 DB JSONB，大状态走 OSS。
- Worker **唯一允许写入**的表见 `core/persistence.py::ALLOWED_WRITE_TABLES` —— `task_runs.last_heartbeat`、`task_checkpoints`、`artifacts`。其它状态翻转通过 `task.events` 让 API / Cost Service 处理。
- 进程内单 in-flight 任务：channel `prefetch_count=1`。
- **控制信号的动态绑定契约**（`core/control.py` + `core/consumer.py`，spec: worker-control-handling）：`task.control` 是 topic exchange，控制队列 `q.task.control.<worker_id>` 不在启动期静态绑定，而是**按 claim 动态绑定**——consumer claim run *之前* `bind_for(task_id)`（routing key `task.<task_id>`），run 在任何路径终止时 `unbind_for(task_id)`（best-effort，队列断连 auto-delete 兜底）。dispatcher 按 `current_run.run_id` 过滤投递，bind/unbind 竞态窗口内到达的陈旧消息被安全丢弃。listener 收到 pause/resume/cancel 时翻转内存 token *并*发出 `kind=status` 确认事件（`paused`/`running`/`cancelling`）让前端状态收敛；cancel 先 set cancel token 再 `pause_token.resume()` 以唤醒正阻塞在 `wait_if_paused()` 的 agent（cancel-during-pause race fix）。

## 插件目录约定

```
worker/plugins/
├── tool/
│   └── <name>/
│       ├── plugin.yaml      # kind/name/version/entrypoint/applies_to/permissions
│       └── handler.py       # `entrypoint` 指向的 callable
└── subagent/
    └── <name>/
        ├── plugin.yaml
        └── prompt.md
```

`plugin.yaml` 示例（最小集）：

```yaml
kind: tool                    # tool | subagent
name: web_search
version: 1.2.0
entrypoint: worker.plugins.tool.web_search.handler:search
schema:
  input: { type: object, properties: { query: { type: string } }, required: [query] }
  output: { type: object }
permissions:
  - network.egress
applies_to:
  task_types: [research]
resources:
  timeout_s: 30
```

启动期 Plugin Loader 扫描 `worker/plugins/{tool,subagent}/*/plugin.yaml`，校验通过后注册到内存 registry；entrypoint 按需懒导入。**新增插件不需要修改核心代码。**

## 与 OpenSpec 的关系

本目录由变更 `init-worker-scaffold` 引入。后续修改公共契约（事件 schema、新写入目标、新插件类型）必须先经 OpenSpec 提案；微调（注释、参数微调、内部重命名）可直接 PR。
