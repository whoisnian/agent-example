# agent-example

一个面向 LLM Agent 任务的多用户平台示例：用户在前端提交任务 → 后端编排 → Worker（基于 LangChain `deepagents`）执行 → 持续迭代/版本化，并提供成本统计、实时观测、暂停/取消/回滚等能力。

> 本仓库目前处于 **MVP 设计阶段**，代码尚未实现，整体方案见 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)。
> 后续将通过 [OpenSpec](https://github.com/openspec/openspec) 工作流逐模块推进实现。

## 仓库结构

```
.
├── docs/                # 文档（架构、历史记录等）
│   ├── ARCHITECTURE.md  # 生产级架构设计方案（MVP 标注）
│   └── HISTORY.md       # 项目演进历史 / 关键决策
├── web/                 # 前端代码根目录（React + TypeScript + Vite）
├── api/                 # 后端 API 代码根目录（Golang）
├── worker/              # Worker 代码根目录（Python + deepagents）
├── openspec/            # OpenSpec 规格与变更提案
│   ├── changes/         # 待落地的变更提案（proposal / design / tasks）
│   └── specs/           # 已沉淀的规格
├── AGENTS.md            # 给 AI 编码代理的协作指南
├── CLAUDE.md            # Claude Code 入口，指向 AGENTS.md
└── README.md
```

## 技术栈一览

| 层 | 选型 |
|---|---|
| 前端 | React + TypeScript + Vite + TailwindCSS + Zustand + React Query |
| 后端 API | Golang（Gin/Echo + sqlc） |
| Worker | Python + LangChain `deepagents` |
| 数据库 | PostgreSQL |
| 对象存储 | OSS（S3 兼容） |
| 消息队列 | RabbitMQ |
| 缓存/PubSub | Redis |
| 可观测 | OpenTelemetry + Prometheus + Grafana |

详细模块划分、数据模型、接口契约、交互流程见 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)。

## 核心特性（MVP 范围）

- 任务全生命周期：创建 / 执行 / 暂停 / 取消 / 完成 / 迭代 / 回滚
- **任务级互斥**：同一 task 同一时刻最多一个活跃版本（DB 唯一索引兜底）
- **任务成本统计**：按 token、tool 调用、wall-time 计费，前端可见
- **故障恢复**：Outbox + 幂等消费 + Checkpoint + Reaper
- **Worker 插件机制**：Tool / Subagent 两类扩展点（Skill 留到 Post-MVP）
- 实时通道：WebSocket（失败降级为轮询）

## 开发工作流

本项目使用 [OpenSpec](https://github.com/openspec/openspec) 管理变更：

1. **提案**：`/opsx:propose <change-name>` 产出 proposal / design / tasks 三件套
2. **应用**：`/opsx:apply <change-name>` 按 tasks 落地代码
3. **归档**：`/opsx:archive <change-name>` 把变更沉淀到 `openspec/specs/`

完整的代理协作约定见 [`AGENTS.md`](AGENTS.md)。

## 快速开始

代码尚未实现，目前可阅读：

- 架构设计：[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- 项目历史：[`docs/HISTORY.md`](docs/HISTORY.md)
- 代理协作：[`AGENTS.md`](AGENTS.md)
- OpenSpec 变更：`openspec/changes/`

各子模块的本地启动方式将在对应子目录的 `README.md` 中给出。
