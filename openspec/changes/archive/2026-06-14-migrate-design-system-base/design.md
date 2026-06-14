## Context

`web/` 当前栈：Vite 8 + React 19 + TypeScript strict + **Tailwind 3.4** + React Query + Zustand。设计系统由归档规格 `web-design-system` 约束：shadcn 基元 vendored 在 `src/components/ui/`、`cn()` 合并工具、CSS 变量主题、`darkMode:["class"]`、MVP 默认深色由 `:root` 承载。

当前主题机制（v3）：
- `globals.css` 用 `@tailwind base/components/utilities` + `:root`/`.dark` 定义 **HSL 三元组**（如 `--background: 225 49% 8%`）。
- `tailwind.config.js`（99 行）把 `theme.extend.colors` 映射成 `hsl(var(--token))`，并刻意保留 Tailwind 默认 `spacing`/`fontSize` scale；动画走 `tailwindcss-animate` 插件 + JS 里的 accordion keyframes。
- `postcss.config.js` = `tailwindcss` + `autoprefixer`。
- `eslint.config.js` 用 `eslint-plugin-tailwindcss@3`（classname 校验）+ `no-restricted-syntax`（禁裸 hex）共同构成颜色护栏。

这是现代化三部曲的 **A（基座迁移）**。B（中性极简重设计）与 C（双主题 + 切换）依赖 A 落地。A 的硬约束：**视觉等价、独立可发布、`typecheck && lint && test && build` 全绿**。

## Goals / Non-Goals

**Goals:**
- 把 Tailwind 引擎从 v3 升到 v4，采用 CSS-first（`@theme`）配置范式。
- 主题 token 格式迁到 OKLCH，且**机械等价转换**现有深色调色板（视觉不可感知差异为目标）。
- 对齐 shadcn v4 token 形态：变量本身即完整颜色，去掉 `hsl(var(--token))` 包裹层；保持 utility 类名与组件代码不变。
- 迁移构建链（PostCSS/Vite 插件、动画插件），移除 `autoprefixer`。
- **恢复等效颜色护栏**（用户硬要求）：禁裸 hex + token 纪律。
- 改写 `web-design-system` 规格中 v3 写法的条款，改写 `web/AGENTS.md` 钉死 v3 的约定。

**Non-Goals:**
- 改任何颜色/间距/圆角的**视觉值**（B 负责）。
- 引入亮色主题或主题切换（C 负责）。
- 重新 vendoring shadcn 基元（未来独立 change）。
- 改三栏外壳、对话流、`data-testid`、契约测试语义。

## Decisions

### D1 · CSS-first，不走 `@config` 桥接
把 `tailwind.config.js` 的 theme 整体迁到 `globals.css` 的 `@theme`，`tailwind.config.js` 删除或仅保留 `content`（v4 多数场景可自动探测，design 落地时确认是否需要保留）。
- **理由**：三部曲目标是真正现代化；`@config` 桥接是半步状态，留着 JS 配置反而阻碍后续 OKLCH/主题工作。
- **Alternative（弃）**：`@config "./tailwind.config.js"` 兼容指令——能最快跑通 v4 引擎，但不解锁 CSS-first token 模型，等于把迁移债留给 B/C。

### D2 · 构建集成用 `@tailwindcss/vite`（而非 `@tailwindcss/postcss`）
在 `vite.config.ts` 注册 `@tailwindcss/vite` 插件，删除 `postcss.config.js` 里的 `tailwindcss`/`autoprefixer`（PostCSS 配置可整体移除，若无其它插件）。
- **理由**：本仓库已是 Vite 项目；官方推荐 Vite 插件路径，构建更快、配置更少、无需手动 `autoprefixer`。
- **Alternative（弃）**：`@tailwindcss/postcss` —— 保留 PostCSS 管线，迁移更"原地"，但多一层无谓的 PostCSS 配置，且本仓库 PostCSS 仅为 Tailwind 服务。

### D3 · OKLCH，机械等价转换现有调色板
把 `:root`/`.dark` 的每个 HSL 三元组转换为视觉等价的 `oklch(L C H)` 值；变量定义为**完整颜色**（`--background: oklch(...)`），通过 `@theme inline`（或等价映射）暴露为 `--color-*` 供 utility 解析。`bg-background`/`text-foreground` 等类名不变。
- **理由**：对齐最新 shadcn token 形态，让 B/C 在现代格式上工作；"等价转换"守住 A 的视觉等价承诺。
- **承认的 trade-off**：B 大概率重选全部颜色，A 转出来的 OKLCH 值多为"过渡性/可丢弃"。但 A 必须独立可发布、必须有**某套**合法 v4 token，最便宜的正确选择就是"今天的颜色、OKLCH 表达"。
- **Alternative（弃）**：在 v4 下继续用 HSL 格式——可行（v4 不强制 OKLCH），但与新 shadcn 形态割裂，B/C 仍要再迁一次格式。

