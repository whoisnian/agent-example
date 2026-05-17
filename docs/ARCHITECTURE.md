# Agent Task Platform — 生产级系统架构设计方案

> 面向"用户在前端提交任务 → 后端编排 → Worker (deepagents) 执行 → 持续迭代/版本化"的多用户任务平台。
> 本文档定义模块划分、接口契约、数据模型、关键交互流程与非功能保障；不涉及具体实现代码与部署脚本。
>
> **本版本以 MVP 为目标**：聚焦"创建 / 执行 / 实时观测 / 控制 / 迭代 / 回滚 / 成本统计"核心链路。文中标记 `[MVP]` 的内容为首版必交付；标记 `[Post-MVP]` 的内容为后续演进，先行预留接口/数据空间但不实现。

---

## 1. 设计目标与非功能需求

### 1.1 核心能力
- `[MVP]` 任务全生命周期管理：创建 / 执行 / 实时监控 / 暂停 / 取消 / 完成 / 迭代 / 回滚。
- `[MVP]` 支持长时任务（分钟～小时级），过程中可观测、可干预、可恢复。
- `[MVP]` 任务"版本化迭代"：在已有任务基础上派生新版本，保留树形版本历史。
- `[MVP]` **任务级互斥**：同一 task 同时只允许一个活跃版本（处于 pending/queued/running/paused/cancelling 中任一状态），活跃期间拒绝重复提交（创建/迭代/回滚），需返回 409，DB 层加唯一索引兜底。
- `[MVP]` **任务成本统计**：按 token 消耗（input/output/cached）、执行时长、工具调用次数等维度计算成本，沉淀到 version / task 两级，前端可见。
- `[MVP]` Worker 端通过插件机制扩展能力（tool / subagent / skill），核心代码不动；MVP 仅交付 tool + subagent 两类，Skill 为 Post-MVP。
- `[Post-MVP]` 多 lane / 多区域 / 灰度 / 插件市场 / 多人协作 等。

### 1.2 非功能目标
| 维度 | 目标 |
|------|------|
| 可扩展性 | 各组件均可水平扩展；无状态服务可无损扩缩容 |
| 可用性 | 单组件故障不影响整体；核心链路 SLA ≥ 99.9% |
| 一致性 | DB 与 MQ 之间使用 Outbox + 幂等消费，保证最终一致 |
| 故障恢复 | Worker 崩溃后由 Checkpoint 恢复，任务最终完成或进入人工干预 |
| 可观测性 | Metrics / Logs / Tracing 三位一体，任务级别链路追踪 |
| 安全 | 多租户隔离、最小权限、传输与静态加密 |

### 1.3 技术选型
| 层 | 选型 | 备选/补充 |
|---|---|---|
| 前端 | React + TypeScript + Vite + TailwindCSS + Zustand + React Query | 纯 Web；不做桌面壳 |
| 后端 API | Golang (Gin/Echo) + sqlc/ent + zap/slog | gRPC 用于内部服务通讯 |
| Worker | Python + LangChain `deepagents` 框架 | uv/poetry 包管理 |
| DB | PostgreSQL 15+（主从 + 逻辑复制） | pgbouncer 连接池；Citus 备用分片 |
| 对象存储 | OSS（阿里云 / S3 兼容） | 多桶多区域 |
| MQ | RabbitMQ 集群（quorum queue） | Kafka 备选（高吞吐场景） |
| 缓存/PubSub | Redis Cluster | 也用于会话、限流、控制信号 |
| 网关 | Nginx / APISIX | 统一鉴权、限流、WebSocket 长连接 |
| 可观测 | OpenTelemetry + Prometheus + Grafana + Loki/ELK | Sentry 用于异常聚合 |
| 部署 | Kubernetes + Helm + ArgoCD | 多 AZ；HPA + PDB |

---

## 2. 总体架构

### 2.1 架构图（逻辑视图）

```
                ┌─────────────────────────────────────────────────────────┐
                │                       Client                             │
                │                   React Web (SPA)                        │
                └───────┬──────────────────┬──────────────────────────────┘
              REST/HTTPS│         WebSocket│ (任务事件流)
                        ▼                  ▼
                ┌─────────────────────────────────────┐
                │           API Gateway (APISIX)       │  鉴权 / 限流 / 路由
                └───────┬─────────────────────┬───────┘
                        ▼                     ▼
        ┌──────────────────────┐   ┌──────────────────────┐
        │  Backend API (Go)    │   │  Realtime Gateway(Go)│
        │  - Task Service      │   │  - WS Hub            │
        │  - Version Service   │   │  - 订阅 task.events  │
        │  - User Service      │   │  - 推送给前端        │
        │  - Artifact Service  │   └─────────▲────────────┘
        │  - Outbox Relayer    │             │
        └───┬──────────┬───────┘             │
            │          │                     │
   SQL/读写 │          │ Publish             │ Subscribe
            ▼          ▼                     │
       ┌────────────────┐  ┌────────────────────────────────┐
       │ PostgreSQL HA  │  │       RabbitMQ Cluster         │
       │  - tasks/...   │  │  X: task.exchange (topic)      │
       │  - outbox      │  │   ├─ q.task.execute.<lane>     │
       └────────────────┘  │   ├─ q.task.control.<worker_id>│
                           │   ├─ q.task.events             │
                           │   └─ q.task.dlq                │
                           └────────────────┬───────────────┘
                                            │
                ┌───────────────────────────┴───────────────┐
                ▼                                           ▼
        ┌──────────────────┐                   ┌──────────────────┐
        │ Worker Pool A    │   ...             │ Worker Pool N    │
        │ (deepagents)     │                   │ (deepagents)     │
        │  - Plugin Loader │                   │                  │
        │  - Sandbox       │                   │                  │
        │  - Checkpointer  │                   │                  │
        └──┬───────┬───────┘                   └──────────────────┘
           │       │
   读/写文件│       │ 心跳 / 事件
           ▼       ▼
       ┌────────┐  RabbitMQ (task.events) → Realtime Gateway
       │  OSS   │
       └────────┘

(Redis 作为：限流计数、控制信号 fast-path、热点缓存)
```

### 2.2 组件清单与职责

| 组件 | 职责 | 状态 | 扩展方式 |
|------|------|------|----------|
| Web Client | 任务交互、版本树展示、实时进度、成本面板 | 无状态 | CDN + 多副本 |
| API Gateway | 入口鉴权、限流、路由、WS 终止 | 无状态 | 水平扩展 |
| Backend API | 业务逻辑、任务编排、版本管理 | 无状态 | 水平扩展 |
| Realtime Gateway | WebSocket 长连接，扇出事件 | 弱状态（连接） | 一致性哈希分片 |
| Outbox Relayer | 把 outbox 表事件可靠投递到 MQ | 无状态 | Leader 选主 / 多副本竞争 |
| PostgreSQL | 业务持久化 | 强状态 | 主从、读写分离、必要时分片 |
| RabbitMQ | 任务调度、控制信号、事件总线 | 强状态 | Quorum Queue 集群 |
| OSS | 大文件、产物、日志 | 强状态 | 桶分区、多 AZ |
| Redis | 缓存、速率、控制 fast-path | 弱状态 | Cluster 模式 |
| Worker (deepagents) | 实际任务执行、调用插件 | 无状态（状态在 DB/OSS） | 横向扩展 + 多 lane |
| Observability Stack | 指标/日志/追踪 | 独立栈 | — |

