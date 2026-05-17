# worker

Worker 代码根目录。

- **技术栈**：Python + LangChain `deepagents` + `aio-pika`
- **职责**：从 RabbitMQ 消费任务 → 调度 deep agent 执行 → 写 checkpoint / cost event / artifact → 上报状态
- **目录规划**：见 [`../docs/ARCHITECTURE.md §3.3`](../docs/ARCHITECTURE.md)

> 代码尚未实现。脚手架将通过 OpenSpec 变更 `init-worker-scaffold` 引入。

## 关键不变量

- 所有 LLM / tool 调用必须经过 `core/cost_meter.py` 包装，确保 cost 事件被发出（缺失会导致计费失真）。
- 每个 step 结束必须写入 `task_checkpoints`，以支持崩溃后从最近 checkpoint 恢复。
- Worker **不直接修改** `tasks` / `task_versions` 业务主表；只能写：`cost_events` / `task_runs.last_heartbeat` / `task_checkpoints` / `artifacts`。其它状态翻转通过 `task.events` 让 API / Cost Service 处理。
- 插件通过 `worker/plugins/<kind>/<name>/plugin.yaml` 声明，启动期由 plugin loader 自动注册；新增插件不需要改核心代码。

## 插件类型（MVP）

- `tool/`：暴露给 LLM 调用的函数（web_search、oss_fs、run_node 等）
- `subagent/`：独立 prompt + 工具集的子代理（planner、critic 等）
- `skill/`：暂不实现（Post-MVP）