### D4 · 直接 `hsl(var(--token))` 引用点的清理
D3 让变量变成完整颜色后，所有**直接**引用 `hsl(var(--token))` 的地方会失效，必须改：
- `globals.css` 的 `.scrollbar-themed`：`hsl(var(--muted-foreground) / 0.4)` → 用 v4 形态（如 `var(--color-muted-foreground)` 配 `color-mix()` 或 `oklch(from …)` 表达透明度）。
- 全仓扫描 `ring-[hsl(var(--ring))]` 式 arbitrary 值，改成对应 token 类或 v4 变量引用。
- **已知的非运行时命中（不算违规，但要处理）**：`src/lib/cn.test.ts:24-27` 是一条**故意的测试夹具**（断言 `tailwind-merge` 保留变量回退 arbitrary），本 change 将其迁到 v4 形态 `ring-[var(--color-ring)]` 并重断言合并行为；`globals.css` 注释里描述旧机制的 `hsl(var(...))` 字样属文档，不算运行时用法。
- **门槛（scoped）**：`grep -rE 'hsl\(var\(' web/src --include='*.ts' --include='*.tsx' --include='*.css'` 在**排除 `*.test.*` 与注释行**后返回零匹配（scrollbar 与任何运行时 arbitrary 都已迁移）。不要用裸 `grep` 误判测试夹具/注释。

### D5 · 动画插件 `tailwindcss-animate` → `tw-animate-css`
替换依赖；accordion 的 `keyframes`/`animation`（原在 JS config）迁到 `globals.css`（`@theme` 的 `--animate-*` + `@keyframes`，或 `tw-animate-css` 提供的等价物）。
- **理由**：`tailwindcss-animate` 不兼容 v4；`tw-animate-css` 是 shadcn 官方迁移后采用的替代。

### D6 · Lint 护栏（带 SPIKE 前置）
**SPIKE（apply 第一步必须先做）**：验证 `eslint-plugin-tailwindcss` 是否有能在本仓库 ESLint 9 flat config + Tailwind v4 下稳定工作的版本。
- **若 spike 通过 → 路径 (a)**：升级插件到 v4-capable 版本，保留 `no-custom-classname` / `no-contradicting-classname`，重配 `callees`/`config` 指向新形态。
- **若 spike 不通过 → 路径 (b)（保底）**：
  - 保留 `no-restricted-syntax` 禁裸 hex 规则（**不依赖插件，必然存活**），并扩展为同时禁裸 `oklch(...)`/`rgb(...)` 字面量出现在 **JSX className / inline style**（只允许走 token 类与 `--color-*`/`var(--*)` 变量）。**边界**：规则只作用于 `src/**/*.{ts,tsx}` 的 className/style 字面量；`globals.css` 的 `@theme`/`:root`/`.dark` token **定义块**是裸 `oklch(...)` 的唯一合法归宿，grep/规则必须**排除**它，否则误伤 token 定义。
  - 退役 `eslint-plugin-tailwindcss` 的 classname 校验时，必须**同步从 `eslint.config.js` 删除**：`plugins.tailwindcss` 注册、`settings.tailwindcss`（callees/config）、以及 `no-custom-classname`/`no-contradicting-classname` 两条规则——否则 flat config 因引用未知 plugin/rule 抛错。明确承认 `no-contradicting-classname` 的矛盾类名检测在路径 (b) 下**丢失**（grep 不替代）；同步更新 `cn.test.ts:16-18` 引用该规则的注释。
  - 用一个 **grep-based CI 检查**（脚本 + npm script）守"className 裸颜色字面量 / 退役调色板类名零匹配"，复用 `web-design-system` 已有 grep 范式（同样排除 token 定义块）。
  - 可选：引入 `stylelint` 校验 `globals.css` 非 token-定义处不出现裸 hex。
- **不变式（两条路径都要满足）**：className/style 裸 hex/`oklch`/`rgb` 仍被拒；`globals.css` token 定义块不被误伤；变量回退的 token 用法仍被允许；护栏在 `web/AGENTS.md` 有文档。

