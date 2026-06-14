## 1. 主题状态与持久化

- [ ] 1.1 新增主题 store（Zustand，`features/theme/store` 或并入既有 ui store）：状态 `light`/`dark`/`system`，`setTheme`，读初值
- [ ] 1.2 解析与应用：resolved theme → 加/去 `<html>.dark`；写 `localStorage`（与 boot 脚本同一 key）
- [ ] 1.3 `system` 跟随：订阅 `matchMedia('(prefers-color-scheme: dark)')` change，实时更新
- [ ] 1.4 兜底守卫：`localStorage` **与** `window.matchMedia` 访问都包 try/catch / 存在性判断——两者缺失（隐私模式 / jsdom 无 `matchMedia`）时安全回退、不抛、`system` 订阅 no-op，保证既有契约测试不因 import 即崩

## 2. FOUC-safe boot（CSP 兼容）

- [ ] 2.1 `index.html` `<head>` 顶部加极小内联 boot 脚本：读 `localStorage`（缺省 `system` → `matchMedia`，并守卫 `matchMedia` 缺失）→ 首帧前设 `<html>.dark`
- [ ] 2.2 把基于**构建产物** `dist/index.html` 内联脚本字节的 sha256 写入 CSP `script-src 'self' 'sha256-...'`（不引入 `unsafe-inline`；保留 `object-src 'none'` 等红线）；优先用在 transform 后注入/校验 hash 的 Vite 插件
- [ ] 2.3 hash 一致性校验跑在 `npm run build` 验收：对比 `dist/index.html` 实际脚本 hash 与 CSP 声明，不一致即失败（防 dev-正常/生产-被拦的 footgun）
- [ ] 2.4 确认 boot 与 store 用同一 key/规则；store 初始化不重复翻转已设 class（防二次闪烁）

## 3. `:root`/`.dark` 约定翻转（D5）

- [ ] 3.1 把 B authored-inactive 的 `.light` 值搬进 `:root`（light），dark 值搬进 `.dark`（标准 shadcn 约定）；`<html>` 按偏好挂/去 `.dark`
- [ ] 3.2 确认翻转后默认渲染正确：`.dark` 缺省时 `:root`(light) 生效、存在时 `.dark` 生效

## 4. 切换 UI

- [ ] 4.1 SideNav 用户区 DropdownMenu 增三个主题项，`data-testid` = `theme-option-{light,dark,system}`，稳定
- [ ] 4.2 Radix 交互：推荐三个 `DropdownMenuItem` + `onSelect` 调 `e.preventDefault()` 保持菜单不关；若改用 `DropdownMenuRadioGroup` 须按需 vendoring 进 `components/ui/` 并在 proposal Impact 记一笔
- [ ] 4.3 选中态打勾在**偏好**项（`system` resolve 成 dark 时勾仍在 `system`）；不改 SideNav 既有结构与 testid

## 5. 测试

- [ ] 5.1 `src/test/setup` 增 `window.matchMedia` stub（jsdom 无此 API，否则 DropdownMenu 组件测试与 store init 报错）
- [ ] 5.2 契约测试：偏好持久化（reload 后保持）、`system` 跟随系统（用 stub 触发 change）、`localStorage`/`matchMedia` 不可用兜底
- [ ] 5.3 SideNav 测试同步新增切换项；选中态反映偏好；既有 testid 不破
- [ ] 5.4 boot hash 一致性校验（对产物）纳入测试/构建
- [ ] 5.5 确认**既有全量契约测试**在新默认（`system`）下仍全绿（不只新增测试）

## 6. 验收

- [ ] 6.1 `npm run typecheck && npm run lint && npm run test && npm run build` 全绿
- [ ] 6.2 手动验证：三态切换即时生效 + 持久化 + 无 FOUC + 系统跟随 + 生产产物 boot 不被 CSP 拦
