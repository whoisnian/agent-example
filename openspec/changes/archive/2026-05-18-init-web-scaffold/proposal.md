## Why

`web/` 当前是空目录。每一个 MVP 页面（TaskCreate / TaskList / TaskDetail / VersionTree / CostDashboard）都依赖统一的前端基础：构建工具链、路由、HTTP 客户端（含统一响应封装）、React Query 配置、Zustand 模式、WebSocket 客户端（含订阅与断线补齐策略）、应用外壳、错误边界与全局通知。本提案一次性建立这套地基，使后续业务提案能聚焦"页面与组件"而非每次重新铺基础设施。

本提案 **不交付任何业务页面**；只交付一个 `npm run dev` 能起来、能渲染应用外壳、能通过 mocked endpoint 验证 HTTP 与 WS 客户端契约的最小骨架。

## What Changes

- 新建 `web/` 项目（Vite 8 + React 19 + TypeScript 5.9 strict 模式），含目录结构 `src/{pages,features,services,components,routes,hooks,types}/`
- 选定包管理：**npm 11**（Node 24+ 自带，无需 nvm/pnpm；`packageManager` 字段固化版本）
- 选定核心库：
  - 路由：`react-router-dom` v7（`react-router` 的 dom-shim 仍保留）
  - 服务端状态：`@tanstack/react-query` v5
  - 本地状态：`zustand` v5
  - 样式：`tailwindcss` v3.4（v4 等待 `eslint-plugin-tailwindcss` 兼容后再升）
  - 表单（预留，不实现）：`react-hook-form` + `zod`
  - 测试：`vitest` v4 + `@testing-library/react` v16 + `msw` v2
  - Lint / format：`eslint` v9 (flat config) + `typescript-eslint` v8 + `prettier`
- 实现以下骨架能力：
  - 应用外壳：顶栏（logo + 用户区占位）、侧栏导航（占位条目：Tasks / Cost / Settings）、主内容区
  - 路由表：`/`（重定向到 `/tasks`）、`/tasks`、`/tasks/:id`、`/cost`、`/settings`，**每个路由仅渲染一个 placeholder 组件**
  - HTTP 客户端：基于 `fetch` 的薄封装，自动解构 `{code, message, data, trace_id}` envelope；非 0 `code` 抛 typed error；自动注入 `Authorization` 与 `X-Request-Id` header；统一超时与重试策略
  - React Query 全局配置（默认 `staleTime`、错误处理 → 全局 toast）
  - Zustand 模式：`features/auth/store.ts`（仅 token 持久化到 `localStorage`）、`features/ui/store.ts`（modal / toast 队列）
  - WebSocket 客户端：自动重连（指数退避）、`subscribe(topic, handler)` API、断线后通过传入的 REST `fetcher` 拉缺口事件（基于 `seq`）、心跳 + idle 关闭
  - 全局错误边界 + 401 拦截（跳转到登录占位页）
  - 主题与样式：Tailwind 配置 + 基础设计 token（color / spacing / typography）
- `web/Makefile`（或 `package.json` scripts）：`dev`、`build`、`preview`、`lint`、`typecheck`、`test`、`test:watch`
- 引入 `web/index.html`、`vite.config.ts`、`tsconfig.json`（strict）、`tailwind.config.js`、`eslint.config.js`（flat）、`.prettierrc`
- 提供 `msw` mock：`/healthz`、`/readyz`、以及一个伪造业务端点 `/api/v1/__scaffold/echo` —— 用来验证 envelope unwrap、错误映射、WS 订阅链路
- CI workflow `.github/workflows/web-ci.yml`：`npm ci` → `npm run lint` → `npm run typecheck` → `npm test` → `npm run build`

非目标：
- 任何业务页面（TaskCreate / TaskList / TaskDetail / VersionTree / CostDashboard）
- 真实的认证流程（登录、刷新 token、SSO）—— 仅做 token 占位
- 与真实后端的 e2e 测试 —— MVP scaffold 用 `msw` 在 vitest 中模拟
- SSR / 服务端渲染 / Next.js —— 明确选择纯 SPA
- 国际化（i18n）—— 占位，不实现
- 主题切换、深色模式 —— 占位，不实现

## Capabilities

### New Capabilities

- `web-bootstrap`: 前端工程骨架 —— Vite + React + TS strict 构建、应用外壳、路由表、错误边界、全局通知、设计 token
- `web-data-access`: 数据访问层 —— HTTP envelope 客户端、React Query 配置、Zustand 模式、类型化错误
- `web-realtime-client`: WebSocket 客户端 —— 自动重连、topic 订阅、`seq`-去重、断线补齐（REST `events?after_id=` fallback）、心跳

### Modified Capabilities

（无 —— `openspec/specs/` 仍为空）

## Impact

- **代码**：`web/` 从空目录变为可运行骨架；新增约 1.5k–2.2k 行（含生成代码、配置、测试）
- **依赖**：见 What Changes 中的库清单
- **本地开发**：需要 Node 24+ 与 npm 11+；`engines` + `packageManager` 字段做版本固化（不再使用 `.nvmrc`）
- **CI**：新增 GitHub Actions workflow（lint + typecheck + test + build）；预计 < 3 min
- **文档**：`web/README.md` 升级为含本地启动、scripts 列表、模块协作约定；`docs/ARCHITECTURE.md` 无需改动
- **跨服务契约**：
  - HTTP envelope 解析与 `init-api-scaffold` 中 `api-bootstrap` 的 "Unified Response Envelope" 必须一致；本 spec 显式重述以避免漂移
  - WS 客户端的订阅协议（`subscribe` / `unsubscribe` / `ping` op，server 推送 `{topic, kind, seq, ts, payload}`）需要在 ARCHITECTURE §5.2 中已有的契约下实现；本 spec 引用该协议
- **下游解锁**：所有 `add-*-page` 业务提案均依赖本骨架；与 `init-api-scaffold` 和 `init-worker-scaffold` 互不依赖，可并行
