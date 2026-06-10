# Suggestions — refactor-web-shell-layout

> 评审基线：proposal.md / design.md / specs/web-bootstrap/spec.md / tasks.md，对照 `openspec/specs/web-bootstrap`、`web/src` 现行代码逐项核实。`openspec validate` 已通过（delta 格式、requirement 名称、scenario 标题层级均无问题）。

## S1: Recents「静默错误」按现有 `useTasksQuery` 不可实现，且修复必然触碰数据访问层（高）

- **目标**：design D2 / delta spec "Recents stays quiet on failure" scenario / proposal "不改任何 API / 数据访问"
- **问题**：delta 要求 Recents 读取失败时 "render an inline quiet error placeholder **without emitting any toast**"，design D2 也写「error 渲染单行静默占位（`meta.silent` 思路，不 toast）」。但实际代码两层都会 toast：
  - `web/src/features/tasks/api.ts` 的 `listTasks()` 走 `apiFetch` 且未传 `toastOnError:false`，而 `web/src/services/http.ts:131` 默认 `toastOnError = true` —— 每次失败（含 retry，默认最多重试 2 次）都会弹 transport 层错误 toast；
  - `web/src/features/tasks/queries.ts` 的 `useTasksQuery` 没有 `meta:{silent:true}`，`web/src/services/query-client.ts:40-42` 的 `queryCache.onError` 会再补一个 toast。
  也就是说「只消费既有 task list query、零数据访问改动」（proposal "Recents 只消费既有 task list query"、design Non-Goal「不动数据访问层」）与该 scenario 互斥。
- **建议**：给 `useTasksQuery` / `listTasks` 增加可选 `{ silent?: boolean }`（映射 `meta:{silent:true}` + `toastOnError:false`），Recents 调用方传入；TaskList 保持现状。两个消费方的 query key 不同（`pageSize` 8 vs 20），meta 不会互相污染。同时把 `features/tasks/queries.ts`、`features/tasks/api.ts` 列入 proposal Impact，并修订「不改数据访问」的措辞为「不改数据访问的契约/transport，仅增加静默选项」。

## S2: 「变更类 mutation 已按 `taskKeys.all` 前缀失效」与代码不符，Recents 会持续展示过期状态（中）

- **目标**：design Risks「Recents 与 TaskList 数据不同步」、tasks 2.3、proposal Impact
- **问题**：design 断言「变更类 mutation 已按 `taskKeys.all` 前缀失效」。实际 `web/src/features/tasks/mutations.ts`：只有 `useCreateTaskMutation` 失效 `taskKeys.all`（=`["tasks"]`，能覆盖 `["tasks","list",*]`）；`useIterateTaskMutation` / `useRollbackTaskMutation` / `useControlTaskMutation` 的 `onSettled` 只失效 `taskKeys.detail(taskId)`（=`["task",id]`，注意是单数 `"task"`，不在 `["tasks"]` 前缀下）与 `taskKeys.versions(taskId)`。另外 `web/src/features/tasks/use-task-live.ts:35-39` 的 live 帧也只失效 detail + versions。结果：iterate/rollback/control 之后以及任务运行期间，Recents 的状态点与排序都不会刷新。
- **建议**：在上述三个 mutation 的 `onSettled` 增加 `qc.invalidateQueries({ queryKey: taskKeys.all })`（或 `["tasks","list"]` 前缀），并考虑在 `useTaskLive` 的 task 帧处理中同样失效 list 前缀（否则运行中的状态点仍是旧值）。该修复触碰 `features/tasks/mutations.ts`（必要时含 `use-task-live.ts`），须补进 proposal Impact —— 当前 Impact 只列了 root-layout / SideNav / PreviewColumn / ui store。若决定接受 staleness，则应从 delta/design 中移除「状态点实时性」的暗示并显式记录取舍。

## S3: 「SideNav 既有断言零改动通过」不成立 —— 3 个用例缺 QueryClientProvider 会直接崩（中）

- **目标**：design D5、tasks 3.1
- **问题**：`web/src/components/layout/SideNav.test.tsx` 中前三个用例（user-email、null user、collapse toggle）直接 `render(<MemoryRouter><SideNav/></MemoryRouter>)`，**没有** `QueryClientProvider`（只有第 4 个 `GatedTree` 有）。一旦 SideNav 内嵌 `RecentTasks` 消费 `useTasksQuery`，这三个用例会抛 "No QueryClient set"。design D5「现有断言（导航高亮、折叠、logout）应零改动通过」不成立。
- **另一处小误差**：tasks 3.1「补 MSW task-list handler」—— 全局 handler 已存在（`web/src/test/mocks/handlers.ts:88` 已 mock `GET /api/v1/tasks` 返回 1 条 fixture），只需为空态/错误态用 `server.use()` 覆盖即可。
- **建议**：把「为 SideNav.test 既有用例包一层 QueryClientProvider wrapper」明确写进 tasks 3.1，并修正 design D5 的零改动断言（保持「断言意图不变」的说法即可）。

