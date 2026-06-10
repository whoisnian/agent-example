# Suggestions — refactor-web-conversation-rich-preview

> 评审基线：proposal.md / design.md / specs/{web-tasks-pages,web-artifact-preview}/spec.md / tasks.md，对照基线 specs（web-tasks-pages、web-artifact-preview、web-artifacts-views）与 `web/src` 现行代码逐项核实。`openspec validate` 已通过（MODIFIED/REMOVED 头、requirement 名称匹配、scenario 层级、REMOVED 的 Reason+Migration 均无问题）。

## S1: 沙箱 iframe 的「加载失败」在浏览器层面不可靠检测，scenario 按现状不可实现（高）

- **目标**：web-artifact-preview delta "Iframe load failure degrades to inline error with Refresh" scenario、design D6「失效处理」、tasks 3.1（`preview-html-error`）
- **问题**：spec 要求 "**WHEN** the rendered HTML iframe fails to load (e.g. the presigned URL expired) **THEN** the panel MUST show a single inline preview error"。但跨域 iframe 没有可用的失败信号：
  - HTTP 级错误（过期 presigned URL 返回的 OSS 403 XML）会作为**正常文档**渲染进 iframe 并触发 `load` 事件，`error` 事件不会触发；
  - 网络级失败浏览器同样多以错误页 + `load` 收场；
  - sandbox 无 `allow-same-origin`（opaque origin）+ 跨域，宿主无法读 `contentDocument` 来探测内容是不是错误页；
  - 用 `fetch` 预检又会撞上 OSS CORS（上轮遗留未验证，正是 D6 选 `src` 而非 `srcdoc` 的理由）。
  即 tasks 3.1 的 `preview-html-error` 在「URL 过期」这个主场景下既写不出实现也写不出可靠断言。
- **建议**：把可检测与不可检测的失败分开重写该 scenario：
  1. **presign 阶段失败**（mutation onError，可靠检测）→ inline 错误 + Refresh；
  2. **iframe 内的 HTTP/网络失败** → 接受「OSS 错误文档直接显示在 frame 里」为 MVP 降级形态，工具栏 Refresh 常驻（已是 D5 设计）作为恢复手段，spec 不再断言「MUST show inline error」。
  design D6 与 tasks 3.1/3.4 同步修订（删除或改写 `preview-html-error` 断言）。

## S2: 回合 prompt「静默降级、不 toast」按现有 `useVersionQuery`/`getVersion` 不可实现（高）

- **目标**：web-tasks-pages delta "Prompt read failure degrades silently" scenario、design D2、proposal「数据访问零新增」/ design Non-Goal「不改任何 API / 数据访问层」
- **问题**：scenario 要求 prompt 读取失败 "**without any toast** and without blocking other turns"。实际：
  - `web/src/features/tasks/api.ts:43` 的 `getVersion()` 未传 `toastOnError:false`，而 `services/http.ts:131` 默认 toast —— 失败一次弹一次（默认 retry 2 次 → 最多 3 个 transport toast）；
  - `web/src/features/tasks/queries.ts:59-65` 的 `useVersionQuery` 没有 `meta:{silent:true}`，`services/query-client.ts` 的 `queryCache.onError` 还会再补一个；它也没有 404 跳过 retry 的守卫（对照同文件 `useTaskQuery` 的写法）。
  满足 scenario 必须改 `queries.ts` + `api.ts`，与「数据访问零新增 / 不改数据访问层」的声明冲突。
- **建议**：给 `useVersionQuery` 加 `meta:{silent:true}` + 404 不重试，`getVersion` 传 `toastOnError:false`（该 hook 目前**零消费方**，改动无回归面）；把这两个文件补进 proposal Impact，Non-Goal 措辞改为「不改数据访问契约/transport，仅调整静默与重试姿态」。同理可顺带核对 `useVersionArtifactsQuery`——这个已是双层静默（`queries.ts:31-34` + `api.ts` 的 `toastOnError:false`），回合产物列表无此问题。

## S3: `PreviewColumn.test.tsx` 必坏但未列入 Impact；工具栏须在面板所有早退状态下渲染（中）

