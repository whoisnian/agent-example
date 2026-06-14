## 1. 视觉规格（先 mock 后改值）

- [x] 1.1 产出中性极简调色板：dark + light 两套完整 OKLCH token 表（基于 shadcn v4 neutral base，近零彩度灰阶 + surface 明度阶）
- [x] 1.2 `--primary` 近单色（dark 近白 / light 近黑）；`--ring` 保留蓝调强调 hue（dark `oklch(0.65 0.18 252)` / light `oklch(0.55 0.2 255)`，用户确认链接亦可用）—— D2
- [x] 1.3 `--radius` → `0.375rem`（用户定）；边框走低对比中性；字体保持 system-ui（用户定，不碰 CSP）—— D4
- [x] 1.4 语义色去饱和校准：destructive 共享 `0.577/0.245/27`（filled+text 两用均衡）；success 改 dark-fg（dark `0.7`/light `0.62`）；warning dark-fg
- [x] 1.5 视觉规格定稿（经 AskUserQuestion 三项决策）后进 §2

## 2. 落地 token 值（纯值层，零结构改动）

- [x] 2.1 `globals.css` `:root`：完整 dark 中性极简值（承载 MVP 默认；`:root`-承载默认惯例不变）
- [x] 2.2 `globals.css` `.light` 块：完整 light 中性极简值（authored-inactive；不挂 class、不动 `index.html`、不翻转语义）
- [x] 2.3 `--radius` → 0.375rem；字体保持 system-ui（无新增字体资源/CSP 改动）
- [x] 2.4 组件代码零结构改动；无 `data-testid` 改动（仅 `globals.css` token 值变化）

## 3. 回归与可访问性

- [x] 3.1 逐面视觉走查：用户 `npm run dev` 实测后决定**默认浅色**（"default light"）—— 视觉签收，约定改为 `:root`=light
- [x] 3.2 light 渲染：light 现为**实时默认**（`:root`），用户已直接看到，无需 dev-only `.light` 走查
- [x] 3.3 两套调色板 WCAG AA 对比校验（OKLCH→sRGB 计算）：文本/卡片/muted/primary/filled 徽章均 ≥4.5；ring 可见性 ≥3。唯一边际：dark `text-destructive`/bg = 4.15（AA large 过、normal text 近线，brightred-on-near-black 的固有取舍）
- [x] 3.4 `--primary` 近单色下主操作对比高（dark 近白按钮 14:1）；`--ring` 焦点态两套主题对比 6.09 / 4.90（≥3 可见）

## 4. 规格与文档

- [x] 4.1 `openspec/specs/web-design-system` 值层 delta（ADDED `Neutral-Minimal Visual Identity`）与落地一致
- [x] 4.2 `web/AGENTS.md` 增中性极简视觉语言简述（不改结构约定）

## 5. 验收

- [x] 5.1 `npm run typecheck && npm run lint && npm run test && npm run build` 全绿（契约测试 202 全过不变）
- [x] 5.2 视觉评审签收：用户确认中性极简身份 + 默认浅色；两套调色板完整、标准约定、无结构/testid 变更
