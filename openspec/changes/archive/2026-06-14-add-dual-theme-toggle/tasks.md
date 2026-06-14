## 1. 主题状态与持久化

- [x] 1.1 新增 `features/theme/store.ts`（Zustand）：状态 `light`/`dark`/`system`，`setTheme`，模块加载读初值（plain-string `theme` key）
- [x] 1.2 解析与应用：`resolveTheme` → 加/去 `<html>.dark`；`setTheme` 写 `localStorage`（与 boot 脚本同一 key/规则）
- [x] 1.3 `system` 跟随：`initThemeSystemSync` 订阅 `matchMedia` change（main.tsx 调一次），`syncResolved` 仅在 `system` 时更新
- [x] 1.4 兜底守卫：`localStorage` 与 `matchMedia` 访问全 try/catch + 存在性判断——缺失时安全回退、不抛、订阅 no-op

## 2. FOUC-safe boot（CSP 兼容）

- [x] 2.1 `index.html` `<head>` 加极小内联 boot 脚本：读 `localStorage`（缺省 `system` → 守卫的 `matchMedia`）→ 首帧前设 `<html>.dark`
- [x] 2.2 `vite.config.ts` 加 `themeCspHash` 插件：`transformIndexHtml(post)` 用**产物字节**算 sha256 注入 CSP `script-src`（替换 `THEME_BOOT_HASH` 占位；不引 `unsafe-inline`，保留 `object-src 'none'`）
- [x] 2.3 `closeBundle` 读 `dist/index.html` 重算 hash 并校验，分歧即 throw（已随 `npm run build` 验收，独立重算确认 `sha256-CAFJ…` 匹配）
- [x] 2.4 boot 与 store 同 key/规则；store 初始化不调 apply（不重复翻转 boot 已设的 class，防二次闪烁）

## 3. 主题约定确认（D5 — B 已标准化，C 无值搬移）

- [x] 3.1 B 已建立标准约定（`:root`=light / `.dark`=dark）；C 不动 token 值，仅 boot/store 按偏好挂/摘 `.dark`
- [x] 3.2 默认渲染：`.dark` 缺省 → `:root`(light)；存在 → `.dark`（store 测试覆盖 class 切换）

## 4. 切换 UI

- [x] 4.1 SideNav 用户区菜单增三个主题项，`data-testid` = `theme-option-{light,dark,system}`
- [x] 4.2 三个 `DropdownMenuItem` + `onSelect` 调 `e.preventDefault()` 保持菜单不关（无需 vendoring RadioGroup）
- [x] 4.3 选中态（`aria-checked` + 高亮 + Check）打在**偏好**项；`system` resolve 成 dark 时勾仍在 `system`；既有结构/testid 不变

## 5. 测试

- [x] 5.1 `src/test/setup` 增 `window.matchMedia` stub（no-op listeners，默认 not-dark）
- [x] 5.2 `store.test.ts`：持久化（reset modules 重载读 `dark`）、`system` 跟随（可控 mock 触发 change）、`localStorage`/`matchMedia` 不可用兜底（6 测试）
- [x] 5.3 SideNav 测试增 3 项：三选项存在、选中持久化+菜单不关、选中态反映偏好；既有 testid 不破
- [x] 5.4 boot hash 一致性：`closeBundle` 构建期校验 + 独立重算确认匹配
- [x] 5.5 既有全量契约测试在新默认下全绿：29 文件 / 211 测试（202 → +9 新增）

## 6. 验收

- [x] 6.1 `npm run typecheck && npm run lint && npm run test && npm run build` 全绿
- [x] 6.2 手动验证（用户 `npm run dev` 确认）：三态切换即时生效 + 持久化 + 无 FOUC + 系统跟随 + 生产 boot 不被 CSP 拦
