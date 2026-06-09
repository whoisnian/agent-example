## Context

`web/` 现状是 Vite 8 + React 19 + TS strict + Tailwind 3.4 + React Query + Zustand。外壳为 `RootLayout`（顶栏 `TopBar` + 左导航 `SideNav` + 内容槽 `ContentSlot`）。主题来自 `tailwind.config.js` 顶层 `theme.colors`/`spacing`/`fontSize`，**整体覆盖了 Tailwind 默认 scale**，并以一套语义 token（`bg`/`surface`/`border`/`text`/`accent`/…）+ eslint `tailwindcss` 插件 + 「禁止生 hex / arbitrary 值」约束色彩。组件层有手写 `components/primitives/Button.tsx` 与一批 `components/tasks/*`、`components/costs/*`，全部带 `data-testid`，并有大量基于 testid / a11y 查询的 vitest + Testing Library + MSW 契约测试。

`features/artifacts/` 数据访问（`useVersionArtifactsQuery`、`useArtifactPresignMutation`）已就绪，但 `TaskDetail` 当前**不渲染任何 artifacts**；`ArtifactList` 组件存在却未挂载（原为 VersionTree 行展开设计）。`features/ui/store.ts` 当前只承载 toasts，无任何布局/选择状态。

目标参考图 `~/Pictures/Screenshot.png` 是一个三栏布局：左侧细导航栏、中间主内容、右侧深色产物预览面板。

## Goals / Non-Goals

**Goals:**
- 以 shadcn/ui 为组件与主题基座，建立 `cn()` + `components/ui/` + CSS 变量主题（`:root`/`.dark`）的标准结构。
- `RootLayout` 重构为三栏：可折叠左导航 / 中任务详情主区 / 可折叠右 Artifact 预览面板，并在窄视口优雅降级。
- 右栏 Artifact 预览面板：基于「选中版本」展示产物列表、空态、presign 下载与文本/图片轻量预览。
- 全部页面（TaskList / TaskDetail / CostDashboard / TaskCreate / LoginPage）换用 shadcn primitives 重绘，**行为与 API 契约不变**。
- 保持既有 `data-testid` 选择器稳定，使现有契约测试尽量零改动通过；新增外壳与预览面板测试。
- 同步 `docs/ARCHITECTURE.md §4.3` 与前端 lint 约定。

**Non-Goals:**
- 不改 API / Worker / MQ / DB 任何契约，不改 `features/*` 的数据访问逻辑与 React Query/Zustand 分工。
- 不实现 `[Post-MVP]` 项（如 VersionTree 图形化 diff、artifact 富预览渲染引擎）；预览限定为文本截断 + 图片 `<img>` + 其它类型走下载。
- 不引入设计 token 自动化（Style Dictionary 等）或国际化重构。
- 不做明暗主题切换 UI（仅落地变量结构，默认沿用当前深色观感）。

## Decisions

### D1：shadcn/ui 以「手动 vendoring」方式引入，而非 CLI 全量初始化
shadcn 不是 npm 包，组件源码直接进仓库 `components/ui/`。采用按需手动添加所需 primitives（Button、Card、Badge、ScrollArea、Separator、Tabs、Tooltip、Input、Label、Textarea、Select、Skeleton、Sonner/Toast 视情况），配 `components.json`（保留偶尔用 CLI 追加单个组件的能力）。
- **为何**：避免 CLI 一次性拉入大量未用组件，违反「小步、500 行/PR」与「不夹带无关代码」红线。
- **依赖**：新增 `class-variance-authority`、`clsx`、`tailwind-merge`（`cn()`）、`lucide-react`（图标）、`tailwindcss-animate`，及实际用到的 `@radix-ui/react-*`（slot、scroll-area、tabs、tooltip、separator、label、select 等）。
- **Tailwind 版本基线（关键）**：本仓库保持 **Tailwind 3.4**（`eslint-plugin-tailwindcss@3.x` 仅支持 Tailwind 3，不升级避免 lint 链断裂）。但 shadcn 当前 CLI/文档默认面向 **Tailwind 4 + `@theme`**，直接拷贝新版组件源码会带入 Tailwind4-only 语法（如 `@theme` 指令、部分 `size-*` 约定）。因此 vendoring 必须采用 **shadcn 的 Tailwind-3 兼容形态**：cva + `tailwind-merge`、颜色走 `hsl(var(--token))` 包裹、动画靠 `tailwindcss-animate`，逐个组件核对无 Tailwind4-only 语法。
- **Radix × React 19**：逐个 `@radix-ui/react-*` 锁定声明 `react@19` peer 的版本（实现时 `npm info @radix-ui/react-<x> peerDependencies` 核对），写入 1.1 任务。
- **替代**：完全自研 primitives —— 放弃，违背用户「用流行的 shadcn/ui」诉求且重复造轮子。