---

## 3. 模块划分

### 3.1 前端模块（React）

```
src/
├── pages/
│   ├── TaskCreate/         # 任务创建表单（支持模板）
│   ├── TaskList/           # 任务列表 + 状态筛选 + 累计成本列
│   ├── TaskDetail/         # 任务详情：实时日志 / 中间产物 / 控制按钮 / 成本面板
│   ├── VersionTree/        # 版本树可视化（react-flow），节点上展示该版本成本
│   ├── ArtifactViewer/     # 预览代码包 / Markdown / 文件树
│   ├── CostDashboard/      # 用户视角累计成本（按天/任务/模型聚合）
│   └── Settings/
├── features/
│   ├── tasks/              # 任务 slice：API + 状态
│   ├── versions/           # 版本管理
│   ├── realtime/           # WS 客户端、事件总线
│   ├── costs/              # 成本聚合查询与展示
│   └── auth/
├── services/
│   ├── http.ts             # 基于 fetch/axios，统一拦截
│   ├── ws.ts               # 自动重连、心跳、订阅多任务
│   └── upload.ts           # OSS 直传（STS 临时凭证）
└── components/             # 通用 UI（含 CostBadge / TokenBar 等）
```

关键设计：
- **状态分层**：本地 UI 状态用 Zustand；服务端状态用 React Query（自带缓存、失效、轮询兜底）。
- **实时通道**：MVP 默认 WebSocket，失败降级为 5s 轮询；SSE 与更复杂的多级降级为 Post-MVP。
- **大文件上传**：前端拿临时 STS 凭证后直传 OSS，不走后端 API，避免后端带宽瓶颈。
- **版本树**：MVP 限制为父子树（每个版本最多一个 parent，无 merge），客户端虚拟滚动加载。
- **互斥提交保护**：前端在 task.status 处于活跃态时禁用"迭代/回滚"按钮并提示原因；提交时仍以后端 409 为准（后端是真相之源）。
- **成本展示**：
  - TaskDetail 顶栏显示该 task 累计 cost（USD）+ token 数（input/output/cached 分项），以及当前 running 版本的实时累计；
  - VersionTree 每个节点 hover 时显示该 version 的成本与耗时；
  - CostDashboard 提供按日期 / task_type / model 聚合的图表。

### 3.2 后端 API（Golang）

按 DDD 风格分层，避免巨石：

```
cmd/api/                     # main 入口
internal/
├── interfaces/http/         # HTTP handler / 路由 / DTO
├── interfaces/ws/           # WS hub & 订阅管理
├── application/             # 用例编排
│   ├── task/
│   ├── version/
│   └── artifact/
├── domain/                  # 领域模型 + 业务不变量
│   ├── task/                # Task, Status, Transition
│   ├── version/             # Version DAG
│   └── event/               # Domain Event
├── infrastructure/
│   ├── persistence/         # Repo 实现 (sqlc)
│   ├── messaging/           # RabbitMQ Publisher / Consumer
│   ├── storage/             # OSS 客户端 / STS
│   ├── cache/               # Redis
│   └── outbox/              # Outbox 表 + Relayer
└── pkg/                     # 通用：log, errors, idgen, auth
```

服务边界：
- **Task Service**：任务 CRUD、状态机推进、控制指令派发；**所有"创建活跃版本"的操作（create/iterate/rollback-branch）必须先在 DB 事务内做互斥检查**（详见 §6.4）。
- **Version Service**：基于父版本创建新版本，维护版本树，提供回滚（= 以指定版本为父创建新版本）。
- **Artifact Service**：管理 OSS 对象元数据，颁发 STS 临时凭证。
- **Cost Service**：消费 `cost.events`，做模型价格表查询、维度聚合、写入 `task_costs` 与 `cost_events`；对外暴露按 task/version/user/time 范围聚合的查询接口。
- **Realtime Gateway**：独立部署进程，订阅 `task.events`，按用户/任务维度推送给已订阅的 WS 客户端；成本类增量事件也走这条通道下发，前端不必额外轮询。
- **Outbox Relayer**：扫描 `outbox` 表未投递事件，发布到 RabbitMQ，发布成功后标记，保证"DB 状态变化 ↔ MQ 事件"的原子性。

### 3.3 Worker（Python + deepagents）

```
worker/
├── main.py                  # 启动 + 消费者注册
├── core/
│   ├── consumer.py          # RabbitMQ 消费 (aio-pika)
│   ├── orchestrator.py      # 任务执行编排
│   ├── checkpoint.py        # Checkpoint 读写（DB + OSS）
│   ├── control.py           # 暂停/取消信号监听
│   ├── heartbeat.py         # 周期上报 worker_runs.last_heartbeat
│   ├── event_publisher.py   # 进度事件上报 (task.events)
│   ├── cost_meter.py        # 包装 LLM/tool 调用，捕获 token usage & 耗时，发 cost.events
│   └── sandbox.py           # 子进程 / Docker 隔离（MVP 仅子进程；microVM 为 Post-MVP）
├── agents/
│   ├── base.py              # 基于 deepagents.create_deep_agent
│   ├── code_agent.py        # 代码生成任务专用
│   ├── research_agent.py    # 调研任务专用
│   └── registry.py          # 任务类型 → Agent 映射
├── plugins/                 # 插件目录（详见 §8）
│   ├── tools/
│   ├── subagents/
│   └── skills/
└── tests/
```

#### 3.3.1 Agent 编排（基于 deepagents）

每个 Worker 进程在启动时根据消费的 `task_type` 装配一个 deep agent：
- **Planner subagent**：把粗粒度任务拆解成可执行步骤，写入 `task.plan`（持久化到 DB）。
- **Executor subagents**：按步执行，可调用 tools。
- **Critic subagent**：阶段性自审，决定是否进入下一步或重试。
- **Filesystem tool**：基于 deepagents 内置 fs 工具，但底层挂载到任务专属的 OSS 前缀（虚拟文件系统）。

#### 3.3.2 Checkpoint 机制
- 任务被拆为多个 step；每个 step 完成后写入 `task_checkpoints`（小状态写 DB，大产物写 OSS，DB 仅存 OSS key + hash）。
- 重启或被另一个 Worker 接管时，从最新 checkpoint 恢复，重放最近一步而非全部重来。
- Checkpoint 写入需幂等（含 step_seq 唯一约束）。

