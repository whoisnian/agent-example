# Review: refactor-web-shadcn-three-column

**总体评价**：方向正确、动机清晰、决策记录（D1–D5）质量高，但**当前形态尚不可直接实施**——存在两处会让方案"做出来不对"的硬伤（CSP 会拦住图片预览、archived `web-tasks-pages` 的产物披露要求与本变更直接冲突却没有 MODIFIED delta），外加若干路径/事实错误与体积失真。修掉「严重」档 3 条后即可进入实现。

下面按 严重 / 中等 / 轻微 三档列出。文件以仓库根为基准的绝对路径标注。

---

## 严重（实施前必须解决）

### S1. 图片预览会被现有 CSP 直接拦截，spec 与设计都漏了这条约束
- **问题**：`web-artifact-preview/spec.md` 的 "Lightweight Artifact Content Preview" 要求 `image/*` 走 `<img src=<presigned OSS URL>>`；design.md D4 同样写"图片用 `<img>` 不经应用代理"。但 `/home/nian/Git/agent-example/web/index.html` 的 CSP meta 是 `img-src 'self' data:`，**不包含 OSS 源**。
- **依据**：`web-bootstrap` spec「Build Toolchain」要求 HTML entry 带 CSP meta 且 disallow inline scripts；现有 `index.html` 实际值 `img-src 'self' data:` 会让任何外站 `<img>`（presigned OSS URL）加载失败。文本预览走 `fetch` 没问题（`connect-src 'self' ws: wss: http: https:` 已放开），但图片走 `<img>` 必挂。这是"实现了也不工作"级别的缺陷。
- **建议**：在 design.md 增加一条决策、在 `web-artifact-preview` spec 增加一条 scenario，明确 CSP `img-src` 需新增 OSS 源（或更稳妥地放开为 `img-src 'self' data: https:`，并评估安全权衡）；同时把"修改 `index.html` CSP `img-src`"列入 tasks（当前 tasks.md §4 完全没提 CSP）。**待确认**：OSS 源是固定域名还是按租户/区域多桶（ARCHITECTURE §3.5 提到多桶多区域）——若多源，`img-src https:` 比逐一枚举更现实。验证方式：实现后在浏览器 console 看是否有 CSP `img-src` 拒绝日志。

### S2. 与 archived `web-tasks-pages` spec 直接冲突，但本变更未提供 MODIFIED delta
- **问题**：本变更把产物展示从"VersionTree 行内展开（`ArtifactList`）"搬到右栏 PreviewPanel，并由 `selectedVersionId` 驱动。但 `/home/nian/Git/agent-example/openspec/specs/web-tasks-pages/spec.md` 仍有一条 "Version Artifact Disclosure" 要求（L202–235）**强制规定**：每个 VersionTree 行 MUST 有 expand/collapse 披露控件、产物 MUST 在行展开时 inline 懒加载、任意版本行（不止 current）MUST 可展开。本变更没有对 `web-tasks-pages` 出 MODIFIED/REMOVED delta，会留下一条与新实现矛盾的已生效规格。
- **依据**：AGENTS.md §3「修改公共接口/能力须先提案」，OpenSpec 的 archived specs 是事实来源；现存 `web-tasks-pages` 的 disclosure 要求与新右栏方案互斥。`web-artifact-preview` 自己的 spec 也写 "updated when the user selects a version in the Task Detail version tree"——即 VersionTree 从"展开"语义变"选中"语义，恰好踩中 `web-tasks-pages` 的契约。
- **建议**：在本变更 `specs/` 下新增 `web-tasks-pages/spec.md` 的 MODIFIED delta：把"行内展开懒加载产物"改写为"行选中驱动 `selectedVersionId` → 右栏预览"，并显式说明 `version-expand-toggle` testid 与"行内 ArtifactList"语义的去留。否则归档时会出现规格自相矛盾。**注意**：现有 `VersionTree.tsx` 用 `version-expand-toggle` + 本地 `expanded` state 内联挂 `ArtifactList`，`web-tasks-pages` 有对应 scenario（L210+）和 `VersionTree.test.tsx`，这批断言会失效——见 M3。