- **目标**：proposal Impact（测试清单）、design D5、tasks 2.2 / 3.4
- **问题**：两点。
  1. D5 把头部（含 `preview-close`）从 `PreviewColumn` 迁入 `ArtifactPreviewPanel`，但 `web/src/components/layout/PreviewColumn.test.tsx:26-40` 以**纯 div children** 渲染 `PreviewColumn` 并断言 `preview-close` —— 头部移除后该用例直接失败。proposal Impact 的测试清单（TaskDetail/VersionTree/RollbackControl/ArtifactPreviewPanel）漏了它。
  2. 现行 `ArtifactPreviewPanel.tsx` 在 `preview-no-version`（46-55 行）、loading、error、empty 各分支**早退**，不渲染任何头部。`ArtifactPreviewPanel` 挂在 `RootLayout` 全局（/tasks、/cost 页也在）；若工具栏只在有产物时渲染，非详情页的抽屉将没有关闭按钮。delta 已写 "header toolbar as its first row"，但实现侧要明确：工具栏必须提升到所有早退分支之上。
- **建议**：Impact 与 tasks 3.4 补上 `PreviewColumn.test.tsx`（close 断言迁移到面板测试）；tasks 2.2 明确「工具栏在 no-version/loading/error/empty 全状态渲染（标题回退 + Copy/Refresh 禁用 + 关闭可用）」，并补一条对应断言。

## S4: `selectedVersionId` 与 `selectedArtifactId` 的一致性不变式未定义，live 刷新会产生「跨版本悬挂选中」（中）

- **目标**：web-artifact-preview delta "Artifact Preview Panel"、design D3、tasks 1.1
- **问题**：`web/src/routes/TaskDetail.tsx:52-55` 的 effect 在 `currentVersionId` 变化时**无条件覆写** `selectedVersionId`（iterate/rollback 成功后 live/refetch 会触发）。引入 `selectedArtifactId` 后会出现：用户点了旧回合的产物（store = {旧版本, 产物A}）→ 任务跑完 current 移动 → effect 把 `selectedVersionId` 覆写为新 current，而 `selectedArtifactId` 仍指向旧版本的产物 A。此时 delta 的 "When the store carries a selected artifact, the panel MUST preview **that exact artifact**" 无法满足（A 不在新版本的列表里），spec 对这种组合也没有定义行为。
- **建议**：在 store 层定义不变式并写进 delta：`setSelectedVersionId(v)` 在 v 变化时同步清空 `selectedArtifactId`（除非通过成对 setter 一次性写入，即 D3 的卡片点击路径）；面板侧对「selected artifact 不在当前列表」按未选中处理（仅列表态）。tasks 1.1 的 setter 设计与 store 单测据此细化。

## S5: 「Cost 面板折叠为紧凑条」与未修改的 "Task Detail Cost Panel With Token Breakdown" requirement 有张力（中）

- **目标**：design D4「紧凑头部（…Cost 面板折叠为紧凑条）」 vs 基线 web-tasks-pages "Task Detail Cost Panel With Token Breakdown"（本变更未 MODIFY）
- **问题**：基线 requirement 要求详情页 cost 面板展示**完整分解**：amount + `input`/`output`/`cached` token 数 + `tool_calls` + `wall_time_ms`（来源 `GET /tasks/{id}/cost`），且 "Inline badge and panel coexist" scenario 要求 CostBadge 与面板并存。delta 的 "Task Detail Page" 只写了 "cost summary" 在紧凑头部；若「紧凑条」砍掉 token 分解字段，就违反了这条未动的 requirement —— 但变更没有提交它的 MODIFIED delta。
- **建议**：二选一并落实：(a) 紧凑条仍渲染完整 `TokenBar` 字段集（可横排压缩），design D4 明确写出「字段不减」；(b) 若确要精简（如折叠/popover），给 "Task Detail Cost Panel With Token Breakdown" 提交 MODIFIED delta。tasks 1.5 相应点名。

## S6: 「当前活跃/最新版本的回合」措辞与 spec 的 "current version's turn" 不一致（低）

- **目标**：design D2 第 4 点 / D4、proposal What Changes vs web-tasks-pages delta "Task Detail Page"
- **问题**：design 多处写「当前**活跃/最新**版本的回合内联 EventLog」，但 delta spec 写的是 "inline within the **current version's** turn only"。switch 回滚后 `current_version` 可指向较早的 `version_no`（current ≠ 最新）；而现有事件管线（`TaskDetail.tsx:60` 的 `useVersionEventsQuery(currentVersionId)`、`use-task-live.ts` 订阅 `version:<current>`）严格锚定 `current_version`。「活跃/最新」的说法会误导实现者去找活跃版本或最大 version_no。
- **建议**：design D2/D4 与 proposal 统一改为「`task.current_version` 对应的回合」（与 spec 及既有管线一致）；顺带注明 switch 后 EventLog 会出现在对话中段而非底部，属预期。

## S7: 文档同步范围不足：版本树残留不止 §4.3，且 §4.3 指错章节（低）

