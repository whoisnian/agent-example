## Why

对照目标参考图（`~/Pictures/Screenshot.png`，Claude.ai 风格三栏），外壳层差距由 `refactor-web-shell-layout` 处理后，仍有两块内容层差距：中栏 TaskDetail 是传统"标题 + 版本树 + 事件小节"的文档式页面，而参考图是**对话流**——每轮交互一个回合、底部常驻输入框；右栏预览面板只有文本截断/图片/下载三态，而参考图是**带头部工具栏（标题 · 类型、Copy、刷新）的 HTML 富渲染面板**。经用户确认：版本树形式不再保留，**每个版本作为一个对话回合**渲染，回合尾部内联给出该版本的回滚按钮与产物列表；富渲染纳入范围（显式推翻上轮 `refactor-web-shadcn-three-column` 的 Post-MVP 边界）。

## What Changes

- **TaskDetail 对话流重排**（API 契约与数据访问不变，仅消费方式变化）：
  - 页面改为"紧凑头部（标题/状态/类型/成本徽章/控制条）+ 可滚动**回合流** + 底部常驻迭代输入框（composer）"；
  - **版本树退役**：版本不再以父子缩进树呈现，改为按 `version_no` 线性排布的对话回合；分支来源以回合内"from v{n}"标注表达（数据仍是 `parent_id`）。**BREAKING（前端内部）**：`web-tasks-pages` 的 "Version Tree Rendering" requirement 移除；
  - **每个回合**渲染：该版本的 prompt（用户消息位，经既有 `GET /versions/{id}` 懒取）、版本号/状态/成本摘要（结果位）、**该版本的产物列表卡片**（内联，点击驱动右栏预览该产物）、**回合尾部的回滚按钮**（branch / switch，互斥与 409 语义不变；当前版本回合标记 current、不提供回滚）；
  - `task.current_version` 对应的回合内联展示事件流（live/轮询管线不变；switch 回滚后 current 可能位于对话中段，属预期）；
  - Iterate 由"按钮展开 textarea"改为底部常驻 composer（互斥语义不变）。
- **右栏预览面板头部工具栏**：选中产物标题 · 类型标签 + Copy / Refresh / 关闭；选择锚点由"版本树行选中"改为"对话回合内产物卡片点击"（全局 store 增加选中产物维度）。
- **HTML 产物富渲染**：`text/html` 产物默认在面板内通过**沙箱 iframe**（`sandbox="allow-scripts"`，不含 `allow-same-origin`，`src` 指向 presigned OSS URL）整页渲染，提供"渲染 / 源码"切换。
- **CSP 调整**：`index.html` 增加 `frame-src https:`；`script-src 'self'`、`object-src 'none'`、`frame-ancestors 'none'` 保持锁定。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `web-tasks-pages`:
  - "Task Detail Page"——页面结构改为对话流（紧凑头部 / 回合流 / 底部常驻 composer）；
  - "Version Tree Rendering"——**REMOVED**（被对话回合流取代）；
  - "Version Artifacts Expandable List With Direct Download"——产物入口由"版本树行选中驱动右栏"改为"回合内联产物列表 + 点击卡片驱动右栏预览"；
  - "Rollback Action With Mode Selection And UI Task-Level Mutex"——回滚入口由版本树行改为回合尾部按钮（语义不变）；
  - "Iterate Action With UI Task-Level Mutex"——交互形态改为常驻 composer（语义不变）。
- `web-artifact-preview`:
  - "Artifact Preview Panel"——头部工具栏（选中产物标题、Copy、Refresh）；选择锚点来源改为对话回合（store 增加选中产物 id）；
  - "Lightweight Artifact Content Preview"——`text/html` 增加沙箱 iframe 富渲染与渲染/源码切换；
  - "Content Security Policy for OSS Preview"——增加 `frame-src https:`。

## Impact

- 受影响代码：`web/src/routes/TaskDetail.tsx`（对话流重排 + composer）、`web/src/components/tasks/VersionTree.tsx`（退役，由回合组件取代）、`web/src/components/tasks/RollbackControl.tsx`（迁入回合尾部）、`web/src/features/artifacts/ArtifactPreviewPanel.tsx`（工具栏 + iframe）、`web/src/features/ui/store.ts`（新增选中产物 id）、`web/src/components/layout/PreviewColumn.tsx`（去自有头部）、`web/src/features/tasks/queries.ts` + `api.ts`（`useVersionQuery` 静默化，见下）、`web/index.html`（CSP）。
- 测试：`TaskDetail.test.tsx`、`VersionTree.test.tsx`（随退役改写为回合断言）、`RollbackControl.test.tsx`、`ArtifactPreviewPanel.test.tsx`、`PreviewColumn.test.tsx`（其 `preview-close` 断言随头部迁移转移到面板测试）。
- 数据访问无新增 transport：回合 prompt 用既有 `useVersionQuery`，回合产物用既有 `useVersionArtifactsQuery`（按回合懒取、Query 缓存去重）。但 prompt 的"静默降级"要求给 `useVersionQuery` 补 `meta:{silent:true}` + 404 不重试、`getVersion` 传 `toastOnError:false`（该 hook 现状零消费方，无回归面）——否则失败会连弹多个 toast。
- 安全面：iframe 沙箱不授予 `allow-same-origin`；CSP 放宽仅限 `frame-src`。无 API / Worker / MQ / DB 影响；无新增 npm 依赖。
- 依赖关系：建议在 `refactor-web-shell-layout` 之后实施，无硬性代码依赖。
