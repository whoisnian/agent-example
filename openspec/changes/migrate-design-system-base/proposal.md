## Why

前端基座钉死在 Tailwind v3（`web/AGENTS.md` 明确拒绝升 v4，因为 `eslint-plugin-tailwindcss@3` 只支持 v3）。这阻断了设计系统现代化的后续工作（中性极简重设计、双主题切换），也让我们无法对齐最新 shadcn 的 token 形态。本 change 是现代化三部曲的第一步（A），先把地基从 v3 迁到 v4，**视觉尽量等价、独立可发布**，为后续的重设计（B）与双主题（C）解锁干净的基座。

## What Changes

- **BREAKING（构建）**：Tailwind v3.4 → v4。`@tailwind base/components/utilities` 指令 → 单个 `@import "tailwindcss"`；`tailwind.config.js`（99 行 `theme.extend`）迁移到 `globals.css` 的 CSS `@theme`（CSS-first，big-bang，不走 `@config` 桥接）。
- **构建链**：`postcss.config.js` 的 `tailwindcss` + `autoprefixer` 替换为 v4 集成方案（`@tailwindcss/vite` 或 `@tailwindcss/postcss`，design 里二选一）；`autoprefixer` 移除（v4 内置）。
- **颜色 token 格式**：HSL 三元组 → OKLCH，但**保持当前深色调色板视觉等价**（机械转换现有 `:root`/`.dark` 值）。对齐 shadcn v4 形态：CSS 变量本身即完整颜色（`--background: oklch(...)`），不再 `hsl(var(--token))` 包裹；utility 类名（`bg-background` 等）与组件代码保持不变。
- **直接变量引用迁移**：清理代码与 CSS 中直接写 `hsl(var(--token))` 的地方（`globals.css` 的 `.scrollbar-themed` 工具类、任何 `ring-[hsl(var(--ring))]` 式 arbitrary 值），改成 v4 token 形态。
- **动画插件**：`tailwindcss-animate` → `tw-animate-css`；accordion `keyframes`/`animation` 从 JS config 迁到 CSS。
- **Lint 护栏（必须保持等效）**：`eslint-plugin-tailwindcss@3` 不支持 v4。经 design 阶段的 spike 决策后，恢复"禁裸 hex + token 纪律"的等效护栏（禁 hex 的 `no-restricted-syntax` 规则不依赖该插件，可保留）。
- **shadcn 基元**：A **不**重新 vendoring；现有 `components/ui/*` 仅需在 v4 下编译通过即可（re-vendoring 留作未来独立 change，避免动 `data-testid` 与契约测试网）。
- **文档**：改写 `web/AGENTS.md` 中"钉死 v3 / 不升 v4"与 `tailwind.config.js` 相关的约定条款。

**非目标（明确排除）**：不改任何颜色/间距/圆角的**视觉值**（留给 B）；不引入亮色主题或切换（留给 C）；不动三栏外壳、对话流、`data-testid`。

## Capabilities

### New Capabilities
<!-- 无新增能力；本 change 是既有设计系统能力的基座迁移 -->

### Modified Capabilities
- `web-design-system`: 主题 token 的表达方式从"`tailwind.config.js` 映射 `hsl(var(--token))`"改为"CSS `@theme` + OKLCH 变量"；"`tailwind.config.js` MUST NOT override default spacing/fontSize"条款随 v4 CSS-first 模型改写；颜色 lint posture 条款从"依赖 `eslint-plugin-tailwindcss` 的变量回退 arbitrary 校验"改写为 v4 下的等效护栏表述；顺带**修正**已过时的"at least"必备 token 列表，使其与现网 `globals.css`/`tailwind.config.js` 实际已提供的一致（补 `--popover(-foreground)`/`--secondary(-foreground)`/`--success(-foreground)`/`--warning(-foreground)`），让 B/C 继承一份正确契约。组件基座（vendored under `components/ui/`、`cn()`、按需添加）、`darkMode` class 策略、MVP 默认深色由 `:root` 承载等要求**保持不变**（spec delta 中以注释显式标注 Component Foundation 故意不改）。

## Impact

- **依赖**：移除 `tailwindcss@3`、`tailwindcss-animate`、`autoprefixer`；新增 `tailwindcss@4`、v4 构建插件（`@tailwindcss/vite` 或 `@tailwindcss/postcss`）、`tw-animate-css`；调整 `eslint-plugin-tailwindcss`（升级或退役其 classname 校验）。
- **构建配置**：`web/postcss.config.js`、`web/vite.config.ts`、`web/tailwind.config.js`（大幅缩减或删除）、`web/eslint.config.js`、`web/components.json`。
- **样式入口**：`web/src/styles/globals.css`（指令、`@theme`、token 格式、scrollbar 工具类、accordion keyframes）。
- **组件**：`web/src/components/ui/*` 验证在 v4 下编译与渲染正确（不重写）；扫描全仓直接 `hsl(var(--*))` 引用点。
- **规格/文档**：`openspec/specs/web-design-system/spec.md`（delta）、`web/AGENTS.md`。
- **浏览器基线抬高**：v4 需 Safari 16.4+ / Chrome 111+ / Firefox 128+（`@property`、`color-mix()`），design 阶段确认可接受。
- **测试**：vitest 契约测试在 jsdom 下不渲染真实 Tailwind CSS，逻辑预期不受影响；`npm run build` 与视觉需重验；`npm run lint` 随护栏方案重验。验收门槛：`typecheck && lint && test && build` 全绿。
- **PR 体量（AGENTS §7 ~500 行）**：A 的核心 PR = 依赖 + vite/postcss/tailwind/eslint 配置 + `globals.css` 重写 + `components.json` + 文档/spec。建议 §1 lint spike 作为独立前置 commit；D7/6.2 的基元视觉校正若意外波及大量文件，可拆为后续独立 change，保核心 PR 在软上限内。