- **目标**：tasks 4.2
- **问题**：`docs/ARCHITECTURE.md` 的 §4.3 是「任务状态机」，前端描述在 **§3.1**。且版本树退役后需要清理的不止外壳一句：§2.2 组件表 "Web Client｜…版本树展示…"（L105）、§3.1 目录树 `VersionTree/ # 版本树可视化（react-flow）`（L129）、§3.1 关键设计「**版本树**：…客户端虚拟滚动加载」（L152）与「VersionTree 每个节点 hover 时显示成本」（成本展示要点）、「VersionTree 由行内展开改为行选中驱动右栏预览」。AGENTS.md 规定 ARCHITECTURE.md 是唯一架构事实来源，留下 react-flow/版本树字样会构成文档-实现冲突。
- **建议**：tasks 4.2 改为点名 §2.2 + §3.1 的上述位置（对话回合流 + 产物卡片驱动右栏 + 工具栏/HTML 富渲染 + CSP 口径），不要只写 §4.3。

## S8: 两处低风险备忘 —— requirement 旧名失义、CSP meta 中 `frame-ancestors` 本就无效（低）

- **目标**：web-tasks-pages delta "Version Artifacts Expandable List With Direct Download"、web-artifact-preview delta "Content Security Policy for OSS Preview"
- **问题与建议**：
  1. 该 requirement 改后已无「expandable list」语义（回合内联列表 + 卡片激活）。为保证 MODIFIED 名称匹配而保留旧名可以接受，但建议在归档后的下一次清理中用 REMOVED+ADDED 对更名（如 "Per-Turn Inline Artifacts With Preview Activation And Direct Download"），避免长期误导。
  2. delta 断言 `frame-ancestors 'none'` "MUST remain locked down" —— 注意 CSP 经 `<meta>` 投递时 `frame-ancestors`（与 `sandbox`/`report-uri`）按规范被**忽略**，现状它就是装饰性的（`web/index.html:8`）。保留无害，但不要据此认为有防嵌入保护；真要生效需在部署层用 HTTP 响应头（超出本变更范围，记一笔即可）。另外 `frame-src https:` 与既有 `img-src https:` 同口径，若开发环境 OSS 走 `http://localhost`，iframe 与图片一样会被拦——与现状一致，非新增问题。

## 已排查无问题

- delta 格式：MODIFIED 四条 + REMOVED 一条名称与基线逐字一致，REMOVED 含 Reason+Migration，scenario 全部 4 个 `#`，`openspec validate` 通过。
- EventLog 迁入回合不破坏 live/轮询：事件 query（`useVersionEventsQuery`）与 `useTaskLive` 都挂在页面级而非 VersionTree，组件位置变化无影响；`liveRefetchInterval` 函数形式不受重排影响。
- 回合升序无需客户端排序：`task-read-api` 规定 versions 按 `version_no` ascending 返回（flat array）。
- `RollbackControl` 为纯展示组件（id-agnostic，回调上抛），迁入回合尾部可整体复用，`rollback-*` testid 可原样保留；`iterate-prompt`/`iterate-submit` testid 现存可保留，`iterate-button` 退役已在 D4 显式点名。
- VersionTree testid（`version-select`/`version-node`/`current-marker`）仅被 `TaskDetail(.test).tsx` 与 `VersionTree(.test).tsx` 引用，退役影响面与 tasks 1.7 列举一致，无遗漏消费方。
- D6 选 `src` 弃 `srcdoc` 的两条理由核实成立：`srcdoc` 继承宿主 CSP（`script-src 'self'` 会拦产物内联脚本）、且需先 `fetch`（撞 OSS CORS）；iframe 直载导航不受 CORS 约束、仅需 `frame-src` 放行。
- sandbox 口径符合安全红线：`allow-scripts` 不与 `allow-same-origin` 同授，无 `allow-popups`/`allow-top-navigation`，opaque origin 隔离 cookie/storage/DOM。
- 与 `refactor-web-shell-layout` 无 spec 文件重叠（本变更动 web-tasks-pages + web-artifact-preview，对方只动 web-bootstrap），归档不冲突；`<main>` 滚动归属的顺序依赖已在 D4/Risks 声明「shell 先行、否则自带调整」，且现行 `root-layout.tsx` 的 `main.overflow-auto` 在改为 TaskDetail 内部滚动时不影响其它路由（TaskList/Cost 仍靠 main 滚动），实现时按 tasks 1.5 核对 `h-full` 链路即可。
- 默认锚点延续：`TaskDetail.tsx` 现有 effect 已实现「进入详情默认选中 current_version、离开清空」，D3「行为延续」与代码一致（覆写时序问题另见 S4）。
