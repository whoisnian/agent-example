## 1. Spike: lint guardrail path (D6)

- [x] 1.1 在分支上试装/试配 `eslint-plugin-tailwindcss` 的 v4-capable 版本，验证能否在本仓库 ESLint 9 flat config + Tailwind v4 下稳定运行（无崩溃、classname 校验生效）—— 仅有 pre-release（alpha/beta）支持 v4，且 v4 分支长期停滞、`latest` 仍是 v3-only 的 3.18.3
- [x] 1.2 记录 spike 结论到 design.md Open Questions：**采用路径 (b)**（`no-restricted-syntax` 禁色字面量 + grep CI 保底，退役 `eslint-plugin-tailwindcss`）

## 2. 依赖与构建链迁移

- [x] 2.1 安装 `tailwindcss@4`、`@tailwindcss/vite`、`tw-animate-css`；移除 `tailwindcss@3`、`tailwindcss-animate`、`autoprefixer`
- [x] 2.2 `vite.config.ts` 注册 `@tailwindcss/vite` 插件
- [x] 2.3 删除或精简 `postcss.config.js`（移除 `tailwindcss`/`autoprefixer`；若无其它插件则整体移除）—— 整体删除
- [x] 2.4 更新 `components.json`：`tailwind.config` 置为 `""`；`cssVariables: true` 已确认
- [x] 2.5 确认 `tailwind-merge` 与 Tailwind v4 对齐 —— 已装 `tailwind-merge@3.6.0`（v3.x 即 v4 line，v3.0 起支持 v4），无需 bump

## 3. 主题与样式入口迁移（CSS-first + OKLCH）

- [x] 3.1 `globals.css`：`@tailwind base/components/utilities` → `@import "tailwindcss"`（+ `tw-animate-css` 导入）
- [x] 3.2 把 `tailwind.config.js` 的 `theme.extend`（colors / borderRadius / fontFamily）搬到 `globals.css` 的 `@theme inline`（暴露 `--color-*`/`--radius-*`/`--font-*`）；`@custom-variant dark` 复刻 class 暗色
- [x] 3.3 `:root`/`.dark` 的 HSL 三元组机械等价转换为完整 `oklch(...)` 值（sRGB→OKLab→OKLCH 精确换算）
- [x] 3.4 把 accordion `keyframes`/`animation` 迁到 CSS（`@theme` `--animate-*` + `@keyframes`）
- [x] 3.5 删除 `tailwind.config.js`（v4 + `@tailwindcss/vite` 自动内容探测，无需保留）

## 4. 直接变量引用清理（D4）

- [x] 4.1 `globals.css` `.scrollbar-themed`：`hsl(var(--muted-foreground) / 0.4)` → `color-mix(in oklch, var(--muted-foreground) 40%/60%, transparent)`
- [x] 4.2 全仓扫描 `ring-[hsl(var(--*))]` 式运行时 arbitrary 值 —— 无运行时命中（仅测试夹具）
- [x] 4.3 迁移 `src/lib/cn.test.ts` 夹具到 `ring-[var(--color-ring)]`，重断言 `tailwind-merge` 保留行为
- [x] 4.4 验证门槛（scoped）：排除 `*.test.*` 与注释行后零运行时匹配 ✓

## 5. Lint 护栏落地（按 §1 spike 结论）

- [x] 5.1 路径 (b)：扩展 `no-restricted-syntax` 禁裸 hex + 裸 `oklch/rgb/hsl/...` 色函数 arbitrary（作用于 ts/tsx className/style）+ 新增 `lint:colors` node 脚本（grep 退役调色板/裸色字面量，排除 globals.css token 定义块）
- [x] 5.2 从 `eslint.config.js` 删除 `plugins.tailwindcss`、`settings.tailwindcss`、`no-custom-classname`/`no-contradicting-classname`（含未用的 path/fileURLToPath import）；卸载 `eslint-plugin-tailwindcss`；更新 `cn.test.ts` 注释；记录矛盾类名检测覆盖丢失（header 注释）
- [x] 5.3 自测护栏：裸 hex + 裸 `oklch(...)` className 被拒 ✓；`var(--color-ring)`+`bg-primary` 放行 ✓；token 定义不被误伤 ✓
- [x] 5.4 退役调色板类名零匹配（`lint:colors`，已纳入 `npm run lint`）✓

## 6. shadcn 基元验证（不重写，D7）

- [x] 6.1 跑全量 vitest（含 `ui.smoke.test.tsx`）：28 文件 / 202 测试全过，基元在 v4 下编译渲染通过
- [x] 6.2 核查 v4 默认值漂移：基元均用**显式** `focus-visible:ring-2`/`ring-ring`/`ring-offset-2`（不依赖 v4 改动的 bare 默认）；bare `border` 由 base `* { border-color: var(--border) }` 兜底；`--radius-*` 映射保留旧 radius；ring/offset 工具类已确认编译进产物 CSS。无需基元内校正

## 7. 规格与文档

- [x] 7.1 改写 `web/AGENTS.md`：vendoring 形态 → Tailwind 4 现状；主题 → CSS-first `@theme` + OKLCH + `@custom-variant`；颜色 lint posture → plugin-independent 护栏（`no-restricted-syntax` + `lint:colors`）
- [x] 7.2 `openspec/specs/web-design-system` delta 与落地一致（OKLCH/CSS-first/护栏；archive 时同步）

## 8. 验收

- [x] 8.1 `npm run typecheck` 全绿
- [x] 8.2 `npm run lint` 全绿（eslint + `lint:colors`）
- [x] 8.3 `npm run test` 全绿（28 文件 / 202 测试）
- [x] 8.4 `npm run build` 成功
- [x] 8.5 关键页面人工视觉对比 —— 用户 `npm run dev` 实测：与 v3 基线"most similar"，视觉等价确认（2026-06-14）
- [x] 8.6 浏览器基线 Safari 16.4+/Chrome 111+/Firefox 128+：MVP 面向现代浏览器，可接受；已记入 design Impact 与 `web/AGENTS.md` 语境
