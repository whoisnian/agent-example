## Why

上一轮 `refactor-web-shadcn-three-column` 落地了 shadcn 基座与三栏结构，但与目标参考图（`~/Pictures/Screenshot.png`，Claude.ai 风格三栏）对照仍有外壳级差距：当前右侧 Artifact 预览只是 320px 的窄配角列，而参考图中预览面板占约一半以上宽度、是视觉主体；左导航只有 3 个图标项 + logout，缺少参考图中的"新建"主操作、最近列表与底部用户头像区。本变更只处理**外壳层**（栏宽重心 + 左导航信息结构），主题色与中栏/右栏内容形态由后续 `refactor-web-conversation-rich-preview` 变更处理。

## What Changes

- **三栏宽度重心重排**：右侧 Artifact 预览列从固定 `w-80` 改为主导宽度（宽视口下约占可用宽度一半），中栏内容收窄为有限的适读宽度并居中，左导航收窄；窄视口降级行为（右栏抽屉、左栏图标条）保持不变。
- **左导航信息结构升级**（`SideNav`）：
  - 顶部新增 "New task" 主操作按钮（导航到 `/tasks/new`）；
  - 主导航项之下新增 **Recents 最近任务列表**（复用既有 tasks 列表数据访问，展示最近若干条，点击进入详情，当前任务高亮）；
  - 底部用户区改为头像样式（邮箱首字母圆形头像 + 邮箱 + logout），折叠态只剩头像。
- 既有 `data-testid`（`side-nav`、`nav-*`、`user-area`、`logout-button`、`preview-column` 等）保持稳定；新增交互补充新 testid。
- 不改任何 API 契约与 React Query / Zustand 分工；Recents 复用既有 task list query，但需两处数据访问层的小调整（不改 transport 契约）：
  - `useTasksQuery`/`listTasks` 增加可选静默选项（`meta.silent` + `toastOnError:false`），Recents 调用方使用，TaskList 行为不变——否则 Recents 的"静默错误"不可实现（现状两层都会 toast）；
  - iterate / rollback / control 三个 mutation 的 `onSettled` 补充失效 `["tasks","list"]` 前缀（现状只失效 detail + versions，Recents 与 TaskList 在任务变更后会持续过期）。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `web-bootstrap`: "Application Shell" requirement 变更——三栏宽度比例约束（右栏主导、中栏适读宽度）、左导航内容结构（New task 主操作、Recents 最近任务列表、头像式用户区）。

## Impact

- 受影响代码：`web/src/routes/root-layout.tsx`、`web/src/components/layout/SideNav.tsx`、`web/src/components/layout/PreviewColumn.tsx`、`web/src/features/ui/store.ts`（如需新增折叠/宽度状态字段）、`web/src/features/tasks/queries.ts` + `api.ts`（list 读取的静默选项）、`web/src/features/tasks/mutations.ts`（list 前缀失效）。
- 测试：`SideNav.test.tsx`（既有 3 个用例需包 `QueryClientProvider` wrapper——SideNav 引入 query 后否则直接崩）、`PreviewColumn.test.tsx`、`router.test.tsx` 等外壳测试需随结构调整更新；Recents 列表复用既有 MSW task-list handler（已存在，空态/错误态用 `server.use()` 覆盖）。
- 无 API / Worker / MQ / DB 影响；无新增依赖（头像用现有 shadcn 基座实现即可）。
- 文档：`docs/ARCHITECTURE.md` §3.1（前端模块，三栏外壳要点）与 §2.2（组件职责表）需同步——注意外壳描述不在 §4.3（那是任务状态机）。
