## Context

A（`migrate-design-system-base`）落地后，基座为 Tailwind v4 + CSS-first `@theme` + OKLCH token + `darkMode:["class"]`。当前视觉身份（A 等价保留的）是 **Linear 风深色靛蓝**：深 navy 背景、靛蓝 primary（≈`#6366f1`）、`--radius: 0.5rem`、system-ui 字体、dark-only。

B 把这套身份重做成 **Vercel/Geist 风中性极简**，且**同时产出 light + dark 两套完整调色板**（C 负责切换机制）。B 是纯视觉层：只改 token 值与排版/圆角节奏，外壳/对话流/`data-testid`/契约测试不动（由 `web/AGENTS.md` 锁定）。

## Goals / Non-Goals

**Goals:**
- 用中性灰阶 OKLCH 调色板替换靛蓝-navy 身份（light + dark 两套完整值）。
- `--primary` 改为近单色高对比；强调色仅留在链接/焦点/语义态。
- 重校 `--radius`、边框/分隔权重、表面层级（card/popover/muted/accent 明度阶）。
- 决定字体策略（保持 system-ui vs 引入 Geist/Inter）。
- 全部关键 surface 视觉评审通过，门槛全绿。

**Non-Goals:**
- 主题切换 UI / 持久化 / FOUC 处理（C）。
- 外壳结构、对话回合模型、`data-testid`、契约测试语义。
- 构建链 / token 格式 / 基座机制（A 已完成）。

## Decisions

### D1 · 双调色板，默认浅色，标准 shadcn 约定（用户决定）
B 一次产出 light + dark 两套完整中性极简 OKLCH 值，采用**标准 shadcn 约定**：**light 集在 `:root`（MVP 默认）**，dark 集在 `.dark`。`<html>` 不挂 `.dark` 即渲染浅色，无 `index.html` 结构改动。
- **决策来源**：用户在 B 视觉评审环节明确要默认浅色（"default light"）。这把"约定标准化"从 C 提前到 B——但代价极小（只是把 light 值放进 `:root`、dark 放进 `.dark`，仍是纯值/选择器变化，无组件/结构/testid 改动），且让 C 大幅简化。
- **default-theme 过渡（A→B→C 一处说清）**：
  - **A**：`:root`=dark（v3→v4 等价迁移的过渡态），`.dark` 镜像；默认深色。
  - **B**：翻到标准 `:root`=light（默认浅色）/ `.dark`=dark；纯值 + 选择器归位，无结构改动。
  - **C**：约定已标准，**无值搬移**；只加偏好 + 持久化 + FOUC-safe boot（按偏好挂/摘 `.dark`），并 MODIFY `CSS-Variable Theme Tokens` 的"默认主题"场景为"偏好驱动"。
- **A 的归档场景仍满足**：A 的"Default theme resolves without a class toggle"说"默认由 `:root` 承载、无需 `.dark`"——B 把 `:root` 改成 light 后该陈述仍成立（默认=`:root`=light，无 `.dark`），故无需回改已归档的 A。

### D2 · `--primary` 近单色高对比
中性极简里 primary 不再是品牌色：light 主题近黑、dark 主题近白（高对比中性）。primary 按钮变单色调；强调色（如果保留一个 accent 蓝）仅用于链接/焦点环/语义。
- **承认的视觉影响**：每个 primary CTA 的"感觉"都会变（不再靛蓝）。这是中性极简方向的预期结果，已在 explore 阶段对齐。
- **`--ring` 必须显式决策（不可让其静默跟随单色 primary）**：现网 `--ring` == `--primary`（`globals.css:36`）。primary 转单色后若 `--ring` 仍跟随，会得到"近黑环@深色 / 近白环@浅色"的低对比焦点态——键盘可达性回归。**决策**：`--ring` 保留一个可见强调 hue（即便 primary 单色），两套主题各自取值并过焦点对比。这也回答了"是否保留一个 accent hue"——至少焦点环保留。
- **Open**：链接是否也用该 accent hue，还是仅焦点环用——落地前出对比 mock 定。

### D3 · 表面层级与边框
重校 `card`/`popover`/`muted`/`accent`/`secondary` 的相对明度，使深色下层级靠"微妙明度阶 + 极细边框"而非大色块；边框/分隔走低对比中性（Geist 风）。
- **理由**：中性极简的层级感来自留白与微对比，不是饱和色。

### D4 · 字体策略（落地前定）
两条路：保持现有 system-ui 栈（零依赖、零字体加载成本）；或引入 Geist/Inter 品牌字体（更贴 Vercel 观感，但加资源 + 字体加载策略）。
- **倾向**：先用 system-ui 出整体 mock，确认中性极简骨架后，再单独评估是否值得引入品牌字体。
- **CSP 边界（硬约束）**：若引入字体，**必须自托管**（打进构建产物，走 `font-src 'self'`，现有 CSP 已覆盖、B 不改 CSP）。**禁止**任何外部字体 CDN（会撞 CSP，且 CSP 编辑只允许发生在 C，避免两个 change 同时改 CSP）。如确需 CDN 字体，另起独立 change。

### D5 · 语义色去饱和校准
`destructive`/`success`/`warning` 保留功能性但按中性极简降饱和，保证在两套主题下可读且不抢戏。

### D6 · 交付物：先 mock 后改值
B 的工作以"调色板 + 圆角 + 排版"的视觉规格（mock/token 表）为先，评审通过后再落到 `globals.css`。组件代码原则上零改动（类名不变）；个别 surface 若视觉权重需微调，只换既有 token 类，绝不动 `data-testid`。

## Risks / Trade-offs

- **[两套调色板的对比度/可访问性]** → light 与 dark 都要过 WCAG AA 文本对比；关键文本/状态色人工校验。
- **[近单色 primary 削弱可发现性]** → 用对比、边框、focus ring 与位置保证主操作仍醒目；mock 评审确认。
- **[视觉评审主观、易反复]** → D6 先出 token 级 mock 定方向，减少在代码里反复试色。
- **[引入品牌字体的隐性成本]** → 若走 D4 字体路，需评估字体加载（FOUT/FOIT）与 CSP `font-src`；默认不引入以规避。
- **[与 A 的边界混淆]** → B 只改值不改机制；若发现需要改 `@theme` 结构/格式，说明 A 有遗漏，回 A 修，不在 B 夹带基座变更。

## Open Questions

- D2：primary 全单色 vs 保留单一强调 hue（链接/焦点）——出对比 mock 后定。
- D4：是否引入 Geist/Inter 品牌字体——出整体 mock 后定。
- `--radius` 目标值（更锐利 vs 维持 0.5rem）——随 mock 定。
