## 1. TaskDetail 对话流（PR1）

- [x] 1.1 `features/ui/store.ts` 新增 `selectedArtifactId: string | null` 与 setter，并实现一致性不变式：单独 set `selectedVersionId` 且值变化时同步清空 `selectedArtifactId`，另提供成对 setter（卡片点击路径原子写入两者）；离开详情页随既有清理逻辑清空；store 单测覆盖不变式（含 current 移动后悬挂选中被清空）
- [x] 1.2 `features/tasks/queries.ts` + `api.ts`：`useVersionQuery` 补 `meta:{silent:true}` + 404 跳过 retry（对照 `useTaskQuery` 写法），`getVersion` 传 `toastOnError:false`（现状零消费方，无回归面）
- [x] 1.3 新建回合组件 `components/tasks/ConversationTurn.tsx`：prompt 用户消息位（`useVersionQuery` 懒取，skeleton / 静默降级为版本号）+ 结果行（`v{n}` + StatusBadge + 成本摘要 + current 标记）+ 分叉 "from v{n}" 标注（`parent_id` 非前一版本时）（testid：`conversation-turn`、`turn-prompt`、`turn-origin`）
- [x] 1.4 回合内联产物列表：`useVersionArtifactsQuery` 按回合懒取；空列表整段省略，quiet loading / 静默 inline 错误；条目显示 kind/mime/size + Download（复用 presign 直下、每次重 mint、单错误面）；点击条目经成对 setter 写入 `selectedVersionId` + `selectedArtifactId` 并展开右栏（testid：`turn-artifact-item`、`turn-artifact-download`）
- [x] 1.5 回合尾部回滚：非当前回合挂 `RollbackControl`（branch/switch，活跃禁用 + `is_active` 禁 switch + 409 处理原样迁移）；**`task.current_version` 对应的回合**内联 EventLog（严格锚定 current，非"活跃/最新"；switch 后位于对话中段属预期；live/轮询管线不动）
- [x] 1.6 `TaskDetail.tsx` 重排为 `flex h-full flex-col`：紧凑头部（标题/StatusBadge/类型/CostBadge/ControlBar + 紧凑 Cost 条——**完整 TokenBar 字段集与 testid 保留**，仅压缩排版，避免触碰未 MODIFY 的 Cost Panel requirement）→ `flex-1 overflow-y-auto` 回合流（升序 `version_no`）→ 底部常驻 composer；近底部时自动滚底、上滚回看不抢占；确认 `<main>`/适读容器 `h-full` 链路（若 shell-layout 未先行则一并调整 `root-layout.tsx`）
- [x] 1.7 composer：textarea + 提交常驻，活跃禁用 + `title` 原因，提交中禁用，成功清空、失败保留输入，409 处理复用现有 mutation；保留 `iterate-prompt`/`iterate-submit`，退役 `iterate-button`
- [x] 1.8 退役 `components/tasks/VersionTree.tsx`；`VersionTree.test.tsx` 改写为回合断言（线性顺序、分叉标注、current 标记、产物激活驱动 store、回滚禁用矩阵），`TaskDetail.test.tsx` 改写（composer 常驻/禁用/清空/409 保留输入、回合流结构、prompt 静默降级）；行为断言意图保持
- [x] 1.9 `npm run typecheck && npm run lint && npm run test` 全绿（PR1 边界）

## 2. 预览面板头部工具栏（PR2）

- [x] 2.1 `ArtifactPreviewPanel` 选中态接 store `selectedArtifactId`（删除面板私有选中 state）；面板行点击写同一 store 字段；选中产物不在当前版本列表时按未选中渲染（兜底，不报错）
- [x] 2.2 `PreviewColumn.tsx` 去除自有头部行，保留抽屉/遮罩/重开骨架；面板首行渲染工具栏并**提升到所有早退分支之上**（no-version / loading / error / empty 全状态渲染：标题回退 + Copy/Refresh 禁用 + 关闭可用）：选中产物 `kind` · mime 标签 + Copy / Refresh / 关闭（关闭走 `togglePreview`，迁移保留 `preview-close` testid）；`PreviewColumn.test.tsx` 的 close 断言随迁到面板测试
- [x] 2.3 Copy：`navigator.clipboard.writeText` 写入已加载文本（含 HTML 源码视图），帽内完整可用 + success toast；截断/非文本/源码未加载/clipboard 不可用时禁用并给原因（testid：`preview-copy`）
- [x] 2.4 Refresh：对选中产物重新 presign 并重放预览（iframe 重载 / 文本重 fetch / 图片重载）（testid：`preview-refresh`）

## 3. HTML 富渲染 + CSP（PR2）

- [x] 3.1 `text/html` 产物默认渲染视图：选中即 presign、成功后立刻挂 `<iframe src={presignedUrl} sandbox="allow-scripts">`（绝不加 `allow-same-origin`），整高填充；**presign 失败**单条 inline 错误 + Refresh，不 toast（testid：`preview-html-frame`、`preview-presign-error`）；frame 内 HTTP/网络失败不做检测（跨域沙箱无可靠信号），Refresh 为恢复手段
- [x] 3.2 渲染 / 源码切换：源码视图复用既有 text 截断预览路径（含 CORS 降级），切换不重新选中产物（testid：`preview-view-toggle`）
- [x] 3.3 `web/index.html` CSP 增加 `frame-src https:`；核对 `script-src 'self'` / `object-src 'none'` / `frame-ancestors 'none'` / `base-uri 'self'` 不变
- [x] 3.4 `ArtifactPreviewPanel.test.tsx` 新增：store 共享选中态 + 悬挂选中兜底、工具栏全状态渲染与标题回退、close 迁移断言（接替 `PreviewColumn.test.tsx`）、Copy 可用/禁用矩阵、Refresh 重 mint 重挂、HTML 默认 iframe（断言 sandbox 不含 `allow-same-origin`）、渲染/源码切换、presign 失败 inline 错误；既有列表/下载/文本/图片断言零改动通过
- [x] 3.5 `npm run typecheck && npm run lint && npm run test` 全绿（PR2 边界）

## 4. 验证与文档

- [ ] 4.1 手动验证：真实 presigned URL 下 HTML iframe 渲染无 CSP 违规、过期 URL 走 Refresh 恢复；记录上轮遗留的 OSS CORS 验证结论（影响源码视图降级路径）
- [x] 4.2 更新 `docs/ARCHITECTURE.md`：§3.1 前端模块（目录树 `VersionTree/ # 版本树可视化（react-flow）` 改为回合组件、关键设计"版本树/虚拟滚动/节点 hover 成本/行选中驱动右栏"等条目改为对话回合流 + 产物卡片驱动 + 工具栏/HTML 富渲染 + CSP 口径）与 §2.2 组件职责表（Web Client"版本树展示"字样）；注意不是 §4.3（任务状态机）；`web/AGENTS.md` 如涉及同步修订
