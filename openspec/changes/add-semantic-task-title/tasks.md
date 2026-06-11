## 1. Worker：消息契约（先行，兼容旧消息）

- [ ] 1.1 `TaskExecuteMessage` 增加可选字段 `gen_title: bool = False`，确认解析容忍未知额外字段（不触发 poison → DLX）
- [ ] 1.2 单测：缺省 `gen_title` 解析为 `False`；带未知额外字段的消息正常解析处理

## 2. Worker：TitleGenerator

- [ ] 2.1 实现输出净化纯函数（首个非空行 → 去包裹引号 → 折叠空白 → ≤64 rune 且 ≤200 字节 rune 边界截断加 `…`，空串返回空）并补单测（ASCII/CJK/emoji/多行/引号包裹/全空白）
- [ ] 2.2 实现 `TitleGenerator`：经 `ModelFactory.get(WORKER_TITLE_MODEL_KEY，未设置回退该 task_type 注册 agent 的 model key)` 取模型，挂 `ctx.cost_meter` 回调，输入截 prompt 前 2000 字符，整体 `asyncio.wait_for` 10s 超时
- [ ] 2.3 消费者接线：`gen_title=true` 且通过全部前置守卫（fresh-run：`ctx.checkpoint_store.latest()` 为 `None`；`ctx.cancel_token` 未置位；`AgentRegistry` 可解析该 `task_type`）时，在 `status=running` 事件后、agent dispatch 前调用；经 `EventPublisher` 发 `kind="title"` 事件（payload `{title}`，正常 run seq）
- [ ] 2.4 失败路径：异常/超时/发布失败/净化为空 → WARN 日志 + `worker_title_generation_failures_total`，不发事件、agent 照常 dispatch；cancel/非 fresh/未注册 task_type 的跳过计 skip 不计 failure
- [ ] 2.5 单测（FakeModelFactory）：成功发 title 事件且 cost_meter 收到回调；`gen_title=false` 零调用；带 checkpoint 的重投/接管不调 LLM 不发事件；cancel 已置位跳过；未注册 task_type 不调用；LLM 抛错/超时不阻断 run；净化为空跳过发事件

## 3. API：创建任务标记 gen_title

- [ ] 3.1 domain service：title 走派生路径时 execute outbox payload 写 `gen_title: true`；显式 title、iterate、rollback 路径不写
- [ ] 3.2 契约/集成测试：省略 title → `outbox.payload->>'gen_title' = 'true'`；显式 title → payload 无 `gen_title: true`；iterate/rollback payload 不含该标记（回归断言，守白名单语义，非新契约）

## 4. API：ingest 消费 title 事件

- [ ] 4.1 domain `task.Service` 新增 `ApplyGeneratedTitle(taskID, title)`：trim → ≤64 rune 且 ≤200 字节截断 → 空串静默跳过；禁止裸 UPDATE
- [ ] 4.2 ingest 增加 `kind=title` 分支：与 `task_events` 落库同事务调用 4.1；`(run_id, seq)` 重复时整体 no-op；不做 terminal 守卫
- [ ] 4.3 集成测试：title 事件更新标题（含 CJK）；终态任务仍更新且状态不变；重投不重放；payload.title 缺失/空白仅落事件行；超长截断不违反列约束
- [ ] 4.4 可观测性：ingest title 分支增加 metric/log 字段（kind 维度计数 + `task_id` 结构化日志）

## 5. 文档同步

- [ ] 5.1 `docs/ARCHITECTURE.md §5.3`：execute 消息契约增加 `gen_title` 并注明 create-only flag（缺省 false，resume/retry 等重发不得重置）；§5.2 WS 推送 kind 枚举增加 `title`（顺带补 `plan`）
