## Why

任务创建已收敛为聊天式入口（`refactor-web-chat-style-polish`），与参考产品一致的预期是：打开应用直接落在"新建会话"。当前 `/` 重定向到 `/tasks` 列表，多一步才能开始创建。

## What Changes

- 根路径 `/` 的认证后默认重定向从 `/tasks` 改为 `/tasks/new`。
- 其余路由、未认证跳转 `/login` 的语义不变。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `web-bootstrap`: "Route Skeleton" requirement 中 `/` 行的重定向目标 `/tasks` → `/tasks/new`。

## Impact

- `web/src/router.tsx` 一行；`web/src/routes/router.test.tsx` 对应断言。