#### 3.3.3 成本采集（Cost Meter）
- 通过 LangChain Callback（`on_llm_end`）+ 自定义 tool wrapper 在 Worker 侧捕获：
  - LLM：`model`, `prompt_tokens`, `completion_tokens`, `cached_tokens`, `latency_ms`。
  - Tool/Subagent 调用：`name`, `latency_ms`, `external_cost`（若工具自带定价，如 web_search API）。
- Run 启动/结束时打点采集 `wall_time_ms` 与 `worker_compute_seconds`（用于折算 compute 成本）。
- 每条 cost event 含 `idempotency_key = run_id + seq`，通过 `cost.events` exchange 发出；Cost Service 消费时按价格表换算为 USD，写入 DB。
- 价格表由 Cost Service 配置加载（`pricing.yaml`），变更会附 `effective_at`，历史已结算成本不回溯（按发生时刻定价）。
- 失败容忍：cost event 丢失不影响任务成功；但持续丢失会触发"成本可观测性"告警。

### 3.4 PostgreSQL

- **主库** 一主写入，多只读副本通过流复制；读写分离由应用层路由（用例级别决定）。
- **连接池** pgbouncer 在 transaction 模式。
- **分库分表**：MVP 阶段单库；按 `tenant_id` 做水平分片预留键空间，必要时引入 Citus 或者按租户拆物理库。
- **重要表**见 §5。

### 3.5 OSS

- 桶规划：
  - `inputs/`：用户上传输入。
  - `artifacts/`：任务产物（代码包 zip、报告 md、图片等）。
  - `checkpoints/`：Worker 大尺寸 checkpoint 状态。
  - `logs/`：任务运行日志归档。
- 命名：`{tenant_id}/{task_id}/{version_id}/{type}/{filename}` —— 便于按租户审计、版本回收。
- 生命周期：`logs/` 90 天转低频，180 天归档；`checkpoints/` 任务终态后 7 天清理。
- 安全：使用 STS 临时凭证；前端直传时绑定 path 前缀。

### 3.6 RabbitMQ

#### 交换机与队列设计

| Exchange | 类型 | 用途 |
|---|---|---|
| `task.exchange` | topic | 任务调度主交换机 |
| `task.control` | direct | 控制信号（暂停/取消） |
| `task.events` | topic | 任务事件（状态变化、日志、产物） |
| `task.dlx` | direct | 死信交换机 |

| Queue | 绑定键 | 备注 |
|---|---|---|
| `q.task.execute.<lane>` | `execute.<task_type>.<lane>` | 按"任务类型 + lane"分流；lane 用于隔离大任务/小任务 |
| `q.task.control.<worker_id>` | `control.<worker_id>` | 控制信号点对点投递到具体 worker |
| `q.task.events` | `event.#` | Realtime Gateway 消费 |
| `q.task.dlq` | — | 死信队列 |

- 全部使用 **Quorum Queue**（基于 Raft）保证一致性、磁盘持久化。
- 消费者 `prefetch=1`，长任务避免阻塞队列。
- 消息含 `idempotency_key` = `task_id + attempt_id`，消费者侧幂等。
- 重试：先级内重试（5 次指数退避），耗尽进入 DLQ；DLQ 由人工或自动 reconciler 处理。

---

## 4. 数据模型

### 4.1 实体关系（核心）

```
tenants ──< users
                │
                ▼
              tasks ──< task_versions ──< task_runs ──< task_events
                                  │                 │
                                  │                 └──< cost_events ──┐
                                  ├──< task_checkpoints                 │
                                  └──< artifacts ──> (OSS)              ▼
                                  └──< task_costs (聚合, 1:1 with version)
worker_registry ──< worker_runs (= task_runs.worker_run_id)
pricing                          (模型/工具单价表, 含 effective_at)
outbox
```

### 4.2 关键表 DDL（精简版）

