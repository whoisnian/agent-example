## Context

上一轮（`refactor-web-shell-layout` + `refactor-web-conversation-rich-preview`）落地了三栏重心、SideNav 信息结构、对话回合流与 HTML 富预览。本轮按参考图消除剩余差距。约束：shadcn/ui + CSS 变量主题（深色保持）、React Query/Zustand 边界、iframe 沙箱红线、既有 `data-testid` 契约尽量保留。依赖 `add-task-title-autogen`（API 端 title 可选）先行。

## Goals / Non-Goals

**Goals:**
- 左导航贴近参考图：无折叠按钮，主导航收进用户区菜单，Recents 成为导航主体。
- New task 页与会话页风格统一（聊天输入卡片），创建即进入对话。
- 执行过程以助手消息形态呈现；产物以卡片呈现并驱动右栏预览。
- 右栏交互可达性：整行点击、可见的视图切换按钮。

**Non-Goals:**
- 不做主题暖浅色化与圆角/衬线视觉微调（遗留差距 1/6，另轮处理）。
- 不改实时/轮询管线、互斥语义、回滚语义、CSP 与 iframe 沙箱策略。
- 不实现移动端专门布局（左导航固定宽度即可，窄屏由右栏 drawer 兜底）。

## Decisions

### D1 折叠功能整体退役，而非只藏按钮

`navCollapsed` / `toggleNav` / `setNavCollapsed` 从 `features/ui/store` 删除，`SideNav` 固定 `w-56`，`RootLayout` 去掉宽度切换。理由：折叠态没有入口即为死代码，留着会让"折叠态隐藏 Recents"等分支永不可达；store 是公共全局状态，按 AGENTS 必须过提案（本变更即是）。窄屏降级仅保留右栏 drawer（既有行为），中栏可用性由其保证。

### D2 user-area 菜单用 shadcn DropdownMenu，vendored 按需添加

头像/用户行整体作为触发器，菜单项：Tasks / Cost / Settings / 分隔线 / Logout。组件 vendored 到 `components/ui/dropdown-menu.tsx`（Radix 依赖随 shadcn 惯例进 package.json）。菜单项保留 `nav-tasks` / `nav-cost` / `nav-settings` testid（迁移位置、语义不变），Logout 保留 `logout-button`。激活路由的菜单项渲染选中态；导航激活高亮的主要载体变为 Recents 行（既有行为）。

### D3 New task = 居中 composer，复用既有 mutation 与错误语义

布局：垂直居中问候标题（如 "What should we build?"）+ 输入卡片（自适应高度 textarea + 右下提交按钮）+ 卡片下方 task_type chips（`code-gen` / `research`，单选，默认 `code-gen`）+ "Advanced" 折叠区（params JSON、lane，沿用既有行内校验/错误展示）。请求体不再含 `title`（`CreateTaskRequest.title` 改可选并停止发送）。成功后导航到详情页——首回合即对话延续，风格闭环。`Cmd/Ctrl+Enter` 提交，与详情页 composer 一致。保留 `task-create-page`、`prompt-input`、`params-input`、`lane-input`、`submit-button`、`form-error` testid；`title-input` 退役；`task-type-select` 由 chips testid（`task-type-chip`）取代——原生 select 退役理由：chips 是按钮组，测试用 click 即可，不违反"保留原生 select"惯例的初衷（那条针对筛选器）。

### D4 EventLog 改助手消息形态，保持数据形状不动

当前回合内事件流包裹为助手消息块（左对齐、`bg-muted` 圆角气泡、助手图标），每条事件渲染为可读行：`status` 事件显示人话（"Status → running"）、`error` 事件红色显示 code/message、其余 kind 显示 kind + payload 摘要（沿用 200 字符截断）。`event-log` / `event-row` testid 保留，`event-log-empty` 保留。不引入 markdown 渲染（事件 payload 是 JSON，不是富文本——Post-MVP 若 Worker 发文本增量再升级）。

### D5 产物卡片：ConversationTurn 内由行改卡

卡片 = `FileText` 图标 + kind 标题行 + mime · size 副行 + 右侧 Download 按钮；整卡（除 Download）为选择点击区，点击走既有 `selectArtifact(versionId, artifactId)` 成对写入。`turn-artifact-item` / `turn-artifact-select` / `turn-artifact-download` testid 保留。卡片栅格：单列堆叠（中栏窄），不做多列。

### D6 预览面板：行点击区拉满 + 显眼切换按钮

- 产物行：把 padding 从外层 div 移到 select button 本身，使 `artifact-select` 命中区覆盖整行高度；Download 仍是独立按钮。testid 不变。
- HTML 视图切换：ghost 文本钮改为 `outline` 变体、带图标的双态按钮（render 视图下显示 `<Code> Source`，source 视图下显示 `<Eye> Render`），`preview-view-toggle` testid 保留，切换语义/keyed remount 不变。

## Risks / Trade-offs

- [移除折叠后超窄屏左导航占 224px] → MVP 接受；右栏 drawer 化已保住中栏，移动端布局列为 Non-Goal。
- [chips 取代原生 select 需要同步改 TaskCreate 测试] → 测试同 PR 内更新；TaskList 的筛选器原生 select 不动。
- [user-area 菜单把主导航藏了一层] → 参考图同款交互；高频入口（New task、Recents）仍一层直达，Tasks 列表可从 Recents "查看全部"或菜单进入。
- [DropdownMenu 引入 Radix 依赖] → shadcn 惯例 vendoring，锁小版本；不影响既有组件。

## Open Questions

（无——title 派生策略已由 `add-task-title-autogen` 决策。）
