## Context

A 落地 v4 基座，B 落地 light + dark 两套完整中性极简调色板（默认深色、无切换）。`darkMode:["class"]` 已就位：切到 light 只需 `<html>` 不挂 `.dark`，切到 dark 挂 `.dark`。C 补"机制层"：偏好状态 + 持久化 + 系统跟随 + FOUC-safe 启动 + 切换 UI。

**核心约束（来自 `web/AGENTS.md` 与 `index.html`）**：CSP `script-src 'self'`、`object-src 'none'` 是安全红线（根 AGENTS §6）。常规"无闪烁"内联 boot 脚本会被 `script-src 'self'` 拦截。SideNav 结构（brand → New task → RecentTasks → 用户区 DropdownMenu 含 Tasks/Cost/Settings/Logout）由 AGENTS 锁定，切换项需融入用户区菜单且保持 `data-testid` 稳定。

## Goals / Non-Goals

**Goals:**
- 主题偏好 `light` / `dark` / `system`，持久化到 `localStorage`。
- `system` 跟随 `prefers-color-scheme` 实时变化。
- 首帧前应用正确主题，无可感知闪烁。
- 在严格 CSP 下实现，安全放行面最小。
- SideNav 用户区菜单提供切换入口，带稳定 `data-testid`。

**Non-Goals:**
- 改调色板值（A/B 已定）。
- 改外壳结构/对话流/其它设置项。
- 服务端持久化主题（仅本地）。

## Decisions

### D1 · FOUC-safe boot：CSP hash 放行内联脚本
在 `index.html` `<head>` 顶部放一段**极小内联脚本**：读 `localStorage` 主题偏好（缺省 `system` → 读 `matchMedia('(prefers-color-scheme: dark)')`），据此在 `<html>` 上加/去 `.dark`，在首帧前完成。CSP 通过追加该脚本的 `'sha256-...'` 到 `script-src` 放行。
- **理由**：唯一能"首帧前"运行的位置就是 `<head>` 内联脚本；hash 放行是最小授权（只放行这段确切字节），不引入 `unsafe-inline`，守住红线。
- **Alternatives（弃）**：
  - **nonce**：SPA 静态 `index.html` 无服务端注入 nonce 的天然位置，构建期 nonce 等价于 hash 但更繁。
  - **外部模块（`main.tsx` 早期）**：脚本作为模块异步加载，无法保证首帧前执行 → 会有一帧闪烁。
  - **接受一帧闪烁**：UX 差，违背 goal。
- **构建注意（hash 必须基于产物字节）**：Vite 会对 `index.html` 做 transform/压缩，浏览器实际执行的是 `dist/index.html` 里的字节。因此 CSP 的 `'sha256-...'` 必须基于**构建产物** `dist/index.html` 的内联脚本字节，而非源文件——否则 dev 正常、生产被静默拦截（经典 CSP-hash footgun）。校验作为 `npm run build` 验收的一部分跑（对比产物脚本 hash 与 CSP 声明），不止源码单测。优先用一个在 transform 后注入/校验 hash 的 Vite 插件。

### D2 · 偏好状态：复用 Zustand，单一 source of truth
主题偏好放 Zustand（与既有 `features/ui/store` 风格一致，或独立 `features/theme/store`）。store 负责：读初值（与 D1 内联脚本读同一 `localStorage` key，保持一致）、`setTheme`、`system` 时订阅 `matchMedia` change。store 变化时同步：写 `localStorage` + 应用 `<html>.dark`。
- **理由**：服务端状态走 React Query、本地 UI 状态走 Zustand 是仓库铁律；主题是纯本地 UI 状态。
- **一致性不变式**：内联 boot 脚本与 store 用**同一个 localStorage key 与同一套取值/解析规则**，避免 boot 与 hydrate 后判定不一致导致二次闪烁。

