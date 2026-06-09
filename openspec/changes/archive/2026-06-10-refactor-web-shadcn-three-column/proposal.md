## Why

当前 web 前端是「顶栏 + 左导航 + 单内容区」的两栏外壳，自带一套受限 Tailwind 语义调色板与手写 `Button` primitive，视觉与交互都较简陋，且任务详情页把版本树、事件流、成本堆在同一列、从不展示已产出的 artifacts。我们希望以业界成熟的 shadcn/ui 作为组件与主题基座，重构为参考 `~/Pictures/Screenshot.png` 的三栏式布局（左导航 / 中任务详情 / 右 Artifact 预览），让「选中任务 → 查看详情 → 预览/下载产物」成为顺畅的主工作流。

## What Changes

- **引入 shadcn/ui 基座**：新增 `cn()` 工具、`components/ui/` 下的 shadcn primitives（Button、Card、Badge、ScrollArea、Separator、Tabs、Tooltip 等按需）、Radix 依赖，并以 shadcn 标准 **CSS 变量主题**（`--background`/`--foreground`/`--primary`/`--muted` …）替换现有 `tailwind.config.js` 中受限的 `colors` 调色板。**BREAKING（前端内部约定）**：放宽「禁止生 hex / 禁止 arbitrary 值」的 lint 约束，改为以 CSS 变量 token 约束色彩；旧的 `bg`/`surface`/`accent` 等语义类名退役。
- **三栏外壳**：`RootLayout` 由两栏改为三栏 —— 左侧可折叠**导航栏**（Logo、Tasks/Cost/Settings、用户区/登出、最近任务入口）、中间**任务详情主区**、右侧可折叠 **Artifact 预览面板**。三列在窄视口下优雅降级（右栏抽屉化 / 单列回退）。
- **新增 Artifact 预览面板**：右栏渲染当前任务「选中版本」的 artifacts 列表（复用既有 `features/artifacts/` 数据访问），支持按需 presign 下载与文本/图片类产物的轻量预览；无产物时显示空态。VersionTree 由「行内展开懒加载」改为「行选中驱动右栏」（替代既有 `web-tasks-pages` 的行内披露契约）。
- **放开 CSP `img-src` 以支持图片预览**：现有 `web/index.html` 的 CSP 为 `img-src 'self' data:`，会拦截指向 OSS 的 `<img>`；需放开 `img-src` 至 OSS 源（多桶/多区域下用 `https:` 更现实），其余 `script-src 'self'` / `object-src 'none'` / `frame-ancestors 'none'` 保持不变。
- **全部页面迁移**：TaskList、TaskDetail、CostDashboard、TaskCreate、LoginPage 全部改用 shadcn primitives 重绘（行为/契约不变，仅展现层重构），并保留全部 `data-testid` 与既有契约测试约定。
- **文档同步**：更新 `docs/ARCHITECTURE.md §4.3` 的前端约定（主题策略、组件目录、三栏外壳）与 `web/AGENTS.md`/lint 说明，使其与新基座一致。

## Capabilities

### New Capabilities
- `web-design-system`: shadcn/ui 主题与组件基座 —— CSS 变量主题 token、`cn()` 合并工具、`components/ui/` primitives 目录约定、明暗主题契约，以及替代受限调色板后的色彩使用规则。
- `web-artifact-preview`: 三栏布局右栏的 Artifact 预览面板 —— 基于选中版本展示产物列表、空态、按需 presign 下载与轻量预览的 UI 行为契约（数据访问仍由既有 `web-artifacts-views` 提供）。

### Modified Capabilities
- `web-bootstrap`: (1)「Application Shell」由「顶栏 + 左导航栏 + 单内容槽」改为「三栏外壳（可折叠导航 / 任务详情主区 / 可折叠 Artifact 预览）」，明确响应式降级与列折叠状态归属全局 UI store；(2)「Design Tokens and Styling」由「硬编码语义调色板 + 禁所有 arbitrary」改为「shadcn CSS 变量 token + 仅禁裸 hex」。
- `web-tasks-pages`: 「Version Artifacts Expandable List With Direct Download」由「VersionTree 行内 expand/collapse 懒加载产物」改为「VersionTree 行选中驱动 `selectedVersionId` → 右栏 Artifact 预览」，下载/空态/单错误面契约迁移到 `web-artifact-preview`。

## Impact

- **代码**：`web/src/routes/root-layout.tsx`（`RootLayout` 维持原位、内部改挂三栏子组件）、`web/src/components/layout/*`（新增三栏子组件，SideNav/TopBar/ContentSlot 重构或退役）、新增 `web/src/components/ui/*` 与 `web/src/lib/cn.ts`、`web/src/components/primitives/Button.tsx` 退役迁移、`web/src/routes/*`（全部页面）、`web/src/components/tasks/VersionTree.tsx`（展开→选中）、`web/src/features/ui/store.ts`（新增列折叠/选中版本等 UI 状态）、`web/src/features/artifacts/*` 消费侧接入。
- **配置**：`web/tailwind.config.js`（主题切到 CSS 变量 + 接回 Tailwind 默认 scale）、`web/src/styles/globals.css`（注入 shadcn `:root`/`.dark` 变量、改 `@apply`）、`web/index.html`（body class 改新 token + CSP `img-src` 放开 OSS）、`web/eslint.config.js`（放宽 arbitrary 规则、仍禁裸 hex）、`web/package.json`（新增 Radix / class-variance-authority / clsx / tailwind-merge / tailwindcss-animate / lucide-react 等依赖）、新增 `components.json`（保留偶尔用 shadcn CLI 追加单个组件的能力）。
- **测试**：现有契约测试依赖 `data-testid` 与可访问性查询，迁移须保持选择器稳定；新增三栏外壳与 Artifact 预览面板的渲染/交互测试。
- **文档**：`docs/ARCHITECTURE.md §4.3`、前端 lint/约定说明。
- **不影响**：API / Worker / MQ / DB 契约，及 `features/*` 的数据访问与 React Query/Zustand 分工（仅展现层与外壳变化）。
