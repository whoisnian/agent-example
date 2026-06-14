# web/AGENTS.md

`web/` 前端（Vite + React 19 + TS strict + Tailwind 3 + React Query + Zustand）的代理细则。补充根 [`AGENTS.md` §4.3](../AGENTS.md)。

## 设计系统（shadcn/ui）

- **组件基座**：shadcn/ui，源码 vendored 到 `src/components/ui/`（不是 npm 包）。按需手动添加，不批量拉取。`components.json` 保留偶尔用 CLI 追加单个组件的能力（`tailwind.config` 字段为 `""`——v4 无 JS config）。
- **vendoring 形态**：**Tailwind 4**（cva + `tailwind-merge@3`，颜色为完整 `oklch()` token、用 `var(--color-*)`/`var(--token)` 引用，动画用 `tw-animate-css`）。构建走 `@tailwindcss/vite`（无 `postcss.config.js`/`autoprefixer`）。主题用 CSS-first `@theme`（见下）；**不要**再用 v3 的 `hsl(var(--token))` 包裹或 JS `tailwind.config.js`。
- **`cn()`**：`src/lib/cn.ts`（`clsx` + `tailwind-merge`）。

## 主题与颜色

- 主题走 shadcn 标准 **CSS 变量**，**CSS-first（Tailwind v4）**：`src/styles/globals.css` 用 `@import "tailwindcss"` + `@import "tw-animate-css"`，`@custom-variant dark (&:is(.dark *))` 复刻 class 暗色，`:root`/`.dark` 定义**完整 `oklch()` token**（变量本身即颜色，无 `hsl()` 包裹），`@theme inline` 暴露 `--color-*`/`--radius-*`/`--font-*`。**不覆盖** Tailwind 默认 `spacing`/`fontSize` scale。
- **MVP 默认深色**放在 `:root`（`<html>` 不挂 `.dark`）；`.dark` 预留（当前镜像 `:root`）。
- **颜色纪律（plugin-independent）**：`eslint-plugin-tailwindcss` 已退役（v4 仅有长期停滞的 pre-release），其 `no-contradicting-classname` 矛盾检测**无替代**。护栏 = eslint `no-restricted-syntax`（拦 className 里的裸 hex `bg-[#...]` 与裸色函数 `bg-[oklch(...)]`/`rgb()`/`hsl()`）+ `npm run lint:colors`（`scripts/check-colors.mjs`：退役调色板类名 + CSS 裸色字面量，**放行** `globals.css` 的 `:root`/`.dark`/`@theme` token 定义块）。允许变量回退 arbitrary（如 `ring-[var(--color-ring)]`）。用语义 token 类：`bg-background`/`bg-card`/`text-foreground`/`text-muted-foreground`/`bg-primary`/`bg-destructive`/`border-border`/`bg-muted`/`bg-accent` 等。已退役旧调色板（`bg-bg`/`bg-surface`/`text-text`/`text-text-muted`/`text-danger`…）——勿再使用。

## 三栏外壳

