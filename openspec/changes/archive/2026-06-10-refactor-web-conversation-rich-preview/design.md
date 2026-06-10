## Context

外壳层（栏宽重心、SideNav）由 `refactor-web-shell-layout` 处理。本变更处理中栏与右栏的内容形态：

- `TaskDetail.tsx` 现状是文档式页面：头部行 → Cost 面板 → VersionTree（父子缩进树 + 行选中驱动右栏 + 行内回滚）→ 当前版本 EventLog，整页随 `<main>` 滚动。参考图中栏是对话流：每轮一个回合、主体滚动、输入框常驻底部。经用户确认**版本树形式退役**，每个版本作为一个对话回合，回合尾部内联回滚按钮与产物列表。
- 数据形状（已核对 `features/tasks/types.ts`）：列表 DTO `VersionNode` 含 `id/parent_id/version_no/status/is_active/cost`，**不含 `prompt`**；`prompt` 在 `VersionFull`（`GET /versions/{id}`，已有 `useVersionQuery`）。产物按版本懒取（`useVersionArtifactsQuery`）。事件只对当前版本拉取（`useVersionEventsQuery` + live/轮询）。
- `ArtifactPreviewPanel.tsx` 支持产物列表 + 单产物轻量预览；`PreviewColumn.tsx` 持有静态头部。参考图右栏是"产物标题 · 类型 + Copy/刷新/关闭"工具栏 + HTML 整页渲染。
- 现行 CSP（`web/index.html`）：`default-src 'self'`，**无 `frame-src`**——iframe 加载 OSS presigned URL 会被拦，必须显式放开。
- 富渲染在上轮 design 被列为 Post-MVP Non-Goal，本变更经用户确认显式推翻。

## Goals / Non-Goals

**Goals:**
- TaskDetail 重排为对话流：紧凑头部 + 回合流（每版本一回合：prompt / 结果摘要 / 产物卡片 / 回滚按钮）+ 底部常驻 composer；iterate/rollback/control 的互斥与 409 语义不变。
- 回合内产物卡片点击驱动右栏预览（store 增加选中产物维度）。
- 预览面板头部工具栏：选中产物标题与类型、Copy、Refresh、关闭。
- `text/html` 产物沙箱 iframe 富渲染 + 渲染/源码切换；CSP `frame-src` 配套放开。

**Non-Goals:**
- 不改主题 token / 配色；不动外壳栏宽（归 shell-layout 变更）。
- 不引入图形化版本树/分支可视化（线性回合 + "from v{n}" 标注即止）。
- 不做 markdown/代码高亮等其它富渲染引擎（仅 HTML iframe）。
- 不做 HTML 内多文件资源解析（单文件 HTML 产物为限）。
- 不改任何 API / 数据访问层；不新增 npm 依赖。

## Decisions

### D1：回合流按 `version_no` 线性排布，分支用标注不用树
`GET /tasks/{id}/versions` 返回的版本按 `version_no` 升序线性渲染为回合序列。回合的 `parent_id` 若不指向上一个 `version_no`（即 rollback-branch 产生的分叉），回合头部加 "from v{n}" 标注；switch 型回滚不产生新版本，无需特殊呈现（current 标记移动即可）。
- **为何**：对话是线性媒介；MVP 的分支深度有限，标注足以回溯，树形可视化明确属 Post-MVP。
- **替代**：按树深度优先排序 —— 回合顺序与时间序脱节，对话语义混乱，弃。