### S3. 关键文件路径写错：`RootLayout` 不在 `components/layout/`，而在 `routes/root-layout.tsx`
- **问题**：proposal.md、design.md（Context/D3）、tasks.md（3.2）、`web-bootstrap` MODIFIED delta 全部把 `RootLayout` 定位在 `src/components/layout/`，并说"shell 组件 MUST 实现在 `src/components/layout/`"。实际 `RootLayout` 在 `/home/nian/Git/agent-example/web/src/routes/root-layout.tsx`，`components/layout/` 下只有 `TopBar.tsx`/`SideNav.tsx`/`ContentSlot.tsx`。
- **依据**：`find src` 结果显示 `src/routes/root-layout.tsx` 存在且 `TopBar.test.tsx` 从 `@/routes/root-layout` import `RootLayout`；`components/layout/` 无 `RootLayout.tsx`。沿用错误路径会让实现者要么找不到文件、要么新建一个并行的 RootLayout 造成双轨。
- **建议**：统一更正：要么明确"`RootLayout` 保持在 `routes/root-layout.tsx`，仅其内部三栏子组件落在 `components/layout/`"，要么在 tasks 里显式包含"把 root-layout 迁入 components/layout 并更新 `TopBar.test.tsx` import"。`web-bootstrap` 现行 spec 写的是"Shell components MUST be implemented in `src/components/layout/`"，而真实 `RootLayout` 在 routes/——本变更的 MODIFIED delta 应顺手澄清这个既有偏差，而非延续它。

---

## 中等（影响质量/可测性/体积，建议实现前定稿）

### M1. `web-bootstrap` 的「Design Tokens and Styling」要求被本变更破坏，却没被 MODIFIED
- **问题**：本变更退役 `bg/surface/accent/text/danger` 语义调色板、放宽 no-arbitrary lint。但 `web-bootstrap` spec 里 "Design Tokens and Styling" 要求（L77–83）明确规定调色板就是这套语义名、且"arbitrary 颜色/尺寸 SHALL be flagged by lint"，并有一条 scenario 断言 `bg-[#abcdef]` 必须 lint 失败。本变更只对 `web-bootstrap` 的 "Application Shell" 出了 MODIFIED，没碰 "Design Tokens"。
- **依据**：`web-bootstrap/spec.md` L77–83；本变更 design.md D2 明说这是"前端内部 BREAKING"。一个 BREAKING 不能只改 proposal 叙述而不改对应 spec 要求。
- **建议**：在本变更 `specs/web-bootstrap/spec.md` 增加对 "Design Tokens and Styling" 的 MODIFIED delta（语义名→CSS 变量 token、lint 从"禁所有 arbitrary"→"禁裸 hex、允许 `hsl(var(--*))`）。否则 `web-design-system`（新增 ADDED）与 `web-bootstrap`（旧 ADDED）会在归档库里对同一件事给出相反规则。

### M2. `index.html` 不在 `src/` 下，但它也用了退役 token，会被"无残留"扫描漏掉
- **问题**：`web-design-system` 的 "No retired semantic class names remain" scenario 限定"no source file under `src/`"。但 `/home/nian/Git/agent-example/web/index.html` 的 `<body class="bg-bg text-text">` 也是退役 token，且 `src/styles/globals.css` 里 `@apply bg-bg text-text` 也要改。CSP token 改动同理（见 S1）。
- **依据**：`index.html` body class 实测为 `bg-bg text-text`；scenario 的 `src/` 限定会让 `index.html` 合法地逃过验收。
- **建议**：把验收范围从 "under `src/`" 扩到 "包含 `index.html` 与 `globals.css`"，或在 tasks §6.2 显式列出 `index.html` body class 与 globals.css `@apply`。

### M3. VersionTree「展开」→「选中」是行为变更，spec 未定义旧 testid/可访问性契约的去留，且漏列受影响测试
- **问题**：现有 `VersionTree.tsx` 通过 `version-expand-toggle`（`aria-expanded`/`aria-label="Show artifacts"`）展开内联 `ArtifactList`。新方案改为"选中行→右栏"。`web-artifact-preview` spec 只说"user selects a version in the version tree"，没定义：选中用什么交互/testid、`version-expand-toggle` 是保留还是移除、`aria-expanded` 语义是否还在。tasks.md 5.4 只说"VersionTree 选中行写入 selectedVersionId"，5.6 笼统说"逐组件跑其单测"，但没点名 `VersionTree.test.tsx` 与 `web-tasks-pages` disclosure scenario 会失效。
- **依据**：`VersionTree.tsx` L99–122 的 expand 逻辑 + `ArtifactList` 内联挂载；`VersionTree.test.tsx`、`web-tasks-pages` L210–235 都依赖这套披露语义。这与 design.md「保持 data-testid 稳定使契约测试零改动」的硬约束直接矛盾——这里不可能零改动。
- **建议**：在 `web-artifact-preview` spec 增加一条 scenario 明确"选中"交互契约（建议新 testid 如 `version-select`，或复用行点击 + `aria-selected`/`data-selected`），并在 tasks 显式列出"重写 `VersionTree.test.tsx` 的展开断言为选中断言""移除/改写 `web-tasks-pages` disclosure scenario 对应测试"。

