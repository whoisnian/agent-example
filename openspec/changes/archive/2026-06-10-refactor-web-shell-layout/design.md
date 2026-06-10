## Context

上一轮 `refactor-web-shadcn-three-column` 已落地：shadcn 基座 + CSS 变量主题、三栏 `RootLayout`（`SideNav` w-60/w-16 折叠、main `flex-1 p-6`、`PreviewColumn` 固定 `w-80` 静态列/小屏抽屉）、UI store 已有 `navCollapsed`/`previewCollapsed`/`selectedVersionId`。对照目标参考图（Claude.ai 风格三栏，`~/Pictures/Screenshot.png`）：

- 参考图右侧预览面板占可用宽度一半以上，是视觉主体；当前右栏 320px 是配角。
- 参考图中栏是有限适读宽度的内容流；当前 main 吃掉全部剩余宽度。
- 参考图左导航很窄（约 190px），自上而下为：主操作（New chat）、分组导航、**Recents 最近列表**（占据导航大部分高度）、底部圆形头像用户区；当前 `SideNav` 只有 logo + 3 个导航项 + 文本式 logout。

数据侧 `useTasksQuery({page, pageSize, status?})` 已就绪（React Query，key `["tasks","list",params]`），Recents 可直接复用，无新增 API。主题色与中栏/右栏内容形态明确不在本变更内（归 `refactor-web-conversation-rich-preview`）。

## Goals / Non-Goals

**Goals:**
- 三栏宽度重心重排：右栏主导（lg+ 约 50% 可用宽度）、中栏适读宽度居中、左导航收窄；保留既有折叠/抽屉降级行为。
- `SideNav` 信息结构升级：New task 主按钮、Recents 最近任务列表、头像式用户区；折叠态（图标条）全部优雅降级。
- 既有 `data-testid` 与契约测试尽量零改动；新增结构补充新 testid 与测试。

**Non-Goals:**
- 不改主题 token / 配色（globals.css 不动）。
- 不改右栏面板内部内容（产物列表/预览逻辑、头部工具栏、富渲染均归下一变更）。
- 不做右栏拖拽调宽 / 全屏切换（如需，随下一变更的预览工具栏一起考虑）。
- 不新增 API、不动数据访问层与 Query/Zustand 分工。

## Decisions

### D1：栏宽用「左右固定策略 + 中栏 minmax」的 flex 比例实现，不引入拖拽
- **宽度基准（明确定义，避免实现即兴）**：右栏百分比的基准是**扣除左导航后的剩余宽度**（即 nav 之外的 main+preview 区）。lg（1024px）三栏并排时若右栏强取 50%，中栏扣除 padding 后仅剩约 300px 不可用，因此分两档：`xl+` 右栏约 50% 剩余宽；`lg~xl` 允许 40%~50% 区间（实现取 `lg:w-[40%] xl:w-[50%]` 即可满足）。spec scenario 同步按此口径放宽。
- `SideNav`：展开 `w-60` 收窄为 `w-56`（折叠 `w-16` 不变）——参考图导航更窄，但 Recents 标题截断可读性优先，14rem 是折中。
- `PreviewColumn`：lg+ 由 `w-80 shrink-0` 改为上述百分比（`min-w-0` + `max-w-[56rem]` 防超宽屏失衡）；小屏抽屉形态保持现状（抽屉宽度仍 `w-80` 级别，避免全屏遮挡）。
- main：`flex-1 min-w-0`，内容包一层 `mx-auto w-full max-w-3xl` 的适读容器（落在 `root-layout.tsx` 的 `<main>` 内层，不动各 route 组件）。
- **为何不拖拽**：拖拽需要持久化宽度状态 + 指针事件处理，MVP 收益低；先用固定比例贴近参考图，后续如有需要再开小 change。
- **替代**：CSS grid 三列 —— 与现状 flex 等价，改动面更大，弃。

### D2：Recents 复用 `useTasksQuery` + 静默选项，不新增 API 或专用缓存
- `SideNav` 内新建 `RecentTasks` 子组件（仍在 `components/layout/`），消费 `useTasksQuery({ page: 1, pageSize: 8 })`——与 TaskList 首页共享 query key 缓存语义（参数不同 key 不同，互不干扰）。
- **静默选项（必须的数据访问层小改）**：现状 `listTasks` 未传 `toastOnError:false`（transport 层默认 toast）、`useTasksQuery` 无 `meta:{silent:true}`（query cache 层再补一个）——"静默错误"按现状不可实现。给 `useTasksQuery`/`listTasks` 增加可选 `{silent?: boolean}`，Recents 传入，TaskList 不传、行为不变。两个消费方 query key 不同，meta 不互相污染。
- 列表项：任务标题（截断）+ 状态点（复用 status → 色彩的既有映射思路，但不引入新 token）；点击 `navigate(/tasks/:id)`；当前路由对应任务高亮（`NavLink`/`useParams` 判断）。
- **"最近"的真实语义是 `created_at` 倒序**（`task-read-api` spec 明确 newest-first = created_at descending），不是"最近活跃"：一个月前创建、昨天刚迭代的任务不会进前 8 条。MVP 接受该偏差（客户端被禁止重排，且只有首页数据）；如需"最近活跃"语义，属 `task-read-api` 的后续 change（list 按 `updated_at` 排序或加 `order_by` 参数）。
- 错误/加载态：导航场景保持安静——loading 渲染 skeleton 行，error 渲染单行静默占位（经上述静默选项，不 toast，不放大故障面）。
- 折叠态（图标条）：Recents 整段隐藏（参考图同样无折叠态 recents）。
- **替代**：专设 `useRecentTasksQuery` + 独立 key —— 多一份缓存与失效面，无收益，弃。