### D2：回合结构与数据获取——全部复用既有 query，按回合懒取
每个回合（`ConversationTurn` 组件，落 `components/tasks/`）渲染：
1. **用户消息位**：该版本 `prompt`，经 `useVersionQuery(version.id)` 懒取（Query 缓存，版本终态后内容不变；加载中渲染 skeleton 行，失败静默降级为只显示版本号）。**前置小改**：现状 `getVersion` 未传 `toastOnError:false`、`useVersionQuery` 无 `meta:{silent:true}` 也无 404 跳过 retry——静默降级按现状不可实现（失败会连弹多个 toast）。需为其补齐三者（对照同文件 `useTaskQuery` 写法）；该 hook 现状零消费方，改动无回归面，已列入 proposal Impact；
2. **结果位**：`v{version_no}` + StatusBadge + 成本（`CostSummary`）摘要行；当前版本回合带 current 标记；
3. **产物卡片列表**：`useVersionArtifactsQuery(version.id)` 内联展示该版本产物（kind/mime/size），点击卡片驱动右栏预览（见 D3）；quiet loading / 空态（无产物则整段省略）/ 静默错误；
4. **回合尾部动作**：非当前版本回合给 Rollback（branch / switch）按钮，复用现有 `RollbackControl` 与 mutation（互斥/`is_active` 禁用/409 语义原样）；**`task.current_version` 对应的回合**内联 EventLog（live 事件流，管线不变）。
- **N+1 边界**：回合数 = 版本数，MVP 单任务版本数小（个位数量级）；`useVersionQuery`/`useVersionArtifactsQuery` 均按 key 缓存去重，终态版本不再变化、无轮询，请求量可接受。如后续版本数膨胀，再提"列表 DTO 附带 prompt/artifacts 摘要"的 API change。
- **事件流归属（措辞精确化）**：严格锚定 `task.current_version`（既有 `useVersionEventsQuery(currentVersionId)` 与 `use-task-live` 订阅 `version:<current>` 的管线就是这个语义），**不是**"活跃版本"或"最大 version_no"——switch 回滚后 current 可指向较早版本，此时 EventLog 出现在对话中段，属预期；历史回合不展示事件——回溯细节属 Post-MVP。

### D3：产物选择锚点从"版本行选中"改为"产物卡片点击"，store 增加 `selectedArtifactId`
`features/ui/store.ts` 在 `selectedVersionId` 之外新增 `selectedArtifactId: string | null`（及 setter）。点击回合内产物卡片：同时写入两者并展开右栏（若折叠）；右栏面板内点击产物行同样写 `selectedArtifactId`（面板内部选中状态上移到 store，两个入口共享一份选中态）。`selectedVersionId` 默认值仍为任务 `current_version`（进入详情页时右栏默认显示当前版本产物列表，行为延续）。
- **一致性不变式（必须，防跨版本悬挂选中）**：`TaskDetail` 现有 effect 在 `current_version` 变化时无条件覆写 `selectedVersionId`（iterate/rollback 完成后由 live/refetch 触发）；若 `selectedArtifactId` 不随之处理，会出现"version 已指向新版本、artifact 还指向旧版本产物"的非法组合。store 层约定：**单独 set `selectedVersionId` 且值变化时同步清空 `selectedArtifactId`**；只有 D3 的卡片点击路径用成对 setter 一次性写入两者。面板侧兜底：选中产物不在当前版本列表中时按"未选中"渲染（仅列表态），不报错。
- **为何**：参考图交互是"对话里点产物卡 → 右侧打开该产物"；选中态若留在面板内部，回合卡片无法驱动它。
- **替代**：回合卡片只设 `selectedVersionId`、由面板自动选首个产物 —— 点击 A 产物却预览到 B，语义错误，弃。

### D4：TaskDetail 用「flex 列 + 滚动主体 + 常驻 composer」重排，不引入虚拟滚动
页面根容器 `flex h-full flex-col`：紧凑头部（标题/StatusBadge/类型/CostBadge/ControlBar，Cost 面板压缩为紧凑条）→ `flex-1 overflow-y-auto` 回合流 → 底部 composer（textarea + 提交按钮，`shrink-0`）。`<main>` 不再整页滚动（与 shell-layout 的适读容器协同，`h-full` 链路打通）。新回合出现/事件追加时自动滚底（用户上滚时不抢占）。
- **Cost 紧凑条字段不减**：基线 "Task Detail Cost Panel With Token Breakdown" requirement（本变更未 MODIFY）要求面板展示完整分解（amount + input/output/cached tokens + tool_calls + wall_time_ms，源自 `/tasks/{id}/cost`），且 CostBadge 与面板并存。紧凑条只压缩排版（横排/小字号），**完整字段集与既有 testid 保留**，不触发该 requirement 的 delta。
- **testid 影响（已知例外）**：`iterate-button` 退役（composer 常驻），`iterate-prompt`/`iterate-submit` 保留；`VersionTree.tsx` 退役，其 `version-select`/`aria-selected` 交互由回合产物卡片取代，`VersionTree.test.tsx` 改写为回合断言；`RollbackControl` 及其 testid 迁入回合尾部尽量原样保留。
- composer 禁用语义沿用现有互斥规则：活跃状态禁用 + `title` 原因；提交中禁用防双发；成功清空、失败保留输入；409 处理原样。