### D7 · shadcn 基元不重写
现有 `components/ui/*`（cva + `tailwind-merge`，个别 `@radix-ui/react-*`）在 v4 下应能编译；逐个 smoke 验证渲染（已有 `ui.smoke.test.tsx`）。如个别基元因 v4 默认值（ring 宽度、默认 radius）出现**视觉漂移**，在该基元内用显式类校正以维持等价，**不**借机升级到新 shadcn 形态（`data-slot`/`radix-ui` 统一包留给未来 change）。

## Risks / Trade-offs

- **[lint spike 失败，路径 (a) 不可行]** → 路径 (b) 是确定性保底：禁 hex 规则与 grep 检查不依赖任何 Tailwind 插件，护栏不会"长期缺失"，满足用户硬要求。
- **[v4 默认 utility 值变化致视觉漂移]**（ring 3px→1px、默认 radius、placeholder 颜色、`space-*`）→ 逐项与当前构建做视觉对比；默认 border 已被 `globals.css` 的 `* { border-color }` 兜底，影响小；漂移点在基元内显式校正（D7）。
- **[OKLCH 转换肉眼可辨的色差]** → 用可靠的 HSL→OKLCH 转换（同感知亮度/色相），关键面（bg/card/primary/destructive）人工比对；A 的目标是"等价"而非"像素一致"，可接受不可感知级差异。
- **[`@theme` 丢失 `tailwind.config.js` 里 fontFamily/borderRadius 等扩展]** → 迁移清单逐项搬运（colors、borderRadius、fontFamily、keyframes、animation），迁移后 diff 核对无遗漏。
- **[浏览器基线抬高]** → v4 需 Safari 16.4+/Chrome 111+/Firefox 128+。MVP 面向现代浏览器，确认可接受；写入 design 与 AGENTS 备注。
- **[契约测试误判]** → jsdom 不渲染真实 CSS，逻辑测试不受影响；真正回归靠 `build` + 人工视觉 + lint 三道门。
- **[CSP 影响]** → A 不引入任何新内联脚本/外部来源，`index.html` CSP 不动（双主题的内联脚本 CSP 问题属于 C，本 change 不触碰）。

## Migration Plan

1. **Spike（D6）**：在分支上试 `eslint-plugin-tailwindcss` v4 兼容性 → 定 (a)/(b)。
2. 升级依赖：装 `tailwindcss@4` + `@tailwindcss/vite` + `tw-animate-css`；移除 `tailwindcss@3`/`tailwindcss-animate`/`autoprefixer`。
3. 改构建：`vite.config.ts` 注册插件；删/简 `postcss.config.js`。
4. 改样式入口：`globals.css` 指令 → `@import`；搬 theme 到 `@theme`；HSL→OKLCH；迁 scrollbar 与 keyframes（D3/D4/D5）。
5. 缩减/删除 `tailwind.config.js`；更新 `components.json`（tailwind v4 字段）。
6. 扫描并修所有直接 `hsl(var(--*))` 引用（D4 grep 门）。
7. 应用 lint 护栏方案（D6）；更新 `eslint.config.js`。
8. 逐基元 smoke + 视觉校正（D7）。
9. 改 `web-design-system` 规格 delta 与 `web/AGENTS.md`。
10. 验收：`npm run typecheck && npm run lint && npm run test && npm run build` 全绿 + 关键页面人工视觉对比。

**Rollback**：本 change 局限于构建/样式/lint/文档层，无数据或 API 变更；回滚 = 还原分支（恢复 v3 依赖与配置文件）。

## Open Questions

- ~~D6 spike 的结论（(a) 还是 (b)）~~ **已定：路径 (b)**（apply 2026-06-14）。证据：`eslint-plugin-tailwindcss` 唯一支持 v4 的版本只有 pre-release（`4.0.0-alpha.x`/`4.0.0-beta.0`）；`beta.0` 发布于 2025-07-16，而 v3-only 的 `3.18.3` 反而晚至 2026-04-13 才发——`latest` 仍锁在 3.x，v4 分支自 2024-06 alpha.0 起停滞约 11 个月。把基座迁移的护栏押在长期停滞的 beta 上风险过高。故采用 (b)：`no-restricted-syntax` 禁色字面量（不依赖插件，必然存活）+ grep-based CI 检查，退役 `eslint-plugin-tailwindcss`。
- `tailwind.config.js` 在 v4 下是否需保留（仅 `content`）还是可完全删除——apply 时按 v4 自动内容探测实测确定。
