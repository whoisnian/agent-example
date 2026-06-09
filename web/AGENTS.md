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
- 列折叠态（`navCollapsed`/`previewCollapsed`）与右栏「选中版本」(`selectedVersionId`) 在 `features/ui/store`（Zustand）。
- **Artifact 预览**：右栏 `features/artifacts/ArtifactPreviewPanel` 按 `selectedVersionId` 渲染产物列表 + 轻量预览（图片 `<img>` / 文本截断 64KB / 其它仅下载），复用既有 `features/artifacts/` 数据访问，不新增 transport。VersionTree 行**选中**（非展开）驱动它。
- 图片预览依赖 CSP `img-src` 含 OSS（`index.html`，当前 `https:`）；文本预览经 OSS `fetch`，受 CORS 约束，失败降级为 download-only。

## 测试与约定

- 保留组件 `data-testid` 稳定，契约测试（vitest + Testing Library + MSW）充当回归网。改交互语义时同步改对应测试。
- 原生 `<select>`（状态/分组筛选）刻意保留，便于测试 `selectOptions`/change；不要换成 shadcn `Select`（portal 非原生）除非同步改测试。
- 提交前：`npm run typecheck && npm run lint && npm run test && npm run build` 全绿。