### D2：主题全面切到 shadcn CSS 变量，接回 Tailwind 默认 scale
`globals.css` 注入 shadcn 标准 `:root` / `.dark` 变量（`--background`/`--foreground`/`--card`/`--primary`/`--muted`/`--accent`/`--border`/`--ring`/`--destructive` + `--radius`）；`tailwind.config.js` 改为 `darkMode:["class"]`，`theme.extend.colors` 映射这些变量（`hsl(var(--background))` 等），并**移除对 `colors`/`spacing`/`fontSize` 的顶层覆盖**，恢复 Tailwind 默认间距/字号 scale（shadcn 组件假定其存在）。语义旧类名（`bg`/`surface`/`accent`/`text-muted`/`danger`→`destructive` 等）退役，统一映射到新 token。
- **为何**：shadcn primitives 与默认 Tailwind scale 强耦合；保留受限 scale 会处处需要 arbitrary 值，反而更脏。
- **lint**：放宽 eslint「no arbitrary / no hex」规则（改为允许 `bg-background` 等变量类，仍禁裸 `#hex`）；这是已在 proposal 标注的前端内部 BREAKING。
- **回归保护**：旧组件中 `text-text` / `text-text-muted` / `bg-surface` 等需机械替换为 `text-foreground` / `text-muted-foreground` / `bg-card` 等，逐文件核对，避免漏改导致颜色丢失。

### D3：三栏外壳与全局 UI 状态归属
`RootLayout`（**保持原位 `src/routes/root-layout.tsx`**，不迁目录以免动 `TopBar.test.tsx` 等对 `@/routes/root-layout` 的 import；其三栏子组件落在 `src/components/layout/`）用 CSS grid 三列：`[nav 固定宽] [main 1fr] [preview 固定宽]`。左导航与右预览的折叠状态、以及右栏「当前选中版本」状态，落在扩展后的 `features/ui/store.ts`（Zustand），符合 AGENTS.md「本地 UI 状态走 Zustand」。注意现行 `web-bootstrap` spec 原文写「Shell components MUST be implemented in `src/components/layout/`」，而真实 `RootLayout` 一直在 `routes/`——本变更的 MODIFIED delta 顺手澄清此既有偏差（wrapper 在 routes、子组件在 components/layout）。
- 新增 store 字段：`navCollapsed`/`previewCollapsed`（含 toggle）、`selectedVersionId`（右栏预览的数据锚点；由 TaskDetail 的 VersionTree 选择驱动，默认取 `current_version`）。
- **响应式**：`lg` 以上三列并排；`md` 隐藏右栏改为按钮触发的抽屉/overlay；`sm` 导航折叠为图标条或顶部抽屉。用纯 CSS + store 标志实现，不引入额外布局库。
- **替代**：把折叠/选择状态放进 URL search param —— 放弃（增加路由复杂度，MVP 不需要可分享性）；放进 React Query —— 违背 Zustand/Query 分工。

