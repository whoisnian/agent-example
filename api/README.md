# api

后端 API 代码根目录。

- **技术栈**：Golang（Gin / Echo + sqlc + slog）
- **职责**：任务 CRUD、状态机推进、版本管理、互斥校验、成本查询聚合、Outbox 投递、Realtime Gateway 与 Worker 编排
- **目录规划**：见 [`../docs/ARCHITECTURE.md §3.2`](../docs/ARCHITECTURE.md)

> 代码尚未实现。脚手架将通过 OpenSpec 变更 `init-api-scaffold` 引入。

## 关键不变量

- 任何"创建活跃版本"的操作（create / iterate / rollback-branch）必须在事务内做互斥检查，并依赖 DB 唯一索引 `one_active_version_per_task` 兜底。
- 所有跨服务边界的事件走 **Outbox 模式**：DB 事务写业务表 + outbox，由 Relayer 异步发布到 RabbitMQ。
- 任何状态翻转通过 Domain Service 的状态机方法完成，禁止裸 SQL UPDATE。
- 错误返回结构：`{code, message, data, trace_id}`，互斥冲突使用 `409 active_version_exists`。