## S4: 「右栏约 50%」在 lg~xl 区间与自身 scenario 冲突，宽度基准也未定义（中）

- **目标**：delta "Column proportions" 段 + "Preview column dominates at wide viewports" scenario、design D1 / Risks
- **问题**：两点。
  1. **数字上**：`lg`（1024px）三栏并排时，nav `w-56`（224px）+ 右栏 50% 后，中栏只剩约 280~400px（视 50% 的基准而定），扣掉 `main` 的 `p-6`（48px）后 TaskList 表格/对话流基本不可用。design Risks 给出的缓解是「lg~xl 区间用 `w-[40%]` 渐进」——但 delta scenario 写死了 "**WHEN** the viewport is at or above the wide breakpoint … **THEN** … occupy **approximately half**"。若实现采用 40% 缓解，就违反了自己提交的 scenario。
  2. **措辞上**："approximately half of the available width" 未定义 available 是「整个视口」还是「扣除 nav 后的剩余宽」，两种解读差 100px+。
- **建议**：在 spec 里把「主导宽度」约束到更高断点（如 `xl+` 才要求 ~50%，lg~xl 允许 40%~50% 区间），或把 scenario 措辞放宽为 "approximately 40–55% of the width remaining beside the navigation column"。同时在 design D1 把基准（剩余宽 vs 视口宽）定下来，不留给实现即兴。

## S5: Recents 的「最近」实际是 created_at 倒序，design 表述与 Claude.ai 语义均有偏差（中）

- **目标**：design D2「排序依赖服务端默认序（按创建/更新时间倒序）」、delta "server order is the recency order"
- **问题**：`task-read-api` spec（"Paginated listing returns owner's tasks newest-first" scenario）明确是 **`created_at` descending**，与「更新时间」无关。一个一个月前创建、昨天刚迭代过的任务永远不会进 Recents 前 8 条——这与参考图（Claude.ai Recents = 最近活跃）语义不同。客户端又被 spec 禁止重排（且只有首页数据，重排也无意义）。
- **建议**：MVP 可以接受「最近创建」语义，但要把 design D2 的「创建/更新时间倒序」改为「`created_at` 倒序」，并在 delta 或 design 显式记录该偏差（如需「最近活跃」语义，属 task-read-api 的后续 change：list 按 `updated_at` 排序或加 `order_by` 参数），避免实现者误以为服务端已按活跃度排序。

## S6: 文档同步指错了章节：外壳描述在 ARCHITECTURE.md §3.1，不在 §4.3（低）

- **目标**：proposal Impact、tasks 4.1
- **问题**：`docs/ARCHITECTURE.md` 的 §4.3 是「任务状态机」；三栏外壳/前端模块描述在 **§3.1 前端模块（React）**（「三栏外壳（shadcn/ui）」要点），组件职责表在 §2.2。「§4.3」应是从根 AGENTS.md 的 §4.3（前端协作约定）误抄——上一轮归档变更的 design 也有同样笔误。
- **建议**：tasks 4.1 改为「更新 `docs/ARCHITECTURE.md` §3.1（三栏外壳要点：栏宽重心、SideNav 信息结构）」，必要时连带 §2.2 组件表的 Web Client 职责一句。

## 已排查无问题

- delta 格式：`## MODIFIED Requirements` + requirement 名 "Application Shell" 与基线完全一致，scenario 均为 4 个 `#`，`openspec validate` 通过。
- 保持稳定的 testid 清单（`side-nav`/`nav-*`/`user-area`/`user-email`/`logout-button`/`preview-*`/`content-slot`/`root-layout`）与现行代码逐一对得上。
- `PreviewColumn.test.tsx` 没有 `w-80` 类名断言（只断 aria-hidden/testid），tasks 1.4 的担忧实际不触发，照常即可。
- 新建任务路径：`useCreateTaskMutation` 失效 `taskKeys.all`，新任务会出现在 Recents（与 S2 的 iterate/rollback/control 缺口不同）。
- 头像不引 Radix 的决策可行：`user` 可能为 null（SideNav.test 已覆盖），实现时沿用现有 null 守卫即可。
- 与 `refactor-web-conversation-rich-preview` 无 spec 文件重叠（本变更只动 web-bootstrap），归档时不会合并冲突；代码层 root-layout/PreviewColumn 的先后顺序已在对方 design 声明回退方案。