### D4：右栏 Artifact 预览面板复用既有数据访问，VersionTree 由「展开」改「选中」
预览面板 `components/layout/PreviewPanel.tsx`（或 `features/artifacts/ArtifactPreview.tsx`）消费 `useVersionArtifactsQuery(selectedVersionId)` 与 `useArtifactPresignMutation`，渲染列表 + 选中产物的轻量预览：
- 文本类（`mime` 以 `text/` 开头或 json/yaml）：presign 后 `fetch` 截断展示（默认 64KB，超出提示下载）。
- 图片类（`image/*`）：presign URL 直接 `<img>`。
- 其它：仅下载按钮。
- 空态 / 加载 / 错误三态沿用既有 silent 约定（`meta.silent` + `toastOnError:false`，组件 `onError` 为唯一错误面）。
- **VersionTree 交互变更**：现行 `VersionTree.tsx` 用 `version-expand-toggle`（`aria-expanded`/`aria-label="Show artifacts"`）行内展开 `ArtifactList`；新方案改为**行选中**（建议新增稳定 `version-select` 交互 + `aria-selected`/`data-selected`），选中写入 `selectedVersionId` 驱动右栏。默认选中 `current_version`。这是 `web-tasks-pages`「Version Artifacts Expandable List」的 spec 级行为变更（见对应 MODIFIED delta），`VersionTree.test.tsx` 与该 spec 的展开类断言**必然要改写**——这是 design 中「保持 testid 稳定使测试零改动」硬约束的**已知例外**，须在 tasks 显式点名。
- 旧 `ArtifactList` 的下载逻辑迁入此面板；保留其 `data-testid`（`artifact-row`/`artifact-download`/`artifact-list-empty|loading|error`）以复用测试。
- **两道跨源关卡（实现前须验证）**：图片 `<img>` 受 **CSP `img-src`** 限制（见 D6）；文本预览的 `fetch(presigned OSS URL)` 受 **CORS** 限制（OSS 桶须回 `Access-Control-Allow-Origin`）。文本预览的 `fetch` 是 presign 之后的**第三个失败点**（presign 成功但 OSS fetch 失败），spec 要求它降级为「download-only + 单条 inline 预览错误」，与下载错误面区分。CORS 是否就绪取决于 OSS/artifacts-api 配置，列入 Open Questions 待验证。

### D5：迁移顺序——基座先行，逐页换皮，testid 不动
顺序：(1) 依赖 + `cn` + 主题变量 + tailwind/eslint 配置；(2) 移植所需 `components/ui/*`；(3) 三栏 `RootLayout` + 导航 + store 扩展；(4) 逐页迁移（Login → TaskList → TaskDetail → Preview 面板 → Cost → Create）；(5) 文档与清理（退役 `primitives/Button`）。每步保持 `npm run typecheck/lint/test` 绿。
- **为何**：基座不先就绪，页面无法编译；逐页换皮便于 review、控制单 PR 体积。
- **测试策略**：迁移以「保持 `data-testid` 与可访问 name 稳定」为硬约束，使契约测试充当回归网；仅在结构必然变化处（详情页拆到三栏、VersionTree 展开→选中）调整对应测试。

### D6：放开 CSP `img-src` 以支持图片预览，其余策略保持锁定
现行 `web/index.html` 的 CSP 为 `img-src 'self' data:`，会让指向 OSS 的 presigned `<img>` 直接被拦（「实现了也不工作」）。本变更将 `img-src` 放开到 OSS 源：鉴于 `docs/ARCHITECTURE.md` 描述 OSS 可能多桶/多区域，逐一枚举 host 不现实，采用 `img-src 'self' data: https:`；同时**保持** `script-src 'self'`（禁 inline/eval）、`object-src 'none'`、`frame-ancestors 'none'` 不变，避免顺手放松脚本策略。`connect-src` 已是 `'self' ws: wss: http: https:`，文本预览的 `fetch` 不受 `connect-src` 限制（但仍受 CORS 限制，见 D4）。
- **替代**：枚举具体 OSS host —— 多桶多区域下维护成本高、易漏；`img-src https:` 更稳健，安全权衡是放宽到任意 https 图片源（对一个仅展示自家产物的内部平台可接受）。
- **任务归属**：tasks §4 显式加入「改 `index.html` CSP `img-src`」，不再遗漏。

## Risks / Trade-offs

