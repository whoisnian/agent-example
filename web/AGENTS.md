# web/AGENTS.md

`web/` 前端（Vite + React 19 + TS strict + Tailwind 3 + React Query + Zustand）的代理细则。补充根 [`AGENTS.md` §4.3](../AGENTS.md)。

## 设计系统（shadcn/ui）

- **组件基座**：shadcn/ui，源码 vendored 到 `src/components/ui/`（不是 npm 包）。按需手动添加，不批量拉取。`components.json` 保留偶尔用 CLI 追加单个组件的能力。
- **vendoring 形态**：保持 **Tailwind-3 兼容**（cva + `tailwind-merge`，颜色 `hsl(var(--token))`，动画用 `tailwindcss-animate`）；不要拷入 Tailwind 4-only 语法（`@theme` 等）。仓库**不升级 Tailwind 4**（`eslint-plugin-tailwindcss@3` 仅支持 v3）。
- **`cn()`**：`src/lib/cn.ts`（`clsx` + `tailwind-merge`）。已注册到 eslint `tailwindcss.callees`。

## 主题与颜色

- 主题走 shadcn 标准 **CSS 变量**：`src/styles/globals.css` 定义 `:root` 与 `.dark`（HSL 三元组）；`tailwind.config.js` 以 `theme.extend.colors` 映射 `hsl(var(--token))`，`darkMode:["class"]`，**不覆盖** Tailwind 默认 `spacing`/`fontSize` scale。
- **MVP 默认深色**放在 `:root`（`<html>` 不挂 `.dark`）；`.dark` 预留未启用。
- **颜色纪律**：禁裸 hex（eslint `no-restricted-syntax` 拦 `bg-[#...]`）；允许引用变量的 arbitrary 值（如 `ring-[hsl(var(--ring))]`）。用语义 token 类：`bg-background`/`bg-card`/`text-foreground`/`text-muted-foreground`/`bg-primary`/`bg-destructive`/`border-border`/`bg-muted`/`bg-accent` 等。已退役旧调色板（`bg-bg`/`bg-surface`/`text-text`/`text-text-muted`/`text-danger`…）——勿再使用。

## 三栏外壳

- `RootLayout`（`src/routes/root-layout.tsx`，**不要迁目录**）= 左导航 `SideNav` / 中 `Outlet` / 右 `PreviewColumn`。三栏子组件在 `src/components/layout/`。
- **栏宽重心**：右栏为主导列（lg `w-2/5` / xl `w-1/2`，基准是扣除 nav 的剩余宽——RootLayout 内层 wrapper 即为此存在，勿移除；`max-w-4xl` 封顶）；中栏内容收在 `max-w-4xl` 居中适读容器；左导航展开 `w-56` / 折叠 `w-16`。
- **SideNav 结构**（自上而下）：brand 行 → "New task" 主按钮（`nav-new-task`）→ 主导航 → `RecentTasks`（复用 `useTasksQuery({page:1,pageSize:8},{silent:true})`，**最近创建**序，折叠态隐藏）→ 头像式用户区。Recents 读取必须静默（不 toast），错误面是行内占位。
- 列折叠态（`navCollapsed`/`previewCollapsed`）与右栏选中态（`selectedVersionId` + `selectedArtifactId`）在 `features/ui/store`（Zustand）。**不变式**：单独 set `selectedVersionId` 且值变化时自动清空 `selectedArtifactId`；成对写入走 `selectArtifact(versionId, artifactId)`（同时展开右栏）。
- 任务生命周期 mutation（iterate/rollback/control）与 live task 帧需失效 `taskKeys.lists` 前缀，否则 TaskList/Recents 状态过期——新增 mutation 时记得带上。
- **TaskDetail = 对话回合流**：紧凑头部（标题/状态/控制条/完整 TokenBar）+ 滚动主体（每版本一回合 `components/tasks/ConversationTurn`：prompt 经 `useVersionQuery` 懒取静默降级 / 结果行 / 内联产物卡片 / 非当前回合的回滚尾部）+ 底部常驻 iterate composer（活跃禁用给原因，成功清空、失败保留输入）。EventLog 只出现在 `task.current_version` 对应回合。版本树组件已退役，勿复活。
- **Artifact 预览**：右栏 `features/artifacts/ArtifactPreviewPanel` 自带头部工具栏（选中产物标题 · 类型 + Copy / Refresh / 关闭；**全状态渲染**，no-version/loading/error/empty 也要有关闭按钮），按 store 选中态渲染产物列表 + 预览（图片 `<img>` / `text/html` 沙箱 iframe 富渲染（默认）+ 渲染/源码切换 / 文本截断 64KB / 其它仅下载），复用既有 `features/artifacts/` 数据访问，不新增 transport。`PreviewColumn` 是纯容器，不放头部。
- **iframe 安全红线**：`sandbox="allow-scripts"`，**绝不**加 `allow-same-origin`；frame 内 HTTP 失败跨域不可探测——不要试图检测，恢复手段是工具栏 Refresh（重新 presign 重挂）。Copy 只复制帽内完整文本，截断态禁用并引导下载。
- CSP（`index.html`）：图片预览依赖 `img-src`、HTML 渲染依赖 `frame-src`（当前均 `https:`）；文本预览经 OSS `fetch`，受 CORS 约束，失败降级为 download-only。`script-src 'self'` / `object-src 'none'` 保持锁定。

## 测试与约定

- 保留组件 `data-testid` 稳定，契约测试（vitest + Testing Library + MSW）充当回归网。改交互语义时同步改对应测试。
- 原生 `<select>`（状态/分组筛选）刻意保留，便于测试 `selectOptions`/change；不要换成 shadcn `Select`（portal 非原生）除非同步改测试。
- 提交前：`npm run typecheck && npm run lint && npm run test && npm run build` 全绿。