```sql
-- 任务主表：逻辑上代表一个用户意图
CREATE TABLE tasks (
  id              UUID PRIMARY KEY,
  tenant_id       UUID NOT NULL,
  user_id         UUID NOT NULL,
  title           TEXT NOT NULL,
  task_type       TEXT NOT NULL,           -- code-gen / research / ...
  status          TEXT NOT NULL,           -- pending/running/paused/cancelled/succeeded/failed
  current_version UUID,                    -- 指向当前激活版本
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON tasks (tenant_id, user_id, status);

-- 版本：每次迭代/回滚生成一个新版本，构成树
CREATE TABLE task_versions (
  id               UUID PRIMARY KEY,
  task_id          UUID NOT NULL REFERENCES tasks(id),
  parent_id        UUID REFERENCES task_versions(id),  -- 父版本（MVP: 严格树）
  version_no       INT  NOT NULL,                      -- 在 task 内自增
  prompt           TEXT NOT NULL,                      -- 用户输入（增量描述）
  params           JSONB NOT NULL DEFAULT '{}'::jsonb,
  status           TEXT NOT NULL,                      -- pending/queued/running/paused/cancelling/cancelled/succeeded/failed
  is_active        BOOLEAN GENERATED ALWAYS AS
                     (status IN ('pending','queued','running','paused','cancelling')) STORED,
  artifact_root    TEXT,                               -- OSS 前缀
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (task_id, version_no)
);
CREATE INDEX ON task_versions (task_id, parent_id);

-- ★ 任务级互斥：同一 task 至多一个活跃版本（DB 强约束）
CREATE UNIQUE INDEX one_active_version_per_task
  ON task_versions (task_id) WHERE is_active;

-- 一次具体执行实例：一个 version 可能因失败重试产生多个 run
CREATE TABLE task_runs (
  id                UUID PRIMARY KEY,
  version_id        UUID NOT NULL REFERENCES task_versions(id),
  attempt_no        INT  NOT NULL,
  worker_run_id     UUID,
  status            TEXT NOT NULL,         -- queued/running/paused/cancelled/succeeded/failed
  started_at        TIMESTAMPTZ,
  ended_at          TIMESTAMPTZ,
  last_heartbeat    TIMESTAMPTZ,
  error             JSONB,                 -- {code, message, stack_oss_key}
  idempotency_key   TEXT NOT NULL UNIQUE,
  UNIQUE (version_id, attempt_no)
);
CREATE INDEX ON task_runs (status, last_heartbeat);  -- 用于 reaper

-- 事件流：状态、进度、日志摘要（明细日志写 OSS）
CREATE TABLE task_events (
  id           BIGSERIAL PRIMARY KEY,
  task_id      UUID NOT NULL,
  version_id   UUID NOT NULL,
  run_id       UUID,
  seq          BIGINT NOT NULL,            -- 同一 run 内单调递增
  kind         TEXT NOT NULL,              -- status/log/step/artifact/error
  payload      JSONB NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON task_events (task_id, id);
CREATE UNIQUE INDEX ON task_events (run_id, seq);   -- 幂等性

-- Checkpoint：可恢复点
CREATE TABLE task_checkpoints (
  id           UUID PRIMARY KEY,
  run_id       UUID NOT NULL REFERENCES task_runs(id),
  step_seq     INT  NOT NULL,
  step_name    TEXT NOT NULL,
  state        JSONB NOT NULL,             -- 小状态；大状态用 oss_key 引用
  oss_key      TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (run_id, step_seq)
);

-- 产物：OSS 对象元数据
CREATE TABLE artifacts (
  id           UUID PRIMARY KEY,
  version_id   UUID NOT NULL REFERENCES task_versions(id),
  kind         TEXT NOT NULL,              -- code-bundle/report/image/log
  oss_key      TEXT NOT NULL,
  mime         TEXT,
  bytes        BIGINT,
  sha256       TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 价格表：模型/工具单价；变更不回溯，按 effective_at 取生效行
CREATE TABLE pricing (
  id              UUID PRIMARY KEY,
  resource_kind   TEXT NOT NULL,            -- llm | tool | compute
  resource_name   TEXT NOT NULL,            -- e.g. claude-opus-4-7 / web_search / worker_compute_s
  unit            TEXT NOT NULL,            -- per_1k_input_tokens / per_1k_output_tokens / per_call / per_second
  unit_price_usd  NUMERIC(18,8) NOT NULL,
  effective_at    TIMESTAMPTZ NOT NULL,
  expires_at      TIMESTAMPTZ,
  UNIQUE (resource_kind, resource_name, unit, effective_at)
);

-- 成本明细事件：每次 LLM/tool 调用 / 计时打点一条
CREATE TABLE cost_events (
  id              BIGSERIAL PRIMARY KEY,
  task_id         UUID NOT NULL,
  version_id      UUID NOT NULL,
  run_id          UUID NOT NULL,
  seq             BIGINT NOT NULL,
  kind            TEXT NOT NULL,            -- llm | tool | compute
  resource_name   TEXT NOT NULL,            -- 关联 pricing.resource_name
  -- LLM 维度
  input_tokens    BIGINT,
  output_tokens   BIGINT,
  cached_tokens   BIGINT,
  -- 通用
  calls           INT,
  duration_ms     BIGINT,
  -- 结算
  amount_usd      NUMERIC(18,8) NOT NULL,   -- 按发生时价格表换算
  pricing_id      UUID REFERENCES pricing(id),
  occurred_at     TIMESTAMPTZ NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ON cost_events (run_id, seq);              -- 幂等
CREATE INDEX ON cost_events (task_id, occurred_at);
CREATE INDEX ON cost_events (version_id);

-- 成本聚合（按 version 与按 task 两级；用 trigger 或 Cost Service 维护）
CREATE TABLE task_costs (
  version_id      UUID PRIMARY KEY REFERENCES task_versions(id),
  task_id         UUID NOT NULL,
  input_tokens    BIGINT NOT NULL DEFAULT 0,
  output_tokens   BIGINT NOT NULL DEFAULT 0,
  cached_tokens   BIGINT NOT NULL DEFAULT 0,
  tool_calls      INT    NOT NULL DEFAULT 0,
  wall_time_ms    BIGINT NOT NULL DEFAULT 0,
  compute_seconds BIGINT NOT NULL DEFAULT 0,
  amount_usd      NUMERIC(18,8) NOT NULL DEFAULT 0,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON task_costs (task_id);
-- task 维度聚合通过视图或物化视图（MVP 用普通视图 SUM(task_costs)）

-- Worker 注册与心跳
CREATE TABLE worker_registry (
  id              UUID PRIMARY KEY,
  hostname        TEXT NOT NULL,
  capabilities    TEXT[] NOT NULL,         -- 支持的 task_type/插件
  lanes           TEXT[] NOT NULL,
  version         TEXT NOT NULL,
  status          TEXT NOT NULL,           -- online/draining/offline
  last_heartbeat  TIMESTAMPTZ NOT NULL
);

-- Outbox：与业务表同事务写入，由 Relayer 投递
CREATE TABLE outbox (
  id           BIGSERIAL PRIMARY KEY,
  aggregate    TEXT NOT NULL,              -- task/version/run
  aggregate_id UUID NOT NULL,
  topic        TEXT NOT NULL,
  payload      JSONB NOT NULL,
  status       TEXT NOT NULL DEFAULT 'pending',  -- pending/sent/failed
  attempts     INT  NOT NULL DEFAULT 0,
  next_retry_at TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON outbox (status, next_retry_at);
```

### 4.3 任务状态机

```
              ┌──────────┐
   create →   │ pending  │
              └────┬─────┘
                   │ dequeue
                   ▼
              ┌──────────┐  pause   ┌──────────┐
              │ running  │ ───────► │  paused  │
              └─┬──┬─────┘ ◄─────── └────┬─────┘
                │  │ cancel             │ cancel
                │  ▼                    ▼
                │ ┌───────────┐    ┌───────────┐
                │ │ cancelled │    │ cancelled │
                │ └───────────┘    └───────────┘
                │
                │ done                  fail (after retries)
                ▼                        ▼
           ┌────────────┐         ┌─────────┐
           │ succeeded  │         │ failed  │
           └────────────┘         └─────────┘
```

`task.status` 是 versions 中"当前活跃 version 的 run 状态"的派生量，由 Domain Service 维护一致性。

### 4.4 版本 DAG

- 每个 `task_versions` 行有可空 `parent_id`，构成有根 DAG（实际上是树或带 merge 的图，但 MVP 阶段限制为树）。
- **迭代**：基于 V_n 创建 V_{n+1}，parent=V_n，prompt 为补充描述；执行时 Agent 拿到 parent.artifact_root 作为基底。
- **回滚**：以 V_k（历史版本）为 parent 创建 V_{n+1}，prompt 为空或自动生成"rollback to V_k"；并将 `tasks.current_version` 指向 V_{n+1}。
- 用户视角下"回滚后再迭代"自然形成多分支 DAG。

---

## 5. 接口定义

### 5.1 REST API（v1 草案）

所有路径前缀 `/api/v1`，统一返回 `{code, message, data, trace_id}`。

| Method | Path | 描述 |
|---|---|---|
| POST   | `/tasks` | 创建任务（隐式创建首版本） |
| GET    | `/tasks` | 列表（分页 + 过滤，含累计成本） |
| GET    | `/tasks/{task_id}` | 详情（含当前版本摘要 + 成本摘要） |
| POST   | `/tasks/{task_id}/control` | 控制：pause/resume/cancel |
| POST   | `/tasks/{task_id}/iterate` | 基于当前版本迭代 → 新版本（活跃中返回 **409 active_version_exists**） |
| POST   | `/tasks/{task_id}/rollback` | 回滚到指定版本（branch 模式同样受 409 约束；switch 模式仅切指针不受约束） |
| GET    | `/tasks/{task_id}/versions` | 版本列表（树） |
| GET    | `/versions/{version_id}` | 版本详情 |
| GET    | `/versions/{version_id}/events?after_id=...` | 事件流（用于断线后补齐） |
| GET    | `/versions/{version_id}/artifacts` | 产物列表 |
| GET    | `/artifacts/{id}/presign` | 取临时下载链接 |
| POST   | `/uploads/sts` | 颁发 OSS 上传临时凭证 |
| GET    | `/tasks/{task_id}/cost` | 任务累计成本（按版本展开 + 合计） |
| GET    | `/versions/{version_id}/cost` | 单版本成本（聚合 + 可选明细） |
| GET    | `/me/cost?from=&to=&group_by=day\|task_type\|model` | 用户维度聚合成本 |
| GET    | `/pricing` | 当前生效价格表（用于前端预估提示） |