- **[主题切换大面积改色类]**：旧语义类 → 新 token 的机械替换易漏，导致局部颜色/对比丢失。→ 按文件清单逐个替换并跑 `eslint-plugin-tailwindcss`；保留深色默认观感，靠现有渲染测试 + 人工对照截图兜底。
- **[契约测试因结构调整而碎]**：三栏化把详情页内容拆列，可能动摇基于 DOM 结构的查询。→ 优先用 `data-testid` / role+name 查询而非结构层级；迁移中同步更新断点处测试，保持断言意图不变。
- **[依赖体积与 React 19 兼容]**：Radix / lucide 增加包体；个别 Radix 版本对 React 19 的兼容。→ 仅装实际用到的 `@radix-ui/react-*`，锁定支持 React 19 的版本；`npm run build` 体积纳入验收。
- **[presign 文本预览的额外请求/泄露]**：右栏预览会对文本产物发起一次 presign+fetch，且 URL 短时有效。→ 截断字节上限、不缓存 presign（沿用 mutation 语义）、仅在用户选中该产物时拉取；图片用 `<img>` 不经应用代理。
- **[lint 放宽降低色彩纪律]**：允许变量类后可能有人混用裸 hex。→ 仅放宽到「允许 token 类 + arbitrary 仅限 `hsl(var(--*))`」，仍禁裸 `#hex`，在 `web/AGENTS.md` 写明。
- **[与 docs/ARCHITECTURE.md 偏离]**：§4.3 现描述旧外壳与调色板。→ 本变更显式更新该节，避免文档/实现冲突（AGENTS.md「唯一架构事实来源」要求）。

## Migration Plan

本变更是「换基座 + 改外壳 + 新增能力 + 全量重绘」四件相对独立的事，单 PR 必然远超 AGENTS.md §7 的 500 行上限。采用**单 OpenSpec change、多 PR**落地（vendored 组件源码行数按 §7「生成代码除外」豁免，但仍按组切分便于 review）：

- **PR1｜基座**（tasks §1+§2）：依赖、`cn`、CSS 变量主题、tailwind/eslint/`index.html`(body class+CSP) 配置、移植 `components/ui/*`。先保证旧页面仍编译/测试绿——必要时临时保留旧 token 别名。
- **PR2｜三栏外壳 + store**（tasks §3）：`RootLayout` 三栏化、导航列、`features/ui/store` 扩展、响应式降级。
- **PR3｜Artifact 预览面板**（tasks §4）：右栏面板 + 轻量预览 + CSP 验证。
- **PR4–5｜页面分批**（tasks §5）：建议 PR4 = Login + TaskList + TaskCreate，PR5 = TaskDetail（含 VersionTree 展开→选中）+ CostDashboard + 子组件迁移。
- **PR6｜清理与文档**（tasks §6）：退役 `primitives/Button`、清旧 token 别名、`grep` 门禁、更新 `docs/ARCHITECTURE.md §4.3` 与 `web/AGENTS.md`。

每个 PR 自身保持 `typecheck/lint/test` 绿。
- **回滚**：纯前端、无数据迁移；任一 PR 出问题可独立 revert，外壳/页面互相解耦，回滚不影响 API/Worker。

## Open Questions

- 是否本轮就提供明暗主题切换控件？当前决定**否**（仅落地变量，MVP 默认观感放 `:root`，`.dark` 预留不激活）；如需可后续小 change 增加。
- 文本预览字节上限：**已决策默认 64KB**（写入 spec scenario，措辞保留「default」以便后续可调）。
- **[待验证·阻塞文本预览能力]** OSS presigned URL 是否对浏览器 `fetch` 配了 CORS `Access-Control-Allow-Origin`（与 CSP 是两道独立关卡）。验证方式：拿一个真实 presigned URL 在浏览器 console `fetch` 看是否被 CORS 拦。若 OSS 未放 CORS，则「文本截断预览」整体不可行——此时降级为 download-only（spec 已定义该降级路径），并视需要在 artifacts-api/OSS 侧另开 change 配置 CORS。图片 `<img>` 不受 CORS 读限制，仅受 CSP（D6 已处理）。
- CSP `img-src` 放宽口径：当前取 `https:`（多桶多区域更现实）；若后续 OSS 收敛为固定域名，可改为枚举 host 收紧。
