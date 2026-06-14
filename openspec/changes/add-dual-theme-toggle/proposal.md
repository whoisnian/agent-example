> **依赖顺序（硬）**：本 change MUST 在 A（`migrate-design-system-base`）与 B（`redesign-neutral-minimal`）之后 apply 与 archive。C 的 `web-design-system` MODIFIED 块在 A 迁移后的内容上翻转 `:root`/`.dark` 约定并改写"默认主题"场景，乱序 archive 会损坏 delta 基。

## Why

现代化三部曲第三步（C）。B 已产出 light + dark 两套完整中性极简调色板，但默认仍深色、无切换入口。C 引入**真正的主题切换能力**：用户可在 light / dark（含跟随系统）间切换，选择被持久化，且**首帧前**就应用正确主题以避免闪烁（FOUC）。这是行为层变更，独立于 A（基座）与 B（调色板值）。

## What Changes

- **主题状态与持久化**：新增主题偏好状态（`light` / `dark` / `system`），持久化到 `localStorage`；解析后把 `.dark` class 应用到 `<html>`（`system` 时读 `prefers-color-scheme` 并随其变化更新）。
- **FOUC-safe 启动**：在首帧渲染前确定并应用主题。**关键约束**：`index.html` 的 CSP 为 `script-src 'self'`，会阻断常规内联 `<script>`，需用 CSP 兼容方案（design 定：hash 放行内联 boot 脚本 / nonce / 外部模块）。
- **切换 UI**：在 SideNav 用户区 DropdownMenu 增加主题切换项（与既有 Tasks/Cost/Settings/Logout 同列；带稳定 `data-testid`）。
- **默认行为变更**：默认主题由"硬编码深色"改为"读取持久化偏好，缺省回退到 `system`（或 dark，design 定）"。

**非目标（明确排除）**：不改调色板的值（A/B 已定）；不改外壳结构/对话流；不引入与主题无关的设置项。

## Capabilities

### New Capabilities
- `web-theme-switching`: 主题偏好的存取与持久化、`<html>.dark` 的应用、跟随系统、首帧前 FOUC-safe 启动（CSP 兼容）、SideNav 切换 UI。

### Modified Capabilities
- `web-design-system`: **MODIFY** `CSS-Variable Theme Tokens` 仅两处——(1) `:root`/`.dark` 约定翻转到 shadcn 标准（`:root`=light、`.dark`=dark，把 B 的 authored-inactive `.light` 值提升进 `:root`）；(2) "Default theme resolves without a class toggle" 场景改写为"偏好驱动 + FOUC-safe boot"。这是默认主题行为翻转的**唯一落点**（A 定义该场景、B 不碰、C 改），避免三方争用同一 spec 块。结构/格式/token 列表沿用 A。

## Impact

- **新增**：主题状态模块（Zustand store 或专用 theme provider）+ 持久化逻辑；FOUC-safe boot（`index.html` 内联脚本或 `main.tsx` 早期模块）。
- **构建/安全**：`index.html` CSP `script-src` 可能需追加 inline 脚本的 sha256 hash（最小放行面）；若走 hash 方案，构建需产出/校验该 hash。
- **UI**：`web/src/components/layout/SideNav.tsx`（DropdownMenu 增主题切换项，新 `data-testid`）。
- **样式**：默认 `<html>` 是否预挂 class 的策略调整（配合 B 的双调色板）。
- **依赖关系**：依赖 A（v4 基座）与 B（双调色板）先落地。
- **测试**：新增主题切换/持久化/系统跟随的契约测试；SideNav 测试同步加切换项；CSP 调整需验证产物加载不被拦。