示例：

```http
POST /api/v1/tasks
Authorization: Bearer <jwt>
Content-Type: application/json

{
  "title": "实现一个 React 桌面端音乐 App",
  "task_type": "code-gen",
  "prompt": "基于 React 实现一个桌面端音乐 App，至少包含...",
  "params": {
    "framework": "react",
    "target": "desktop",
    "design_style": "minimal"
  },
  "lane": "default"
}

→ 201
{
  "code": 0,
  "data": {
    "task_id": "...",
    "version_id": "...",
    "version_no": 1,
    "status": "pending"
  }
}
```

迭代：
```http
POST /api/v1/tasks/{task_id}/iterate
{
  "base_version_id": "<v1>",        // 可省，默认 current_version
  "prompt": "在当前版本基础上，增加歌手详情页…",
  "params": {}
}
→ 201  返回新的 version_id
→ 409  当前 task 已有活跃版本：
       {
         "code": "active_version_exists",
         "message": "task has an active version, please wait or cancel it first",
         "data": { "active_version_id": "...", "active_version_status": "running" }
       }
```

控制信号：
```http
POST /api/v1/tasks/{task_id}/control
{ "action": "pause" | "resume" | "cancel", "reason": "..." }
```

成本查询：
```http
GET /api/v1/tasks/{task_id}/cost
→ 200
{
  "task_id": "...",
  "total": {
    "amount_usd": 1.7234,
    "input_tokens": 142000, "output_tokens": 38000, "cached_tokens": 6000,
    "tool_calls": 17, "wall_time_ms": 423000
  },
  "by_version": [
    {"version_id": "...", "version_no": 1, "amount_usd": 1.10, "wall_time_ms": 280000, ...},
    {"version_id": "...", "version_no": 2, "amount_usd": 0.62, "wall_time_ms": 143000, ...}
  ]
}
```

### 5.2 WebSocket / 实时通道

- 连接：`wss://.../api/v1/ws?token=<jwt>`
- 客户端发：
  ```json
  { "op": "subscribe", "topics": ["task:<id>", "version:<id>"] }
  { "op": "unsubscribe", "topics": [...] }
  { "op": "ping" }
  ```
- 服务端推：
  ```json
  {
    "topic": "task:<id>",
    "kind": "status" | "log" | "step" | "artifact" | "error",
    "seq": 142,
    "ts": "2026-05-17T12:34:56Z",
    "payload": { ... }
  }
  ```
- 断线重连：客户端记录每个 topic 已收到的最大 `seq`，重连后通过 REST `/events?after_id=` 补齐缺口，再切回 WS。

### 5.3 MQ 消息契约

#### 任务执行（API → Worker）
Routing key: `execute.<task_type>.<lane>`
```json
{
  "msg_id": "uuid",
  "idempotency_key": "<run_id>",
  "task_id": "...",
  "version_id": "...",
  "run_id": "...",
  "attempt_no": 1,
  "task_type": "code-gen",
  "prompt": "...",
  "params": {...},
  "parent_version_id": "...|null",
  "parent_artifact_root": "oss://...|null",
  "deadline_ts": 1715900000
}
```

#### 控制信号（API → 特定 Worker）
Routing key: `control.<worker_id>` 或 fanout 到所有 Worker（订阅 task_id 维度）。
```json
{ "task_id": "...", "run_id": "...", "action": "pause|resume|cancel", "ts": "..." }
```
> 控制信号也通过 Redis Pub/Sub 走 fast-path，Worker 同时监听两路，取先到者，保证延迟与可靠并存。

#### 事件上报（Worker → Realtime Gateway / DB）
Routing key: `event.<task_type>.<kind>`
```json
{
  "task_id": "...", "version_id": "...", "run_id": "...",
  "seq": 17, "kind": "step",
  "payload": { "step_name": "plan", "progress": 0.2, "summary": "..." },
  "ts": "..."
}
```
Realtime Gateway 持久化到 `task_events`（幂等键 = run_id+seq）并推送 WS。

#### 成本事件（Worker → Cost Service）
独立 Exchange `cost.exchange` (topic) → 队列 `q.cost.events`，由 Cost Service 消费。
Routing key: `cost.<kind>` （llm / tool / compute）
```json
{
  "task_id": "...", "version_id": "...", "run_id": "...",
  "seq": 42, "kind": "llm",
  "resource_name": "claude-opus-4-7",
  "input_tokens": 1200, "output_tokens": 480, "cached_tokens": 0,
  "duration_ms": 4300,
  "occurred_at": "2026-05-17T08:32:11Z"
}
```
Cost Service 收到后：① 查 pricing 表得到当前生效单价；② 计算 amount_usd；③ INSERT cost_events（幂等键 run_id+seq）；④ UPSERT task_costs；⑤ 转发一条精简事件到 `task.events`，前端实时看到成本累计变化。

### 5.4 Worker 插件接口

详见 §8。

---

## 6. 核心交互流程

### 6.1 任务创建与首次执行

```
User ─► Web ─► API: POST /tasks
                ├─[Tx]── INSERT tasks
                │        INSERT task_versions(v1)
                │        INSERT task_runs(attempt=1, status=queued, idem_key)
                │        INSERT outbox(topic=execute, payload=...)
                │        UPDATE tasks.current_version=v1
                ◄ 201
              Outbox Relayer
                └─► RabbitMQ.publish(execute.code-gen.default)
                                            │
                                            ▼
                                         Worker.consume
                                            │
                                            ├─ ack 之前：
                                            │   UPDATE task_runs SET status=running, started_at, worker_run_id
                                            │   PUBLISH event.status(running)
                                            │
                                            ├─ deep agent 执行
                                            │   ├─ 每次 LLM/tool 调用 → cost_meter → PUBLISH cost.<kind>
                                            │   │   (Cost Service 异步消费，结算并广播 task.events.cost 增量)
                                            │   ├─ step1 → checkpoint → event.step
                                            │   ├─ step2 → checkpoint → event.step
                                            │   └─ ...
                                            │
                                            ├─ 上传产物到 OSS，INSERT artifacts
                                            ├─ UPDATE task_runs/versions/tasks → succeeded
                                            │   （version 从 is_active=true → false，
                                            │    自动释放 one_active_version_per_task 索引位）
                                            ├─ PUBLISH event.status(succeeded)
                                            └─ MQ ack
```