### M4. tasks 的 commit/PR 拆分与 AGENTS.md「单 PR ≤500 行」不现实，未给出分 PR 边界
- **问题**：tasks.md 把全部工作（基座 + 三栏 + 预览面板 + 5 个页面全量换皮 + 子组件迁移 + 文档）放在一个变更里，6 大节、约 30 个子任务。design.md Migration Plan 提到"逐 commit"，但没有把它切成多个 ≤500 行 PR 的边界。全量页面换皮 + 移植十余个 shadcn primitives，单 PR 体积会远超 500 行。
- **依据**：AGENTS.md §7「单次 PR 控制在 500 行以内（生成代码/测试除外）；超出请拆」；§3「一个变更聚焦一件事」。本变更实际是"换基座 + 改外壳 + 新增能力 + 全量重绘"四件事。
- **建议**：在 tasks.md 或 design.md 显式声明 PR 切分（例如：PR1 基座=§1+§2；PR2 三栏外壳+store=§3；PR3 预览面板=§4；PR4–5 分批页面=§5；PR6 清理文档=§6），并标注哪些行数靠 vendored 组件（可豁免）。**待确认**：是否要拆成多个 OpenSpec change——按 §3「一个变更聚焦一件事」，"引基座"与"全量重绘页面"严格说是两件事，但若团队接受单 change 多 PR 也可，需在 design 写明取舍。

### M5. React 19 + Radix + `eslint-plugin-tailwindcss` 的版本兼容只在 Risks 一笔带过，未落到依赖/任务的硬约束
- **问题**：design.md Risks 提到"个别 Radix 版本对 React 19 的兼容"，tasks 1.1 说"锁定支持 React 19 的版本"，但没给出最低版本基线。更关键的是 `eslint-plugin-tailwindcss@3.18.3`（见 `package.json` devDependencies）**只支持 Tailwind 3**，本变更恰好保持 Tailwind 3.4，所以这点 OK——但 spec/design 没说明"为何不升 Tailwind 4"，而 shadcn 新版默认面向 Tailwind 4 + `@theme`，照搬新版 shadcn 组件源码可能引入 Tailwind4-only 语法。
- **依据**：`package.json`：`tailwindcss ^3.4.19`、`eslint-plugin-tailwindcss ^3.18.3`、`react ^19.2.0`。shadcn 当前文档/CLI 默认 Tailwind 4；vendoring 时若直接拷新版组件会带入 `size-*`/`@theme` 等不一致写法。
- **建议**：在 design.md 增加一条决策：明确"vendoring 的是 shadcn 的 Tailwind-3 兼容版本（cva + `tailwind-merge`，CSS 变量 + `hsl()` 包裹，配 `tailwindcss-animate`）"，并锁定 Radix 各包的 React-19-OK 最低版本写进 tasks 1.1。**待确认**：逐个 `@radix-ui/react-*` 的 peerDeps 是否声明 `react@19`——验证方式 `npm info @radix-ui/react-<x> peerDependencies`。

### M6. 预览面板的"单错误面"约定在跨多个数据源时定义不全
- **问题**：`web-artifact-preview` spec 把"single-error-surface"约定从 list-read 复用到下载。但新增的**文本预览 fetch**（presign + 二次 `fetch(url)`）是第三个失败点：presign 成功但 `fetch(OSS)` 失败（网络/CORS/CSP）时谁出错误面、是否计入"单一错误面"，spec 没定义。
- **依据**：design.md D4 文本预览 = presign + fetch 两步；现有 `useArtifactPresignMutation` 的 silent 约定只覆盖 presign，不覆盖随后的裸 `fetch`。
- **建议**：在 spec 增补一条 scenario：文本预览的 OSS fetch 失败时，面板显示 inline 预览错误（不弹 toast 或恰好一条），与下载错误面区分；并在 tasks 4.3/4.5 增加该分支测试。**待确认**：OSS 是否对浏览器 `fetch` 配了 CORS `Access-Control-Allow-Origin`——若没有，文本预览的 `fetch` 会被 CORS 拦（`<img>` 不受 CORS 读限制但受 CSP 限制，见 S1）。这点可能让"文本截断预览"整体不可行，需在 ARCHITECTURE §3.5/artifacts-api 侧确认 presign URL 的 CORS 策略。

