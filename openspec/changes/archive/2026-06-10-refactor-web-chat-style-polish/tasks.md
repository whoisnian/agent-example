## 1. 前置（依赖 add-task-title-autogen 已落地）

- [x] 1.1 `features/tasks/types.ts`：`CreateTaskRequest.title` 改可选；确认 MSW mock handler 接受无 title 的创建请求

## 2. SideNav 与全局状态

- [x] 2.1 vendored 添加 shadcn `dropdown-menu` 组件（`components/ui/dropdown-menu.tsx` + Radix 依赖）
- [x] 2.2 `features/ui/store.ts`：移除 `navCollapsed` / `toggleNav` / `setNavCollapsed`；同步 store 单测
- [x] 2.3 `SideNav.tsx`：移除折叠 toggle 与全部 collapsed 分支，固定 `w-56`；user-area 改为 DropdownMenu 触发器（菜单含 Tasks/Cost/Settings/Logout，激活路由选中态；`nav-tasks`/`nav-cost`/`nav-settings`/`logout-button` testid 迁入菜单）
- [x] 2.4 `root-layout.tsx`：去除 nav 宽度切换逻辑；`SideNav.test.tsx` / `router.test.tsx` 同步（折叠用例删除、菜单用例新增）

## 3. New task 聊天式页面

- [x] 3.1 `TaskCreate.tsx` 重写：居中问候标题 + composer 卡片（textarea + 提交钮 + Ctrl/Cmd+Enter）+ task_type chips（`task-type-chip`）+ Advanced 折叠区（params/lane）；不发送 `title`
- [x] 3.2 错误语义保持：params 本地 JSON 校验、`invalid_input` 行内展示、提交失败保留输入；`TaskCreate.test.tsx` 重写（`title-input` / `task-type-select` 用例退役）

## 4. Tasks 页面

- [x] 4.1 `TaskList.tsx`：移除 `new-task-button` 及其测试断言；空态文案不再指向页内按钮

## 5. 任务详情对话化

- [x] 5.1 `EventLog.tsx`：包裹为助手消息块（左对齐气泡 + 助手图标）；status 事件人话化、error 事件 destructive 配色、其余 kind + payload 摘要；`event-log`/`event-row`/`event-log-empty` testid 保留，测试更新
- [x] 5.2 `ConversationTurn.tsx`：`TurnArtifacts` 行改卡片（图标 + kind 标题 + mime·size 副行 + Download；整卡点击区走 `selectArtifact`）；`turn-artifact-*` testid 保留，测试更新

## 6. 预览面板

- [x] 6.1 `ArtifactPreviewPanel.tsx`：产物行 padding 移入 select button 使命中区覆盖整行；Render/Source 切换改 outline 变体 + 图标（Code/Eye）+ 文案；testid 不变，测试补点击区与按钮形态断言

## 7. 验收

- [x] 7.1 `npm run typecheck && npm run lint && npm run test && npm run build` 全绿
- [x] 7.2 `openspec validate refactor-web-chat-style-polish` 通过
- [x] 7.3 对照两张参考图人工核对：SideNav 结构、New task 页、详情页事件气泡与产物卡片、右栏切换按钮
  - 2026-06-11 本地 dev 栈人工核对通过（用户确认）：SideNav 菜单 / New task composer / 产物卡片驱动预览 / Render-Source 切换均符合预期