关键不变量：
1. DB 事务里同时写业务表与 outbox，保证"任务创建成功"≡"MQ 必然能收到该任务"。
2. Worker 消费时 **先持久化 status=running 再 ack**，避免重复消费时丢失状态切换的可观察性；幂等性由 `idempotency_key` 保证。

### 6.2 实时状态推送

```
Worker ─► RMQ.event.* ─► Realtime Gateway 消费
                            ├── 写入 task_events (run_id, seq 幂等)
                            └── 按订阅 fanout 给 WS clients
```

- Gateway 间通过 Redis Pub/Sub 协调，避免某个 Gateway 拥有连接但事件被另一个实例收到 —— 简化设计：所有 Gateway 都消费同一 fanout exchange，自己判断"是否有匹配连接"再发，避免跨实例转发。

### 6.3 暂停 / 恢复 / 取消

```
User → API: POST /tasks/{id}/control {action: cancel}
   ├─[Tx]── INSERT outbox(topic=control, payload={action:cancel,...})
   │        （tasks.status 不立刻改，等 Worker 确认；可置 cancel_requested 标记）
   ◄ 202
Outbox Relayer → RMQ.task.control / Redis pub
Worker (轮询 control queue + Redis):
   ├── 设置 in-memory cancel flag
   ├── 在下一个安全检查点：
   │     - 写 checkpoint
   │     - UPDATE task_runs → cancelled
   │     - PUBLISH event.status(cancelled)
   │     - ack execute message (不重投)
```

- **pause/resume**：pause 时 Worker 写完 checkpoint 后释放执行权（ack 当前 execute message，并发出一个 `enqueue_after_resume` 标记到 DB）；resume 时 API 重新 publish execute message，Worker 从 checkpoint 恢复。
- **cancel**：永久终止；不再重试。
- **强制取消**：若 Worker 在 N 秒内未响应，控制平面将该 run 标记 `cancelled_force`，由 Reaper 接管（见 §7.4）。

### 6.4 迭代（基于现有版本）—— 含任务级互斥

```
User → API: POST /tasks/{id}/iterate
   ├─[Tx, SERIALIZABLE or SELECT ... FOR UPDATE on tasks row]
   │   1) SELECT * FROM tasks WHERE id=? FOR UPDATE
   │   2) 检查是否存在活跃 version：
   │       EXISTS(SELECT 1 FROM task_versions
   │              WHERE task_id=? AND is_active)
   │      → 若存在：ROLLBACK，返回 409 active_version_exists
   │   3) INSERT task_versions(v_new, parent_id=v_cur, status='pending')
   │      （DB 上的 UNIQUE INDEX one_active_version_per_task 兜底，
   │        如有并发漏网者会触发 23505，转译为 409）
   │   4) INSERT task_runs(version_id=v_new, attempt=1)
   │   5) INSERT outbox(execute, parent_artifact_root=v_cur.artifact_root)
   │   6) UPDATE tasks.current_version=v_new
   ◄ 201 / 409
```

互斥规则总结：
- **真相之源 = DB 唯一索引** `one_active_version_per_task`。无论调用方是 API、CLI 还是内部脚本，都不会出现并行活跃版本。
- **应用层先行检查**用于给出更友好的错误信息（提示用户当前活跃版本及其状态）。
- **解锁触发点**：当一个 version 从 active 集合迁出（终态 succeeded/failed/cancelled）时，索引自动释放，新的迭代请求即可成功。
- **前端配合**：在 `task.status` 活跃时禁用迭代/回滚-branch 按钮，并通过 WS 监听状态变化主动启用。

Worker 收到 execute 时拿到 `parent_artifact_root`，"基于父版本产物增量改造"：
- 代码类任务：先把 parent artifact 拷贝/挂载为工作目录（OSS 同 bucket 内 server-side copy，避免下载上传）。
- 调研类任务：拿父版本的报告作为上下文输入给 agent。

### 6.5 回滚

逻辑等价于"以历史版本为父，创建一个空 prompt 的新版本"。也可以提供 `--no-execute` 模式：只切 `current_version` 指针不重新执行（适用于产物已经存在的情况）。两者由 API 参数选择：

```http
POST /tasks/{id}/rollback
{ "target_version_id": "<v_k>", "mode": "switch" | "branch" }
```

- `switch`：仅更新 `current_version` 指针，不创建新版本、不执行；不受任务级互斥约束（不产生活跃版本）。
- `branch`：基于 v_k 创建新版本并执行（用于"以 v_k 为起点继续走"）；与 iterate 一样需要先通过互斥检查，活跃中返回 409。

### 6.6 失败与重试

```
Worker step 失败
  ├─ 可恢复异常 → 本地重试（小次数）
  ├─ 不可恢复（输入非法等） → UPDATE run.status=failed, PUBLISH event.error, ack
  └─ 进程崩溃/超时 → 不 ack → MQ 自动重投到下一个 Worker
      ↓
   下一个 Worker 接收：
      - 幂等键已存在的 event/checkpoint 跳过
      - 从最新 checkpoint 恢复，attempt_no+1
      - 超过 max_attempts → 进入 DLQ
```

DLQ 处理：
- 自动 reconciler 周期扫描 DLQ，归类（按 error.code）。
- 人工介入通过管理后台重新入队或标记失败。

### 6.7 完整示例：用户任务一（React 音乐 App + 迭代）

```
T0: 用户提交 v1 (prompt: 实现 React 桌面音乐 App ...)
    → Planner subagent 拆分：项目脚手架 / 路由 / 组件 / 样式 / 打包
    → Executor subagents 逐步生成代码，写入 OSS:
       artifacts/{tenant}/{task}/{v1}/code/
    → 每步 event.step (前端实时显示)
    → 每次 LLM 调用 → cost.llm；前端成本面板秒级累加
    → 最终产物：code-bundle.zip + 预览说明 README.md
    → status=succeeded；task_costs[v1].amount_usd 结算入库

T0.5: 期间用户尝试再次提交迭代 → API 返回 409 active_version_exists
      前端按钮置灰，提示"v1 正在运行 (running)"

T1: v1 终态后，用户基于 v1 提交 v2 (prompt: 增加歌手详情页 ...)
    → API: iterate 通过互斥检查，v2.parent=v1
    → Worker: OSS 中 server-side copy v1 → v2 工作目录
    → 仅对增量内容生成/修改文件
    → 输出 diff + 新 zip；继续累加 cost
    → status=succeeded；task 累计 cost = sum(v1.cost, v2.cost)
```

### 6.8 完整示例：用户任务二（调研报告）

```
v1 (prompt: 调研 2026 各厂商 coding agent ...)
    → research_agent 装配：web_search tool, fetch tool, summarizer subagent
    → step1: 检索关键词列表
    → step2: 抓取并存档 (raw/*.html)
    → step3: 抽取要点 → ndjson
    → step4: 结构化对比表
    → step5: 生成 report.md，上传 OSS
    → artifacts: report.md, sources.json
```

---