### D3 · 切换 UI：SideNav 用户区菜单项
在用户区 DropdownMenu 增主题切换，`data-testid`（`theme-option-{light,dark,system}`）稳定。融入既有菜单，不新增第四栏或独立按钮。
- **理由**：AGENTS 已把全局动作收在用户区菜单；主题属全局偏好，归此处最自然。
- **Radix 交互（必须明确，不可含糊）**：现 SideNav 用 `DropdownMenuItem` + `onSelect`，默认**选中即关菜单**（`SideNav.tsx:124-139`）。两条可行实现：
  - **推荐**：三个显式 `DropdownMenuItem`（light/dark/system），`onSelect` 调 `e.preventDefault()` 阻止关闭，让用户连续试主题；选中态打勾在偏好项上。
  - 或 `DropdownMenuRadioGroup`/`DropdownMenuRadioItem`——但这些**当前未 vendored**，需按需 vendoring 进 `components/ui/` 并在 Impact 记一笔（动 `components/ui/` 受 `web/AGENTS.md` 按需添加约束）。
- **选中态语义**：勾选反映**偏好**（light/dark/system），不是 resolved 主题——`system` 即便 resolve 成 dark，勾也在 `system` 上（见 spec scenario）。

### D5 · `:root`/`.dark` 约定已由 B 标准化，C 无值搬移
B（用户决定默认浅色）已采用标准 shadcn 约定：`:root`=light、`.dark`=dark。因此 C **不做任何值搬移或约定翻转**——只加偏好 + 持久化 + FOUC boot 来按偏好挂/摘 `.dark`。C 对 `web-design-system` 的 MODIFIED delta 仅把"默认主题"场景从"`:root` 静态默认"改为"偏好驱动"。
- **理由**：约定已是标准，C 聚焦纯机制层；默认主题行为的改写仍由 C 这一个 change 承担（A 定义场景、B 不碰、C 改写），无三方争用。

### D4 · 默认回退：`system`
无持久化偏好时缺省 `system`（跟随操作系统），而非硬编码 dark。
- **理由**：双主题已就绪，跟随系统是现代默认、体验最佳。MVP 历史默认深色仍可通过"OS 为深色"或显式选 dark 达到。
- **Alternative**：缺省 dark（保留 MVP 观感）——design 倾向 `system`，apply 时可一行切换，影响面小。

## Risks / Trade-offs

- **[CSP hash 漂移]**（脚本改了忘更新 hash → 脚本被拦 → 主题不应用）→ 校验必须基于**构建产物** `dist/index.html` 字节（非源文件），作为 `npm run build` 验收的一部分，不一致即失败；优先 transform 后注入 hash 的 Vite 插件。
- **[jsdom 无 `matchMedia` → 整套测试崩]**（默认翻 `system` 后，store 的 `matchMedia` 访问在 jsdom 下 `undefined`，会让**所有**契约测试 import 即抛，而非仅新测试）→ store 守卫 `matchMedia` 存在性 + `src/test/setup` 增 stub；专门断言既有全量契约测试在新默认下仍全绿。
- **[boot 脚本与 store 判定不一致致二次闪烁]** → D2 不变式：共享 key 与解析规则；store 初始化时不重复"翻转"已由 boot 设好的 class。
- **[`localStorage` 不可用/隐私模式]** → try/catch 兜底，退回 `system`，切换在会话内仍生效（仅不持久化）。
- **[SSR/预渲染]** → 本项目是 Vite SPA，无 SSR；boot 脚本在浏览器执行，无 hydration mismatch 顾虑。
- **[与 B 默认外观的衔接]** → B 默认深色靠"承载默认的 token 集 + 不挂 `.dark`"；C 落地后默认由偏好决定，需确认 B 的 `:root`(light)/`.dark`(dark) 映射与 D1 的 class 应用方向一致（挂 `.dark` = 深色）。

## Open Questions

- D4：缺省 `system` vs `dark` —— apply 时最终拍板（design 倾向 `system`）。
- D3：切换交互形态（三态循环按钮 vs 子菜单）—— 落地时按菜单观感定，不影响能力契约。
- CSP hash 的产出方式（手写并加校验 vs 构建插件自动注入）—— apply 时按最简可靠方案定。