- `RootLayout`（`src/routes/root-layout.tsx`，**不要迁目录**）= 左导航 `SideNav` / 中 `Outlet` / 右 `PreviewColumn`。三栏子组件在 `src/components/layout/`。**例外**：`/tasks/new` 路由不渲染右栏（路由驱动抑制，含 re-open 按钮；**不要**用改写 `previewCollapsed` 的方式实现，会污染用户偏好）。
- **栏宽重心**：右栏为主导列（lg `w-2/5` / xl `w-1/2`，基准是扣除 nav 的剩余宽——RootLayout 内层 wrapper 即为此存在，勿移除；`max-w-4xl` 封顶）；中栏内容收在 `max-w-4xl` 居中适读容器；左导航固定 `w-56`（**折叠功能已退役**，勿复活 `navCollapsed`）。
- **SideNav 结构**（自上而下）：brand 行 → "New task" 主按钮（`nav-new-task`）→ `RecentTasks`（复用 `useTasksQuery({page:1,pageSize:8},{silent:true})`，**最近创建**序）→ 头像式用户区 = DropdownMenu 触发器（菜单含 Tasks / Cost / Settings / Logout，`nav-*` 与 `logout-button` testid 在菜单项上，激活路由项带选中态）。Recents 读取必须静默（不 toast），错误面是行内占位。
- 右栏折叠态（`previewCollapsed`）与选中态（`selectedVersionId` + `selectedArtifactId`）在 `features/ui/store`（Zustand）。**不变式**：单独 set `selectedVersionId` 且值变化时自动清空 `selectedArtifactId`；成对写入走 `selectArtifact(versionId, artifactId)`（同时展开右栏）。
- 任务生命周期 mutation（iterate/rollback/control）与 live task 帧需失效 `taskKeys.lists` 前缀，否则 TaskList/Recents 状态过期——新增 mutation 时记得带上。
- **TaskDetail = 对话回合流**：紧凑头部（标题/状态/控制条/完整 TokenBar）+ 滚动主体（每版本一回合 `components/tasks/ConversationTurn`：prompt 经 `useVersionQuery` 懒取静默降级 / 结果行 / 内联产物卡片（整卡点击驱动右栏预览，Download 独立）/ 非当前回合的回滚尾部）+ 底部常驻 iterate composer（活跃禁用给原因，成功清空、失败保留输入）。EventLog 以**助手消息气泡**呈现且只出现在 `task.current_version` 对应回合（status 人话化 / error destructive 配色）。版本树组件已退役，勿复活。
- **TaskCreate = 聊天式创建入口**：居中问候 + composer 卡片（prompt + task_type chips + Advanced 折叠区 params/lane，Ctrl/Cmd+Enter 提交）。**无 title 输入**——创建请求不发 `title`，由 API 从 prompt 派生（task-write-api）；TaskList 页无页内 New task 按钮，入口只在 SideNav。
- **Artifact 预览**：右栏 `features/artifacts/ArtifactPreviewPanel` 自带头部工具栏（选中产物标题 · 类型 + Copy / Refresh / 关闭；**全状态渲染**，no-version/loading/error/empty 也要有关闭按钮），按 store 选中态渲染产物列表 + 预览（图片 `<img>` / `text/html` 沙箱 iframe 富渲染（默认）+ 渲染/源码切换 / 文本截断 64KB / 其它仅下载），复用既有 `features/artifacts/` 数据访问，不新增 transport。`PreviewColumn` 是纯容器，不放头部。
- **iframe 安全红线**：`sandbox="allow-scripts"`，**绝不**加 `allow-same-origin`（frame 跑在 opaque origin；下载响应自带 `CSP: sandbox allow-scripts` 双保险）；frame 内 HTTP 失败不可探测——不要试图检测，恢复手段是工具栏 Refresh（重新 presign 重挂）。Copy 只复制帽内完整文本，截断态禁用并引导下载。
- CSP（`index.html`）：产物预览**同源**（presign 返回 API 相对路径 `/api/v1/artifacts/{id}/download?token=...`，字节经 API 反向代理，浏览器不接触 OSS）——`img-src 'self' data:`、`frame-src 'self'`、`connect-src 'self' ws: wss:`，**不放行任何 OSS 来源或 `http:`/`https:` 通配**；文本预览是同源 `fetch`，无 CORS 门槛。`script-src 'self'` / `object-src 'none'` 保持锁定。

## 测试与约定

- 保留组件 `data-testid` 稳定，契约测试（vitest + Testing Library + MSW）充当回归网。改交互语义时同步改对应测试。
- 原生 `<select>`（状态/分组筛选）刻意保留，便于测试 `selectOptions`/change；不要换成 shadcn `Select`（portal 非原生）除非同步改测试。
- 提交前：`npm run typecheck && npm run lint && npm run test && npm run build` 全绿。
