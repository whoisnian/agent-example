## 1. 视觉规格（先 mock 后改值）

- [ ] 1.1 产出中性极简调色板 mock：dark + light 两套完整 OKLCH token 表（含 surface 层级明度阶）
- [ ] 1.2 定 `--primary` 近单色方案；显式定 `--ring` 保留可见强调 hue（即便 primary 单色），决定链接是否同用该 hue —— D2 对比 mock
- [ ] 1.3 定 `--radius` 与边框/分隔权重；定字体策略（system-ui 维持 vs 引入**自托管** Geist/Inter，不碰 CSP）—— D4
- [ ] 1.4 语义色（destructive/success/warning）去饱和校准，两套主题各自取值
- [ ] 1.5 视觉规格评审通过（关键 surface 截图级走查）后再进 §2

## 2. 落地 token 值（纯值层，零结构改动）

- [ ] 2.1 `globals.css` `:root`：写入完整 **dark** 中性极简 OKLCH 值（承载 MVP 默认；保持现 `:root`-承载默认惯例不变）
- [ ] 2.2 `globals.css`：把完整 **light** 中性极简 OKLCH 值写入 **`.light` 块（authored-inactive）**，不挂 class、不动 `index.html`、不翻转 `:root`/`.dark` 语义（翻转归 C）
- [ ] 2.3 更新 `--radius`；如引入品牌字体则配置 `--font-*` 并**自托管**字体资源（`font-src 'self'` 已覆盖，不改 CSP）
- [ ] 2.4 确认组件代码零结构改动；个别 surface 视觉权重微调只换既有 token 类，不动 `data-testid`

## 3. 回归与可访问性

- [ ] 3.1 逐面视觉走查（默认 dark）：LoginPage / 三栏外壳 / TaskList / TaskDetail 对话流 / Artifact 预览 / CostDashboard
- [ ] 3.2 **dev-only light 渲染走查**：临时强挂 `.light`（或一次性 dev flag）截图走查关键 surface，仅供评审 light 集正确性（catch 复制粘贴错误，如近白前景@近白背景）；**不发布切换入口**，C 负责首次真实 light 渲染
- [ ] 3.3 两套调色板文本/状态色 WCAG AA 对比校验
- [ ] 3.4 确认近单色 primary 下主操作仍醒目（对比/边框/focus ring/位置）；`--ring` 焦点态在两套主题键盘可达可见

## 4. 规格与文档

- [ ] 4.1 确认 `openspec/specs/web-design-system` 值层 delta 与落地一致
- [ ] 4.2 如基调描述需要，更新 `web/AGENTS.md` 的视觉语言说明（不改结构约定）

## 5. 验收

- [ ] 5.1 `npm run typecheck && npm run lint && npm run test && npm run build` 全绿（契约测试不变）
- [ ] 5.2 视觉评审签收：中性极简身份落地、两套调色板完整、无结构/testid 变更
