> **PR 切分（单 change 多 PR，见 design.md Migration Plan）**：PR1=§1+§2（基座）｜PR2=§3（外壳）｜PR3=§4（预览面板）｜PR4=§5.1-5.3+部分子组件｜PR5=§5.4-5.6（详情+VersionTree+成本）｜PR6=§6（清理+文档）。每个 PR 自身 `typecheck/lint/test` 绿；vendored 组件行数按 AGENTS.md §7「生成代码除外」豁免。

## 1. 基座：依赖、cn、主题、配置（PR1）

- [x] 1.1 安装依赖：`class-variance-authority`、`clsx`、`tailwind-merge`、`tailwindcss-animate`、`lucide-react`，及实际用到的 `@radix-ui/react-*`（slot/scroll-area/tabs/tooltip/separator/label/select）。逐个 `npm info @radix-ui/react-<x> peerDependencies` 核对并锁定声明 `react@19` peer 的版本
- [x] 1.2 新增 `src/lib/cn.ts`（`clsx` + `tailwind-merge`）并补单测（`cn` 已在 `eslint.config.js` 的 `tailwindcss.callees` 注册，无需再配 lint callee）
- [x] 1.3 在 `src/styles/globals.css` 注入 shadcn `:root` 与 `.dark` 变量（`--background`/`--foreground`/`--card`/`--primary`/`--muted`/`--accent`/`--border`/`--input`/`--ring`/`--destructive`/`--radius`）；MVP 默认观感放 `:root`（不挂 `.dark`），并把现有 `@apply bg-bg text-text` 改为新 token
- [x] 1.4 改写 `tailwind.config.js`：`darkMode:["class"]`，`theme.extend.colors` 映射 CSS 变量；移除对 `colors`/`spacing`/`fontSize` 的顶层覆盖以恢复 Tailwind 默认 scale；加入 `tailwindcss-animate` 插件
- [x] 1.5 改 `web/index.html`：`<body class>` 由 `bg-bg text-text` 换为新 token；CSP `img-src` 放开为 `'self' data: https:`（保持 `script-src 'self'`/`object-src 'none'`/`frame-ancestors 'none'` 不变）
- [x] 1.6 放宽 `eslint.config.js` 颜色规则：允许 `hsl(var(--*))` 形式的 arbitrary 值，仍禁裸 `#hex`
- [x] 1.7 新增 `components.json`（shadcn 配置，路径别名指向 `components/ui`/`lib`，保留偶尔用 CLI 追加单组件的能力）
- [x] 1.8 确认 `npm run typecheck && npm run lint && npm run test` 全绿（必要时临时保留旧 token 别名以免旧页面编译失败）；确认 vendoring 的是 shadcn 的 **Tailwind-3 兼容形态**（无 `@theme`/Tailwind4-only 语法）

## 2. 移植 shadcn primitives 到 components/ui（PR1）

- [x] 2.1 移植 `Button`（cva 变体），并提供从旧 `primitives/Button` API 的等价变体映射（`primary→default`、`ghost→ghost`、`danger→destructive`）
- [x] 2.2 移植 `Card`、`Badge`、`Separator`、`ScrollArea`
- [x] 2.3 移植表单类 `Input`、`Label`、`Textarea`、`Select`
- [x] 2.4 移植 `Tabs`、`Tooltip`、`Skeleton`（用于加载态）
- [x] 2.5 为新增 primitives 补最小渲染冒烟测试（保证可用、a11y role 正确）

## 3. 三栏外壳与全局 UI 状态（PR2）

- [x] 3.1 扩展 `features/ui/store.ts`：新增 `navCollapsed`/`previewCollapsed`（含 toggle）、`selectedVersionId`（默认取任务 `current_version`）及对应 setter；补 store 单测
- [x] 3.2 三栏化 `RootLayout`（**保持文件在 `src/routes/root-layout.tsx`**，不迁目录，避免动 `TopBar.test.tsx` 的 `@/routes/root-layout` import）：CSS grid 三列，子组件落在 `components/layout/`；保持 `data-testid="root-layout"`
- [x] 3.3 重写 `SideNav` 为左导航列（Logo、Tasks/Cost/Settings、用户区+登出、最近任务入口），用 shadcn + lucide 图标；保留 `data-testid="side-nav"` 与 `nav-*` 选择器及 active 样式
- [x] 3.4 将 `TopBar` 的用户区/登出能力并入左导航列；移除或精简旧顶栏。明确处置：保留 `logout-button`/`user-email`（满足 `web-auth` 断言），并决定 `top-bar`/`user-area` testid 去留；把 `TopBar.test.tsx` 的登出 gating 测试迁到承载登出的左导航组件
- [x] 3.5 实现响应式降级：宽视口三列并排，窄视口右栏抽屉化、左栏折叠为图标条；补外壳折叠/响应式渲染测试
- [x] 3.6 退役 `components/layout/ContentSlot.tsx`（如被三栏取代）或改造为中列容器

