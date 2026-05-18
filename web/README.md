# web

前端代码根目录。

- **技术栈**：React 19 + TypeScript 5.9 strict + Vite 8 + TailwindCSS 3 + Zustand 5 + TanStack Query 5 + React Router 7
- **包管理**：npm 11（Node 24+）—— **不使用 pnpm 或 nvm**
- **目录规划**：见 [`../docs/ARCHITECTURE.md §3.1`](../docs/ARCHITECTURE.md) 与 `openspec/changes/init-web-scaffold/design.md` D10

> MVP 骨架由 OpenSpec 变更 `init-web-scaffold` 引入。当前仅含可运行的应用外壳、路由、HTTP envelope 客户端、Zustand store、WebSocket 单例与 vitest+msw 测试 —— 不含任何业务页面。

## 本地启动

```bash
# 1. 装包（首次或锁文件变化）
npm install

# 2. 启动 dev server（HMR、http://localhost:5173）
npm run dev

# 3. 生产构建
npm run build && npm run preview
```

无后端也能跑：vitest + msw 完整覆盖；登录页接受任意非空 token。

## 常用命令

| 命令 | 作用 |
|---|---|
| `npm run dev` | Vite dev server（HMR, `:5173`） |
| `npm run build` | `tsc --noEmit` + `vite build` → `dist/` |
| `npm run preview` | 本地预览 `dist/`（`:4173`） |
| `npm run lint` | ESLint 9 flat config（含 `tailwindcss/no-arbitrary-value: error`） |
| `npm run lint:fix` | 自动修复 |
| `npm run format` / `format:check` | Prettier |
| `npm run typecheck` | `tsc --noEmit` |
| `npm test` | Vitest 单测 |
| `npm run test:watch` | Vitest watch |
| `npm run test:coverage` | 含覆盖率 |

## 环境变量

| 变量 | 默认 | 用途 |
|---|---|---|
| `VITE_API_BASE_URL` | （空）→ 同源 | REST API 基址，例如 `http://localhost:8080` |
| `VITE_WS_URL` | `ws://localhost:8080/api/v1/ws` | WebSocket 地址 |

## 模块协作约定

- **服务端状态走 TanStack Query；本地 UI 状态走 Zustand。不要混用。** 唯一例外：`auth.token`（同步可读，跨模块共享，持久化到 `localStorage`）放在 Zustand。
- **HTTP 统一通过 `src/services/http.ts` 的 `apiFetch<T>`** —— 自动解构 `{code, message, data, trace_id}` envelope，非 0 抛 typed `ApiError`，401 自动清 token + 跳 `/login`。
- **实时通道** 封装在 `src/services/ws.ts` 的单例 `realtimeClient`：多组件订阅同一 topic 自动合并；断线退避重连（base 1s, cap 30s, full jitter）+ 重发订阅；按 `seq` 去重并在出现 gap 时回调 `onGap`。后台标签页 5 分钟无订阅时自动关 socket，返回前台再重连。
- **任务级互斥**：未来业务页在 `task.status` 活跃时必须禁用迭代/回滚-branch 按钮；前端是建议性，DB 唯一索引 + API 409 才是真相之源。
- **成本展示**：未来 `src/features/costs/` 给 TaskDetail / VersionTree / CostDashboard 提供统一 hooks 复用。
- **设计 token**：颜色、间距、字号等只走 `tailwind.config.js`。**禁止任意值类（`bg-[#abc]` / `mt-[13px]`）—— ESLint `no-arbitrary-value` 设为 error。**

## React 19 / Vite 8 注意点

- `JSX.Element` 不再是全局命名空间；用 `import type { JSX } from "react"`，或返回类型省略让 TS 推断。
- `forwardRef` 大多数场景不再需要：函数组件可直接接收 `ref` prop。
- React 19 的 strict mode 在 dev 下双调用 effects；如发现重复请求请检查是否漏依赖（不是双调用的错）。
- Vite 8 dev server 默认 HMR over WebSocket；如本地代理后端，记得放行 `ws:`。

## 测试约定

- 单元测试紧贴源文件：`src/foo/bar.ts` ↔ `src/foo/bar.test.ts`。
- DOM 测试在 jsdom 环境；touching `fetch` 的测试在文件顶部加 `// @vitest-environment node`（Node 24 的 undici 拒绝 jsdom 的 `AbortSignal`）。
- 路由测试用 `MemoryRouter + Routes` 旧式 API（避免 data-router 内部 `Request` 触发同一 AbortSignal 坑），同 testid 契约。
- WebSocket 测试用注入的 fake `WebSocket` —— msw v2 的 WS handlers 不能完整模拟 replay-on-reconnect 与 idle close。

## 与 OpenSpec 的关系

本目录由变更 `init-web-scaffold` 引入。修改公共契约（envelope、WS 协议、auth 行为、互斥规则）须先经 OpenSpec 提案。