### D5：预览面板头部工具栏归 `ArtifactPreviewPanel`，`PreviewColumn` 退化为纯容器
`PreviewColumn` 删除自有头部行，仅保留抽屉/遮罩/重开骨架；工具栏由面板渲染为首行：左侧"选中产物 `kind` · mime 标签"（未选中回退 "Artifact Preview"），右侧 Copy / Refresh / 关闭（关闭走 ui store `togglePreview`，`preview-close` testid 迁移保留）。
- **工具栏必须在面板所有状态渲染**：现行面板在 no-version / loading / error / empty 各分支**早退**，而面板挂在 `RootLayout` 全局（/tasks、/cost 页也在）——若工具栏只随产物内容渲染，这些状态下抽屉将没有关闭按钮。实现时把工具栏提升到所有早退分支之上（标题回退 + Copy/Refresh 禁用 + 关闭可用）。
- **测试影响**：`PreviewColumn.test.tsx` 现以纯 div children 渲染并断言 `preview-close`，头部迁移后该用例必坏——close 断言随迁到面板测试（已列入 proposal Impact）。
- **为何**：标题与 Copy/Refresh 依赖选中产物状态（现已上移 store，见 D3），关闭按钮放面板与放容器行为一致，少一层 props 传递。

### D6：HTML 富渲染用 `<iframe src={presignedUrl} sandbox="allow-scripts">`，不用 `srcdoc`
`mime === "text/html"` 的产物默认进入"渲染"视图：沙箱 iframe 直接以 presigned OSS URL 为 `src` 整高渲染；工具栏提供"渲染 / 源码"切换，源码视图复用既有 text-like 截断预览路径。
- **为何 `src` 而非 `srcdoc`**：(1) `srcdoc` 继承宿主页 CSP，`script-src 'self'` 会拦死产物内联脚本；(2) `srcdoc` 需先 `fetch` 字节，受 OSS CORS 关卡（上轮 Open Question 仍未验证）；`src` 直载不经 CORS，只需 CSP `frame-src` 放行。
- **沙箱口径**：`sandbox="allow-scripts"`，**不含** `allow-same-origin`（opaque origin，隔离宿主 cookie/storage/DOM）；绝不同时授予两者。
- **失败处理（按可检测性分两类，跨域沙箱 iframe 没有可靠的"加载失败"信号）**：过期 presigned URL 返回的 OSS 403 XML 会作为正常文档渲染进 iframe 并触发 `load`（`error` 事件不触发）；opaque origin + 跨域使宿主无法读 `contentDocument` 探测；`fetch` 预检又撞 OSS CORS。因此：
  1. **presign 阶段失败**（mutation onError，可靠检测）→ 单条 inline 错误 + Refresh，不 toast；
  2. **iframe 内的 HTTP/网络失败** → 接受"OSS 错误文档直接显示在 frame 里"为 MVP 降级形态，工具栏常驻的 Refresh 即恢复手段，不另设错误检测。
- **降低 2 的发生面**：选中产物时即时 presign 后立刻挂 iframe（URL 新鲜期内加载），过期主要发生在"挂起很久后回看"场景。
- **替代**：`fetch` 后 `blob:` URL —— 仍受 CORS + 需放行 `frame-src blob:` + 大文件驻留内存，弃。

