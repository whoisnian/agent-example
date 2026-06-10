## 1. 栏宽重心重排

- [ ] 1.1 `root-layout.tsx`：`<main>` 内加居中适读容器（`mx-auto w-full max-w-3xl`，按 TaskList/CostDashboard 观感核定 3xl vs 4xl），`main` 保持 `flex-1 min-w-0`
- [ ] 1.2 `PreviewColumn.tsx`：lg+ 静态列由 `w-80 shrink-0` 改为 `lg:w-[40%] xl:w-[50%]`（基准为扣除 nav 的剩余宽；`min-w-0` + 最大宽度上限）；小屏抽屉形态与 `preview-open/close/backdrop` 行为不变
- [ ] 1.3 `SideNav.tsx`：展开宽度收窄为 `w-56`（折叠 `w-16` 不变）
- [ ] 1.4 核对各页面（TaskList / TaskDetail / TaskCreate / CostDashboard）在新栏宽下无横向溢出或压迫性换行（`PreviewColumn.test.tsx` 已核实无宽度类名断言，预期不需改）

## 2. 数据访问层小改（Recents 前置）

- [ ] 2.1 `features/tasks/queries.ts` + `api.ts`：`useTasksQuery`/`listTasks` 增加可选 `{silent?: boolean}`（映射 `meta:{silent:true}` + `toastOnError:false`）；TaskList 不传、行为不变；补单测
- [ ] 2.2 `features/tasks/mutations.ts`：iterate / rollback / control 的 `onSettled` 增加失效 `["tasks","list"]` 前缀；`use-task-live.ts` 的 task 帧处理同样补失效；更新对应测试

## 3. SideNav 信息结构

- [ ] 3.1 brand 行之下新增 "New task" 主按钮（展开态全宽 Button + Plus 图标，折叠态 icon Button；`data-testid="nav-new-task"`，导航 `/tasks/new`）
- [ ] 3.2 新建 `components/layout/RecentTasks.tsx`：消费 `useTasksQuery({page:1, pageSize:8}, {silent:true})`，服务端序（`created_at` 倒序）展示标题行，点击导航 `/tasks/:id`，当前任务高亮；quiet loading（skeleton 行）/ empty / error（静默占位、不 toast）；testid：`recent-tasks`、`recent-task-item`、`recent-tasks-loading|empty|error`
- [ ] 3.3 `SideNav` 挂载 Recents（主导航之下、用户区之上，可滚动区域）；折叠态整段隐藏
- [ ] 3.4 用户区改头像式：邮箱首字母圆形头像 + 邮箱 + logout；折叠态仅头像 + icon logout；保持 `user-area`/`user-email`/`logout-button` testid

## 4. 测试

- [ ] 4.1 `SideNav.test.tsx`：为既有 3 个无 provider 用例统一包 `QueryClientProvider` wrapper（已知例外：非零改动，断言意图不变）；MSW task-list handler 已存在，空态/错误态用 `server.use()` 覆盖；新增 New task 导航、Recents 列表/高亮/quiet error/生命周期失效刷新、头像用户区断言
- [ ] 4.2 外壳布局断言更新：`router.test.tsx` / `PreviewColumn.test.tsx` 适配新结构（保持既有 testid 断言意图不变）
- [ ] 4.3 `npm run typecheck && npm run lint && npm run test` 全绿

## 5. 文档同步

- [ ] 5.1 更新 `docs/ARCHITECTURE.md` §3.1（前端模块：三栏外壳要点——栏宽重心、SideNav 信息结构）与 §2.2（组件职责表 Web Client 行）；注意不是 §4.3（任务状态机）
- [ ] 5.2 如 `web/AGENTS.md` 涉及外壳/导航描述，同步修订
