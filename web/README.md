# web

前端代码根目录。

- **技术栈**：React 18 + TypeScript + Vite + TailwindCSS + Zustand + React Query
- **包管理**：pnpm
- **目录规划**：见 [`../docs/ARCHITECTURE.md §3.1`](../docs/ARCHITECTURE.md)

> 代码尚未实现。脚手架将通过 OpenSpec 变更 `init-web-scaffold` 引入。

## 开发约定

- 服务端状态走 React Query，本地 UI 状态走 Zustand，不要混用。
- 实时通道封装在 `src/features/realtime/`，组件不直接持有 WebSocket。
- 任务级互斥在 UI 层反映：`task.status` 活跃时禁用迭代/回滚-branch 按钮。
- 成本相关展示统一用 `src/features/costs/` 提供的 hooks。