### D2b：补齐 iterate / rollback / control 的 list 前缀失效
现状核实：只有 `useCreateTaskMutation` 失效 `taskKeys.all`（`["tasks"]`，覆盖 list）；`useIterateTaskMutation` / `useRollbackTaskMutation` / `useControlTaskMutation` 的 `onSettled` 只失效 `taskKeys.detail`（注意其 key 是单数 `["task", id]`，不在 `["tasks"]` 前缀下）与 versions，`use-task-live.ts` 的 live 帧同样只失效 detail + versions。结果：任务变更/运行期间 Recents 与 TaskList 的状态点都不会刷新。
- **决策**：三个 mutation 的 `onSettled` 增加 `invalidateQueries({ queryKey: ["tasks", "list"] })`；`useTaskLive` 的 task 帧处理同样补失效该前缀（status 帧频率低，失效成本可接受）。
- 此改动同时修复 TaskList 页的既有过期问题，属顺带收益而非范围蔓延（Recents 的正确性依赖它）。

### D3：用户区改头像式，不引入新依赖
- 邮箱首字母大写圆形头像（`div` + `rounded-full bg-primary`），右侧两行：邮箱（截断）+ "Logout" 文本按钮；折叠态只剩头像，点击弹出（沿用 `title` 提示 + 直接 logout 按钮并存的简单形态，不引 DropdownMenu）。
- **为何不引 shadcn Avatar/DropdownMenu**：Avatar 组件核心价值是图片加载回退，本项目无头像图片源；DropdownMenu 为一个 logout 动作引入 Radix 新依赖不值。保持「仅装实际用到的」纪律。
- `user-area`/`user-email`/`logout-button` testid 保持。

### D4：New task 主按钮放 brand 行之下，复用 shadcn Button
- 展开态：全宽 `Button`（default variant）+ Plus 图标 + "New task"，`navigate("/tasks/new")`；折叠态：图标 Button。
- testid：`nav-new-task`。TaskList 页内既有创建入口不动（两入口并存，与参考图一致）。

### D5：测试与 testid 策略
- 不动的：`side-nav`、`nav-collapse-toggle`、`nav-tasks|cost|settings`、`user-area`、`user-email`、`logout-button`、`preview-column`、`preview-open`、`preview-close`、`preview-backdrop`、`content-slot`、`root-layout`。
- 新增：`nav-new-task`、`recent-tasks`、`recent-task-item`（行）、`recent-tasks-empty|loading|error`。
- `SideNav.test.tsx`：MSW 的 task-list handler 已存在（`test/mocks/handlers.ts` 已 mock `GET /api/v1/tasks`），空态/错误态用 `server.use()` 覆盖即可；但**既有 3 个用例（user-email、null user、collapse toggle）没有包 `QueryClientProvider`**——SideNav 引入 `useTasksQuery` 后会直接抛 "No QueryClient set"，需统一加 wrapper。既有断言意图不变，但"零改动通过"不成立，属已知例外。
- `PreviewColumn.test.tsx` 无宽度类名断言（已核实只断 aria-hidden/testid），宽度重排不影响它。

## Risks / Trade-offs

- **[右栏 50% 在中等宽度屏挤压中栏]** → 已在 D1 定为两档（lg~xl 40%~50%、xl+ 约 50%，基准为扣除 nav 的剩余宽），spec scenario 按此口径表述，不再依赖实现期权衡。
- **[Recents 与 TaskList 数据不同步]**（两个不同 params 的 query key）→ 由 D2b 的 list 前缀失效修复（iterate/rollback/control + live task 帧）；创建路径本就失效 `taskKeys.all`，无需改动。
- **[SideNav 测试因结构变化碎裂]** → 保持既有 testid 与 DOM 角色查询；新增结构只追加不改名。
- **[适读容器影响现有页面布局]**（TaskList 表格、CostDashboard 图表原本吃全宽）→ `max-w-3xl` 对表格/图表偏窄，实现时核对各页观感，必要时放宽到 `max-w-4xl`；该值只在 root-layout 一处，调整成本低。

## Migration Plan

单 PR 可完成（预计 <500 行）：(1) root-layout/PreviewColumn 宽度重排 → (2) SideNav 新结构（New task、Recents、头像用户区）→ (3) 测试更新与新增 → (4) `docs/ARCHITECTURE.md §4.3` 同步。纯前端、无数据迁移，revert 即回滚。

## Open Questions

- Recents 条数（默认 8）与是否需要"View all →"尾行：默认不加（主导航已有 Tasks 入口），实现时如观感空洞可补。
- 中栏适读宽度初值 `max-w-3xl` vs `max-w-4xl`：以 TaskList 表格不出现压迫性换行为准，实现时定。