## 4. Artifact 预览面板（右栏）（PR3）

- [x] 4.1 新增 `PreviewPanel`（右栏容器）：读 `selectedVersionId`，无选中版本时渲染空/占位态
- [x] 4.2 迁移 `ArtifactList` 下载逻辑入面板：列表渲染（kind/mime/size）、presign 按需下载、加载/空/错误三态，保留 `artifact-row`/`artifact-download`/`artifact-list-empty`/`artifact-list-loading`/`artifact-list-error` testid 与单错误面约定
- [x] 4.3 实现轻量内容预览：`image/*` 走 `<img>`；text/json/yaml 走 presign+fetch 截断（默认 64KB 上限 + 截断提示）；其它仅下载
- [x] 4.4 预览仅对用户**选中的单个 artifact**触发 fetch（切换 version 不预拉），不缓存 presign（沿用 mutation 语义）
- [x] 4.5 验证两道跨源关卡：图片 `<img>` 不被 CSP `img-src` 拦（浏览器 console 无 violation）；文本 `fetch(presigned OSS)` 的 CORS——若 OSS 未放 `Access-Control-Allow-Origin`，文本预览降级为 download-only + 单条 inline 预览错误（见 design Open Questions）
- [x] 4.6 新增预览面板测试：选中版本驱动、空态、下载 re-mint、下载单错误面、文本 fetch 失败降级、图片/文本/二进制三类预览分支

## 5. 页面迁移（行为不变，仅换皮）（PR4–PR5）

- [x] 5.1 `LoginPage`：改用 shadcn `Card`/`Input`/`Label`/`Button`；保留内联错误面与既有 testid，使 `LoginPage.test.tsx` 通过（PR4）
- [x] 5.2 `TaskList`：表格/分页/状态筛选改用 shadcn primitives；保留 `task-row`/`status-filter`/`page-*`/`new-task-button` 等 testid，使 `TaskList.test.tsx` 通过（PR4）
- [x] 5.3 `TaskCreate`：表单改用 shadcn 表单 primitives；保留校验与 testid，使 `TaskCreate.test.tsx` 通过（PR4）
- [x] 5.4 `TaskDetail`：迁入三栏中列，header/状态/成本/控制条/版本树/事件流用 shadcn 重绘；产物展示从详情页中列移除、改由右栏面板承载（PR5）
- [x] 5.5 `VersionTree` 展开→选中改造：移除 `version-expand-toggle`+内联 `ArtifactList`，改为行选中（`version-select` + `aria-selected`/`data-selected`）写入 `selectedVersionId`，默认选中 `current_version`；**改写** `VersionTree.test.tsx` 的展开类断言为选中断言（这是 design 硬约束的已知例外）（PR5）
- [x] 5.6 `CostDashboard`：分组选择/窗口/条形图改用 shadcn primitives；保留 `amount_usd` 字符串规则与 testid，使 `CostDashboard.test.tsx` 通过（PR5）
- [x] 5.7 迁移 `StatusBadge`/`CostBadge`/`TokenBar`/`ControlBar`/`EventLog`/`RollbackControl` 等子组件到新 token 与 shadcn primitives，逐组件跑其单测（PR5）

## 6. 清理与文档（PR6）

- [x] 6.1 退役 `components/primitives/Button.tsx`，移除所有旧引用；清理临时保留的旧 token 别名
- [x] 6.2 全仓核对无残留旧语义类——覆盖 `src/**`、`web/index.html`、`src/styles/globals.css`（含 `feedback/Toaster.tsx`、`feedback/ErrorBoundary.tsx`、`routes/placeholders/*` 等约 22 个命中文件）；加硬门禁：`grep -rE 'bg-bg|bg-surface|text-text|text-text-muted|bg-accent|bg-danger|border-border' web/src web/index.html` 计数为 0
- [x] 6.3 更新 `docs/ARCHITECTURE.md §4.3`：三栏外壳、shadcn 主题策略、`components/ui`/`lib/cn` 目录约定、放宽后的颜色 lint 规则、CSP `img-src` 变更
- [x] 6.4 更新 `web/AGENTS.md`（或新增）说明新颜色规则（禁裸 hex、允许 `hsl(var(--*))`）与组件目录约定
- [x] 6.5 终检：`npm run typecheck && npm run lint && npm run test && npm run build` 全绿，`npm run build` 体积纳入验收