### D7：CSP 仅增 `frame-src https:`
与上轮 `img-src https:` 同口径（OSS 多桶多区域，枚举 host 不现实）；`script-src 'self'`、`object-src 'none'`、`frame-ancestors 'none'`、`base-uri 'self'` 全部保持。安全权衡：允许 iframe 嵌任意 https 源，但 iframe 全部带 sandbox 且无 same-origin，可接受。
- **备忘**：CSP 经 `<meta>` 投递时 `frame-ancestors` 按规范被浏览器忽略——现状它本就是装饰性的，保留无害但不构成防嵌入保护；真要生效需部署层 HTTP 响应头（超出本变更范围）。另外 `frame-src https:` 与既有 `img-src https:` 一样不放行 `http://localhost` 的 OSS——与现状一致，非新增问题。

### D8：Copy 只复制已加载的完整文本，截断态禁用
Copy 用 `navigator.clipboard.writeText` 写入已 fetch 的文本缓冲（含 HTML 源码视图）：帽内完整时可用 + success toast；截断时禁用并以 `title` 引导下载；非文本类或源码未加载时禁用；clipboard API 不可用（非安全上下文）时禁用不抛错。Refresh 对选中产物重新 presign 并重放预览，复用"每次重新 mint"规则。

## Risks / Trade-offs

- **[回合懒取造成请求扇出]**（每版本 1×version + 1×artifacts）→ 版本数小 + Query 缓存 + 终态不重取；膨胀时再开 API change 聚合。
- **[版本树测试与 spec 断言大面积改写]** → `VersionTree.test.tsx`/`TaskDetail.test.tsx` 的树形/行选中断言必然重写，但回滚/互斥/409 的行为断言意图不变；在 tasks 显式点名，作为"testid 稳定"硬约束的已知例外。
- **[iframe 加载已过期 presigned URL]** → 跨域沙箱 iframe 无可靠失败信号（见 D6），presign 失败走 inline 错误，frame 内 HTTP 失败接受为降级形态 + Refresh 恢复。
- **[HTML 产物内相对引用资源 404]** → 接受为 MVP 限制（单文件 HTML），源码/下载兜底。
- **[自动滚底与用户回看冲突]** → 仅当滚动位置已在底部附近时跟随新内容，上滚回看时不抢占（实现为简单阈值判断，不引库）。
- **[与 shell-layout 的滚动容器耦合]** → 按"shell-layout 先行"的顺序实施；顺序颠倒则本变更自带 `<main>` 滚动归属调整。
- **[sandbox iframe 内脚本行为不可控]**（产物由 Worker LLM 生成）→ opaque origin 隔离 + 无 `allow-popups`/`allow-top-navigation`。

## Migration Plan

两个 PR（均 <500 行）：
- **PR1｜TaskDetail 对话流**：回合组件（prompt/摘要/产物卡片/回滚尾部/当前回合事件流）+ 常驻 composer + VersionTree 退役 + store 增加 `selectedArtifactId` + 相关测试改写。
- **PR2｜预览工具栏 + 富渲染**：PreviewColumn 去头部、面板工具栏（Copy/Refresh/关闭、选中态接 store）、HTML iframe 渲染与切换、`index.html` CSP `frame-src`、面板测试。

纯前端、无数据迁移；任一 PR 可独立 revert。建议排在 `refactor-web-shell-layout` 实施之后。

## Open Questions

- 历史（非当前）回合是否提供"查看该版本事件"入口：当前决定**否**（events API 按版本可拉，但 UI 入口属回溯增强，Post-MVP）。
- HTML 渲染视图默认是否自动执行脚本：当前决定**是**（`allow-scripts`，对齐参考图）；如对 LLM 生成内容有顾虑，可降为默认无脚本 + "启用交互"开关，属小改。
- 回合内产物卡片与右栏面板列表并存是否冗余：当前保留两处（面板列表是 `web-artifact-preview` 既有契约，且回合卡片只覆盖单版本视角）；如实施后观感重复，可在后续 change 中把面板收敛为纯预览区。
- requirement 旧名 "Version Artifacts Expandable List With Direct Download" 在回合化后已无 "expandable list" 语义：为保证 MODIFIED 名称匹配本轮保留旧名，归档后的下一次清理可用 REMOVED+ADDED 对更名（如 "Per-Turn Inline Artifacts With Preview Activation And Direct Download"）。