### M7. `web-bootstrap` MODIFIED delta 删掉了 TopBar，但 `top-bar`/`user-area` testid 与 `TopBar.test.tsx` 的处置不一致
- **问题**：MODIFIED 后的 "Application Shell" 不再有 top bar，用户区/登出并入左导航。tasks 3.4 说"保留 logout/user-email testid、迁移 TopBar.test.tsx"，但没提 `data-testid="top-bar"` 与 `data-testid="user-area"`（`TopBar.tsx` 现有）的去留，`TopBar.test.tsx` 还 import 了 `RootLayout` 做 gating 测试。
- **依据**：`TopBar.tsx` 有 `top-bar`/`user-area` testid；`TopBar.test.tsx` 渲染 `<TopBar/>` 独立组件 + 经 `RootLayout` 的登出 gating。若 TopBar 组件消亡，这两个 testid 和该测试文件需要明确归宿。
- **建议**：spec/tasks 明确：`top-bar`/`user-area` 是否退役、登出 gating 测试迁到哪个组件（左导航）。保持 `logout-button`/`user-email` 即可满足 `web-auth` 相关断言，但要把决定写进 tasks。

---

## 轻微（打磨项）

### L1. 明暗主题：落地 `.dark` 变量但 Non-Goal 不做切换 UI，spec 缺"默认主题如何选中"的可测断言
- **问题**：design.md 决定"仅落地变量、默认深色、不做切换 UI"。但 `web-design-system` spec 要求定义 `:root` 和 `.dark` 两套变量，却没有 scenario 说明默认走哪套、`<html>` 是否挂 `.dark` class（`darkMode:["class"]` 下不挂 class 就走 `:root`）。
- **建议**：增补一条 scenario 或在 spec 注明"MVP 默认观感放在 `:root`，`.dark` 预留但不激活"，避免实现者纠结把深色放 `:root` 还是 `.dark`。

### L2. `components.json` 列为"可能新增"，但 D1 已决定手动 vendoring，二者口径需统一
- **问题**：proposal Impact 说 "可能新增 `components.json`"，tasks 1.6 却把它列为确定任务。手动 vendoring 模式下 `components.json` 只在偶尔用 CLI 追加组件时有用。
- **建议**：统一表述——若坚持纯手动 vendoring，`components.json` 可选；若想保留"偶尔 CLI 添加单个组件"的能力则确定加。明确即可。

### L3. 64KB 文本上限：spec 写死 64KB，design Open Questions 仍标"实现时定"
- **问题**：`web-artifact-preview` spec scenario 已固化 64KB，而 design.md Open Questions 仍说"建议 64KB，实现时定"。两处口径不一致。
- **建议**：既然已写入 spec，把 design Open Question 改为已决策（64KB），或在 spec 用"a fixed byte cap (default 64KB)"措辞，保留可调空间但去掉矛盾。

### L4. tasks 缺少对 `Toaster`/`ErrorBoundary`/placeholders 等"也用退役 token"的清理项
- **问题**：tasks §5/§6 列了主要页面与子组件，但 grep 显示 `src/components/feedback/Toaster.tsx`、`ErrorBoundary.tsx`、`routes/placeholders/*` 也用 `bg-bg`/`text-text` 等退役 token，未单独点名。靠 6.2 的"全仓核对"兜底，易漏。
- **依据**：grep 命中 22 个文件含退役 token，包含上述 feedback/placeholder 文件。
- **建议**：在 6.2 附一份完整待改文件清单（22 个），或在验收里加"`grep` 退役 token 计数为 0（含 index.html/globals.css）"的硬门禁，呼应 spec 的"no retired class names"。

### L5. 性能/安全考量（presign 频次）已较周全，仅建议补一条"选中即预览"的防抖说明
- **问题**：design D4/Risks 已说明"仅在选中时拉取、不缓存 presign"。轻微补充：用户在 VersionTree 快速切换选中版本时，会连续触发 list query（有 React Query 缓存，OK）与文本预览 fetch（无缓存，每次 presign+fetch）。
- **建议**：spec/design 注明文本预览应在"选中某 artifact"而非"选中某 version"时才触发（spec 已暗示 per-artifact，明确即可），避免切版本就预拉。属锦上添花。

---

## 附：已核对正确、无需改动的点（供实现者放心）
- `cn` 已在 `eslint.config.js` 的 `tailwindcss.callees` 注册，新增 `cn()` 不需再配 lint callee。
- `connect-src 'self' ws: wss: http: https:` 已放开，文本预览的 `fetch(presigned OSS)` 不受 CSP `connect-src` 限制（但仍受 CORS 限制，见 M6）。
- `features/artifacts/`（`useVersionArtifactsQuery`/`useArtifactPresignMutation`/`PresignResult` 含 `mime`/`url`）数据访问已就绪，预览面板复用它、不新增 transport 的判断成立。
- 保留 `artifact-row`/`artifact-download`/`artifact-list-empty|loading|error` testid 以复用 `ArtifactList.test.tsx` 的方向可行——这些 testid 在现有测试中确实被直接查询。
