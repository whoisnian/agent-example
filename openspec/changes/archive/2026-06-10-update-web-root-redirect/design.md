## Context

单行路由变更：`router.tsx` 的 index 路由 `<Navigate to="/tasks" replace />` 改指 `/tasks/new`。设计空间极小，单独成档仅为满足工件链。

## Goals / Non-Goals

**Goals:** 认证后访问 `/` 直达聊天式创建页。

**Non-Goals:** 不动 `/login` 守卫、其它路由、SideNav 入口。

## Decisions

- 保持 `replace` 跳转（不在历史栈留 `/`），与原行为一致。
- Tasks 列表仍由 SideNav 用户菜单 / Recents 到达，无需补入口。

## Risks / Trade-offs

- [用户习惯了落在列表页] → 参考产品（Claude.ai）同款行为；列表一次点击可达。