## 7. 扩展性与容错

### 7.1 水平扩展
- **API / Realtime Gateway / Worker**：无状态，K8s HPA 基于 CPU + 自定义指标（队列积压、WS 连接数）。
- **RabbitMQ**：3/5 节点 quorum，按租户/lane 拆队列；超大流量场景对 lane 维度做联邦或迁 Kafka。
- **PostgreSQL**：读多写少业务用读副本；超过单库后按 `tenant_id` 分片（Citus 或应用层 sharding）。
- **OSS**：天然水平扩展，按租户前缀路由到多 bucket（避免单 bucket 元数据热区）。

### 7.2 一致性保证
- **Outbox + Idempotent Consumer**：DB 事务为"真相之源"，MQ 投递最终一致；消费端按 `idempotency_key` 去重。
- **事件单调序**：`task_events.seq` 在 run 内单调，前端可用此判断顺序与去重。
- **乐观锁**：`tasks/versions/runs` 表带 `updated_at` 或 `version` 字段，所有状态翻转使用 CAS 防止并发覆盖。
- **状态机校验**：所有 transition 在 Domain Service 层校验合法性，非法翻转返回 409。

### 7.3 故障恢复矩阵

| 故障 | 影响 | 恢复机制 |
|---|---|---|
| API 实例崩溃 | 该实例上 in-flight HTTP 失败 | LB 摘除；客户端重试；事务未提交即回滚 |
| Realtime Gateway 崩溃 | 该实例上 WS 断开 | 客户端重连任意实例 + REST 补齐缺口事件 |
| Outbox Relayer 崩溃 | 事件延迟 | Leader 选举重新接管，从 pending 继续 |
| Worker 进程崩溃 | 当前 step 中断 | MQ 不 ack → 重投 → 新 Worker 从 checkpoint 恢复 |
| Worker 整机宕机 | 同上 | MQ consumer cancel；其他节点接管 |
| MQ 单节点宕机 | quorum 容忍 N/2 个节点 | Raft 选主继续；超阈值则只读模式 |
| PG 主库宕机 | 写不可用 | Patroni/RDS HA 切主；应用重连 |
| OSS 抖动 | 上传/下载失败 | SDK 自动重试 + 任务级重试 + 多区域 fallback |
| 网络分区 | 部分组件不可见 | 各组件本地降级；恢复后 outbox/MQ 自动追赶 |
| Bug 导致任务死循环 | 资源浪费 | 任务级超时 (deadline_ts) + Reaper 强制 cancel |

### 7.4 Reaper（清道夫）
独立的小型 Go 服务，周期扫描：
- `task_runs.status=running AND last_heartbeat < now - 2*heartbeat_interval` → 标记 `lost`，重新入队或标记失败。
- `task_runs.status=cancelling AND now - cancel_requested_at > N` → 强制 cancel。
- `outbox.status=pending AND attempts > X` → 告警。

### 7.5 限流与背压
- API 层：APISIX 全局 QPS + per-tenant 限流。
- 任务级：每租户并发任务数上限；超过则任务进入 `pending_throttled` 等待。
- Worker 池：基于 lane 隔离大任务，避免长任务把队列塞满；动态 prefetch 根据队列深度调整。

---

## 8. Worker 插件机制

### 8.1 三类扩展点

| 类型 | 用途 | 类比 |
|------|------|------|
| **Tool** | 暴露给 LLM 调用的函数（web_search, run_code, oss_read…） | deepagents/LangChain Tool |
| **Subagent** | 拥有独立 prompt + 工具集的子代理（planner, critic, researcher） | deepagents subagent |
| **Skill** | 一组提示词 + 工具组合的"技能包"，可在运行时按需加载 | 类似 Claude Code skill |

### 8.2 目录与发现

```
worker/plugins/
├── tools/
│   ├── web_search/
│   │   ├── plugin.yaml
│   │   └── handler.py
│   └── oss_fs/
├── subagents/
│   └── critic/
│       ├── plugin.yaml
│       └── prompt.md
└── skills/
    └── react-project/
        ├── skill.yaml
        ├── instructions.md
        └── tools/  # 局部 tools
```

`plugin.yaml` 示例：
```yaml
kind: tool                    # tool | subagent | skill
name: web_search
version: 1.2.0
entrypoint: handler:search    # python "module:callable"
schema:
  input:
    type: object
    properties:
      query: { type: string }
      top_k: { type: integer, default: 5 }
    required: [query]
  output: { type: object }
permissions:
  - network.egress
  - oss.write:tmp/
applies_to:
  task_types: [research, code-gen]
resources:
  timeout_s: 30
  memory_mb: 512
```

### 8.3 Python 接口（约定）

```python
from worker.plugins import Tool, Subagent, Skill, register

@register
class WebSearchTool(Tool):
    name = "web_search"
    schema = {...}  # JSON Schema

    async def call(self, ctx: ToolContext, query: str, top_k: int = 5) -> dict:
        # ctx 提供：task_id, run_id, oss client, logger, cancel_token
        ...
```

```python
@register
class CriticSubagent(Subagent):
    name = "critic"
    prompt = "..."
    tools = ["read_file", "diff"]
    model = "claude-sonnet-4-6"
```

`Skill` 通过声明式 yaml + instructions.md 注册；Worker 启动时扫描 `plugins/` 目录构建注册表，并按 `applies_to.task_types` 路由。

### 8.4 安全与沙箱
- 每个 tool 调用受 `permissions` 白名单约束（网络/文件/进程）。
- 危险操作（代码执行、shell）默认跑在隔离沙箱：
  - 一级：子进程 + seccomp + cgroup limit。
  - 二级（敏感任务）：Firecracker microVM / gVisor。
- OSS 访问只允许任务专属前缀（运行时为 ctx 注入受限 STS）。

### 8.5 版本与灰度
- 插件按 SemVer 发布，记录在 `worker_registry.capabilities`。
- 同一插件多版本并存，任务可指定版本 (`params.plugins.web_search=1.2.0`)；未指定则取 latest stable。
- 灰度：通过 lane 路由（`lane=canary` 的任务进入装载 canary 插件的 Worker 组）。

### 8.6 任务类型与 Agent 装配示例

```yaml
# agents/code_agent.yaml
task_type: code-gen
base_agent:
  model: claude-opus-4-7
  system: prompts/code_system.md
subagents: [planner, executor-frontend, critic]
tools: [oss_fs, run_node, run_tests, web_search]
skills: [react-project, electron-shell]
limits:
  total_timeout_min: 60
  max_tokens_per_step: 200000
```

---

## 9. 安全 / 多租户 / 合规

