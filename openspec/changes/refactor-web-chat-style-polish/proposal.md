## Why

对照 Claude.ai 参考图（`Screenshot_new_chat.png` 新会话页、`Screenshot.png` 会话页），上一轮三栏/对话回合改造后仍有四处体验差距：左导航的折叠按钮与平铺主导航与参考图不符；New task 页面仍是文档式表单而非聊天输入框；任务详情的执行过程（EventLog）仍是裸日志行而非对话气泡；右栏产物行点击区域过小、HTML 渲染/源码切换按钮不显眼。

## What Changes

- **SideNav**：移除 `nav-collapse-toggle` 折叠按钮与左导航折叠态（`navCollapsed` 全局状态退役）；`Tasks` / `Cost` / `Settings` 主导航项从平铺列表移入底部 user-area 的弹出菜单（头像/用户行触发，含 Logout）。导航主体变为：brand → New task → Recents → 用户区。
- **New task 页面**（`/tasks/new`）：改为聊天式创建入口——居中问候标题 + 大输入卡片（多行 prompt + 提交），task_type 以 chips 选择，params/lane 收进"高级选项"折叠区；**移除 title 输入**（依赖 `add-task-title-autogen`，标题由后端派生）。
- **Tasks 页面**：移除右上角 "New task" 按钮（入口收敛到 SideNav）。
- **任务详情页**：当前回合的事件日志改为**助手消息形态**（左侧对话气泡内的可读事件流，替代裸 mono 日志行）；回合内产物列表改为**卡片形态**（图标 + kind 标题 + mime/size 副行 + Download），点击卡片展开右栏预览（既有 store 成对写入语义不变）。
- **预览面板**：产物行的选择点击区域扩展到整行高度（Download 按钮除外）；HTML Render/Source 切换改为带图标的明显按钮。
- **BREAKING（UI 契约）**：`nav-collapse-toggle`、`new-task-button`（TaskList）、`nav-tasks`/`nav-cost`/`nav-settings`（平铺导航）等 testid 退役或迁移到菜单内；`event-log` 行呈现语义改变。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `web-bootstrap`: "Application Shell" —— 左导航不再折叠（移除折叠 toggle 与 icon rail），主导航入口移入 user-area 弹出菜单，响应式降级仅保留右栏 drawer。
- `web-tasks-pages`: "Task Create Page" 改聊天式输入卡片（无 title 字段）；"Task List Page" 移除页内 New task 按钮；"Task Detail Page" 事件日志改助手消息形态；"Version Artifacts Expandable List With Direct Download" 更名并改为卡片形态（REMOVED+ADDED，兑现归档备忘的旧名清理）。
- `web-artifact-preview`: "Artifact Preview Panel" 产物行整行可点；"Lightweight Artifact Content Preview" 渲染/源码切换改为带图标按钮。

## Impact

- **代码**：`components/layout/SideNav.tsx`（+ 新 user-area 菜单组件）、`features/ui/store.ts`（移除 `navCollapsed`/`toggleNav`/`setNavCollapsed`）、`routes/root-layout.tsx`（去折叠宽度切换）、`routes/TaskCreate.tsx`（重写为 composer）、`routes/TaskList.tsx`、`components/tasks/EventLog.tsx`、`components/tasks/ConversationTurn.tsx`（产物卡片）、`features/artifacts/ArtifactPreviewPanel.tsx`（行点击区 + 切换按钮）、对应全部测试。
- **依赖**：`add-task-title-autogen` 必须先落地（create 请求不再发 title）；`features/tasks/types.ts` 的 `CreateTaskRequest.title` 改可选并停止发送。
- **不变**：路由结构、React Query/WS 数据访问、iframe 沙箱与 CSP 红线、任务级互斥 UI 语义。
