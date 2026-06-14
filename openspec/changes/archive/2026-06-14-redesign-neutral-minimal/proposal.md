> **依赖顺序（硬）**：本 change MUST 在 A（`migrate-design-system-base`）之后 apply 与 archive。B 只新增值层要求，不 MODIFY A 拥有的 `CSS-Variable Theme Tokens`，因此 A→B 的 archive 顺序对结构 spec 幂等。

## Why

现代化三部曲第二步（B）。地基迁到 Tailwind v4 后（A），把视觉语言从当前的 **Linear 风深色靛蓝**重做成 **Vercel/Geist 风中性极简**：中性灰阶、大留白、极简边框、近单色的 primary。这是一次纯视觉重设计——只改 token 的**值**与排版/圆角节奏，不动外壳结构、对话流与契约。

## What Changes

- **调色板重选（中性极简）**：用中性灰阶替换靛蓝-深navy 身份。`--primary` 从品牌靛蓝（`#6366f1`）改为**近单色高对比**（浅色近黑 / 深色近白），强调色仅保留在链接/焦点/语义态。语义色（`destructive`/`success`/`warning`）保留但按中性极简基调去饱和校准。
- **同时定义 light + dark 两套完整调色板**：`globals.css` 给出两套完整中性极简 OKLCH 值，采用**标准 shadcn 约定**：light 集在 `:root`（MVP 默认，`<html>` 不挂 `.dark` 即渲染浅色），dark 集在 `.dark`。C 只加切换机制（偏好 + 持久化 + FOUC boot 挂/摘 `.dark`），**无值搬移、无约定翻转**。
- **默认外观**：MVP 默认为**浅色**（由 `:root` 承载，用户决定），**本 change 不引入切换入口**（留给 C）。
- **形状与排版节奏**：重校 `--radius`、边框/分隔的对比与权重、表面层级（card/popover/muted/accent 的相对明度阶）；如引入品牌字体（Geist/Inter）**必须自托管**（`font-src 'self'` 已覆盖，B 不动 CSP），design 决定引入与否或保持 system-ui。所有 CSP 改动留在 C。
- **逐面回归**：在三栏外壳、TaskDetail 对话流、Artifact 预览、CostDashboard、登录页上核对新视觉。

**非目标（明确排除）**：不改三栏外壳结构、对话回合模型、`data-testid`、契约测试语义（纯视觉）；不引入主题切换/持久化/FOUC 处理（留给 C）；不迁移构建链或 token 格式（A 已完成）。

## Capabilities

### New Capabilities
<!-- 无独立新能力；B 在既有 web-design-system 能力下 ADD 一条"值层"要求。 -->

### Modified Capabilities
- `web-design-system`: **ADD** 一条新要求 `Neutral-Minimal Visual Identity`（约束 token 的**值**：中性极简两套完整调色板、`--primary` 近单色、`--ring` 焦点可见、可访问性、纯值层不动结构）。**刻意不 MODIFY** A 拥有的 `CSS-Variable Theme Tokens`（结构/格式/必备 token 列表归 A），以解耦 A/B 的 delta、保证 A→B archive 幂等。必备 token 列表的修正由 A 完成，B 只改值、不改列表。

## Impact

- **样式**：`web/src/styles/globals.css`（`:root`/`.dark` 的 OKLCH 值、`--radius`、可能的 `--font-*`）。
- **可能的依赖**：若引入品牌字体（Geist/Inter）则新增字体资源/包；否则无依赖变更。
- **组件**：不改结构；如个别 surface 的视觉权重需微调，仅调既有 token 类（不动 testid）。
- **规格/文档**：`openspec/specs/web-design-system/spec.md`（值层 delta）、必要时 `web/AGENTS.md` 的视觉基调描述。
- **依赖关系**：依赖 A（`migrate-design-system-base`）先落地（v4 + OKLCH + `@theme`）。
- **测试**：契约测试语义不变；验收以人工视觉评审 + `typecheck && lint && test && build` 全绿为准。