- **认证**：JWT (短期) + Refresh Token；可对接 SSO/OIDC。
- **授权**：RBAC + 资源所有权检查（所有 Repo 查询强制 `tenant_id` 过滤）。
- **多租户隔离**：DB row-level 过滤；OSS 前缀隔离；MQ routing key 含租户维度时不与他租户队列共享 lane。
- **传输加密**：全链路 TLS。
- **静态加密**：DB TDE；OSS SSE-KMS。
- **审计日志**：所有控制 API（创建/取消/回滚）+ 插件高危调用全部进审计表，独立保留策略。
- **PII 处理**：用户 prompt 与产物可能含敏感数据；OSS 桶按租户加密密钥隔离。
- **依赖供应链**：Worker 插件来源签名校验（cosign），允许私有插件仓库白名单。

---

## 10. 可观测性

| 维度 | 工具 | 指标举例 |
|---|---|---|
| Metrics | Prometheus | `task_runs_total{status}`, `mq_consume_latency`, `outbox_pending`, `ws_connections`, `worker_concurrency` |
| Logs | Loki/ELK | 结构化 JSON，带 `trace_id`, `task_id`, `run_id`, `step` |
| Tracing | OpenTelemetry | API → MQ → Worker → 插件调用 全链路 span |
| Alert | Alertmanager | 队列积压、Worker 心跳异常、DLQ 增长、Outbox 堆积、PG 复制延迟 |
| Dashboard | Grafana | 任务漏斗、平均执行时长、错误率、租户 Top N |

任务级"故事化"视图：以 `task_id` 串联 events + spans + logs + artifacts，提供调试入口。

---

## 11. 部署拓扑（建议）

```
K8s 集群（多 AZ）
├── ns: edge          (APISIX 网关, 多副本 + HPA)
├── ns: app           (api, realtime, outbox-relayer, reaper, deployment + HPA + PDB)
├── ns: worker        (worker-pool-default / worker-pool-code / worker-pool-research, Deployment + KEDA based on RMQ depth)
├── ns: data          (postgres-operator, redis-operator, rabbitmq-cluster-operator)
└── ns: obs           (Prom, Loki, Tempo, Grafana, Alertmanager)

外部托管
├── OSS               (多区域)
└── KMS               (密钥管理)

CI/CD
├── GitHub Actions: build, test, scan, sign
├── ArgoCD          : GitOps 滚动
└── Plugin Registry : 私有 OCI / S3, 启动时同步
```

资源建议（参考起点）：
- API: 4c8g × 3
- Realtime: 4c8g × 3
- Worker: 8c16g × N（根据任务类型，code-gen 可能更大）
- PG: 主 16c64g + SSD；2 只读
- RabbitMQ: 8c16g × 3
- Redis: 4c8g × 3

---

## 12. 演进路径

### 12.1 MVP 范围（首版交付）

**必须有（must）**
- 单租户（或固定多用户、无配额隔离），账号体系最简 JWT。
- 前端：TaskCreate / TaskList / TaskDetail（实时日志 + 控制 + 成本面板）/ VersionTree（含每版本成本）/ CostDashboard（按任务/按日聚合）。
- 后端 API：§5.1 表中所有路径；含成本查询；含 409 互斥保护。
- Worker：deepagents 基础 agent，code-gen 与 research 两个 task_type；Tool + Subagent 两类插件；子进程沙箱即可。
- 持久化：PostgreSQL 单实例 + 每日备份；OSS 单 bucket（按前缀分类）；RabbitMQ 单节点托管或 3 节点 quorum 起步。
- 关键模式：Outbox、幂等消费、Checkpoint、Reaper（最简版：仅心跳超时 → 标记 lost + 重投）。
- 成本：cost_events 明细 + task_costs 聚合 + pricing 表 + 前端展示，**先 LLM token 维度，compute 折算先粗（按 wall_time × 固定单价）**。
- 任务级互斥：DB 唯一索引 + API 409 + 前端禁用按钮。
- 可观测：Prometheus + Grafana + 结构化日志；OpenTelemetry trace 接入 API 与 Worker，但仪表盘只做最关键 5 个面板。

**显式不做（won't, 留到后续）**
- Electron 桌面壳、SSE/多级降级、版本 DAG merge、租户级配额计费。
- Skill 插件、microVM 沙箱、Plugin 灰度发布、Plugin 市场。
- PG 分库分表、Citus、Kafka、多 Region。
- 强制 cancel 之外的 Reaper 高级策略；DLQ 自动 reconciler 仅做告警，不自动重入。
- 多人协作 / 并行迭代（与任务级互斥规则一致，单 task 单用户单活跃）。

### 12.2 后续演进

| 阶段 | 增量范围 |
|------|----------|
| **v1（生产硬化）** | 多租户隔离与配额、Skill 插件、Worker 沙箱升级、Outbox HA、Reaper 完整策略、Plugin 灰度、强制 cancel、成本预算告警 |
| **v2（规模化）** | PG 分片、RMQ 多 lane / 联邦或迁 Kafka、多 Region、按 capability 调度 Worker、租户级 SLA |
| **v3（平台化）** | 插件市场、BYO compute、Webhook 出站、自助计费 / 审计 / 合规 |

---

## 13. 开放问题与后续待定

1. **成本预算与告警**：MVP 仅做事后展示；是否要在 v1 引入"任务预算上限 → 超出自动 pause/cancel"以及"用户日额度"。
2. **Compute 成本口径**：MVP 拟用 `wall_time × 固定单价` 折算 Worker compute。是否在 v1 改为采集真实 CPU/GPU 秒数（cgroup / NVML）。
3. **Cached tokens 折算规则**：Anthropic / OpenAI 的缓存命中折扣规则需在 pricing 表中显式建模（命中/未命中两条目）。MVP 先按"命中按 10% 计价"占位，待对接实际计费单据后校准。
4. **产物大小上限**：单版本最大允许产物（e.g. 1 GB）；超出走异步打包 / 流式下载。
5. **互斥粒度**：当前为 task 维度；未来若引入"团队共享 task"，是否改为 task × user 维度或加入显式 lock 接口。
6. **Worker 资源调度**：是否在 v2 引入基于 task 预计资源画像的调度器（而非纯 lane）。

---

## 附录 A：术语表

- **Task**：用户的一次意图，包含多个版本；MVP 下同一时刻最多一个活跃版本。
- **Version**：一次具体的"输入快照 + 产物快照"，MVP 下构成严格树（单父）；Post-MVP 可演进为带 merge 的 DAG。
- **Run**：一个版本的一次执行实例，失败重试会产生多个 Run。
- **Active Version**：状态属于 {pending, queued, running, paused, cancelling} 的版本；由 DB 唯一索引保证 task 内至多一个。
- **Cost Event**：Worker 每次 LLM/tool 调用或计时打点产生的一条成本明细，由 Cost Service 按 pricing 结算后入库。
- **Lane**：RMQ 队列分流维度，用于隔离大/小任务、灰度等（MVP 仅一个 default lane）。
- **Outbox**：与业务事务一起写入的"待发送事件表"，由 Relayer 投递。
- **Checkpoint**：Worker 在 step 边界写下的可恢复状态。
- **Plugin**：Worker 端可装载的 Tool/Subagent/Skill（MVP 仅 Tool + Subagent）。
