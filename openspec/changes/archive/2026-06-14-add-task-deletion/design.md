## Context

后端（`api/`，Golang，分层 interfaces/application/domain/infrastructure，sqlc + Outbox）与前端（`web/`，React Query + Zustand）均已实现。任务领域：`tasks` → `task_versions`（`is_active` 生成列 + `one_active_version_per_task` 唯一部分索引做互斥）→ `task_runs`，外加 `artifacts`（OSS）与 `cost_events`。已有任务变更 API：write（create/iterate）、rollback、control，均 owner-scoped、统一 envelope、活跃冲突用 `active_version_exists`（HTTP 409，`data` 带 `active_version_id`/`active_version_status`）。读 API owner-scoped 且对未拥有资源返回 404 不泄露。

用户诉求：删除任务 + 前端按钮。已定方向：**软删除** + **禁止删除活跃任务**。

## Goals / Non-Goals

**Goals:**
- `DELETE /api/v1/tasks/{task_id}`：owner-scoped、软删除、幂等、活跃任务拒绝。
- 软删除任务从所有 owner-scoped 读中消失（list/detail/versions）。
- 前端：列表与详情删除按钮 + 确认 + 活跃禁用 + 列表失效 + 详情删除后回列表。
- 成本/审计完整性：不删 `cost_events`/`task_versions` 行。

**Non-Goals:**
- 硬删除 / 级联删行 / OSS 清理 / worker 改动。
- 批量删除、恢复 UI。
- 改 `status` 枚举或互斥规则。

## Decisions

### D1 · 软删除用 `tasks.deleted_at TIMESTAMPTZ`（可空），不改 `status`
活跃判定、状态机、互斥都建立在 `status`/`is_active` 上；引入 `status='deleted'` 会污染这些不变式与 CHECK 枚举。用独立 `deleted_at` 列正交表达删除，`NULL` = 未删除。
- **可见性（落地：list 走 SQL，其余走 Go 守卫）**：list/count 在 SQL 加 `AND deleted_at IS NULL`（`ListTasks`/`CountTasks`）。`GetTaskByID` **保持不变**——它被 iterate 写路径复用（`service.go:276`，且把 not-found 包成 500），改它会让 iterate-on-deleted 变 500。改为在 `read_service` 的三个 choke point（`GetTask`、`ownedTask`、`ownedVersion`）`owns()` 校验后加 `if t.DeletedAt.Valid → ErrTaskNotFound/ErrVersionNotFound`，一处覆盖 detail、versions、version-by-id 及实时 `OwnsTask`/`OwnsVersion`（后两者包装 `ownedTask`/`ownedVersion`），软删除任务的 WS 订阅随之停止授权；iterate 写路径不受影响。
- **索引**：把 `(tenant_id,user_id,status)` 全索引替换为 `WHERE deleted_at IS NULL` 部分索引（迁移 drop+create），listing 走它。
- **Alternative（弃）**：`status='deleted'` —— 撞 CHECK 枚举、污染活跃/互斥语义。

### D2 · 活跃任务拒绝，复用 `active_version_exists`（具体机制）
复用 rollback/control 的既有三步惯用法（不是单个共享函数）：owner-scoped 加锁读 → `IsActive(status)` 闸 → 取活跃版本构造冲突。具体：
- `LockTaskForControl(ctx, taskID, owner)`（`tasks.sql.go:163`，`FOR UPDATE` + inline owner 谓词，返回 `status`/`current_version`；无行 → `ErrTaskNotFound`）；
- `IsActive(locked.Status)` 为真 → `GetActiveVersionByTask(ctx, taskID)`（`querier.go:46`）取 `{id,status}` → 构造 `&ErrActiveVersionExists{...}`（HTTP 409，`data` 带 `active_version_id`/`active_version_status`）。
- **前端**：`active_version_exists` 已被消费，但其判别器 `isConflictData` 现是 `TaskDetail.tsx` 的**局部**函数；本 change 把它上移到 `features/tasks/types.ts`（紧邻 `ActiveVersionConflict`）供 TaskList/TaskDetail 共用（见 D5）。
- **理由**：语义最简、与既有互斥护栏一致、前端零新增错误分支。不绕过互斥唯一索引（§6 红线）。

### D3 · 幂等 + 不泄露
删除走 owner-scoped：任务不存在、非本人、或已软删除 → 一律 `404 task_not_found`（与 read 的 owner-scoped 隐藏一致）。重复删除返回 404（幂等：第二次看不到该任务）。成功首删返回 `200`（统一 envelope，`data` 可为 `{id}` 或 null——design 落地时与现有 control/rollback 响应风格对齐，倾向 `{deleted: true}` 或 200 空 data）。
- **不泄露**：404 不区分"不存在/非你的/已删除"。

### D4 · 状态机方法，禁裸 UPDATE（复用 LockTaskForControl）
软删除走 domain service 方法 `SoftDeleteTask(ctx, owner, taskID)`，单事务内：
1. `LockTaskForControl`（owner-scoped + `FOR UPDATE`，无行 → `ErrTaskNotFound`）——一次加锁读同时服务 404 与活跃校验，**不另写 owner 谓词**，与 control/rollback 一致；
2. `IsActive(status)` → 有活跃版本则 `ErrActiveVersionExists`（D2）；
3. 否则执行新 sqlc query `SoftDeleteTask`：`UPDATE tasks SET deleted_at=now(), updated_at=now() WHERE id=$1 AND deleted_at IS NULL`。
- **幂等关键**：`LockTaskForControl` 当前**不**过滤 `deleted_at IS NULL`——已删任务仍会被锁到，然后第 3 步的 `WHERE deleted_at IS NULL` 影响 0 行，据此返回 `ErrTaskNotFound`（幂等：二次删除 → 404，时间戳不变）。
- 不在 handler 拼 SQL（§4.1）。删除不跨服务边界（无 MQ 投递），**不经 Outbox**。
- **可观测性**：`slog`（`trace_id`/`task_id`）+ metric `task_deleted_total`。

### D5 · 前端：按钮位置与交互
- **TaskList 行内**：每行尾列加删除按钮（图标，`data-testid="task-delete-{id}"` 或行内 `task-delete`），点击弹确认（复用现有弹窗/AlertDialog；若无则用最小确认——倾向 shadcn `AlertDialog`，按需 vendoring 并在 Impact 记）。活跃任务（`isActiveStatus(status)`）按钮 disabled + title 给原因。
- **TaskDetail 头部控制条**：加删除按钮，活跃禁用，删除成功后 `navigate('/tasks')`。
- **失效**：删除 mutation 成功失效 `taskKeys.lists` 前缀（TaskList + SideNav Recents 刷新）；详情页另失效该 task 的 detail。
- **错误**：`active_version_exists` → warning toast "先取消活跃版本"；404（已删）→ 静默或 info（视为已达成）。
- **无 404 refetch 循环**（已核实）：详情删除后 `navigate('/tasks')` 卸载 TaskDetail，停掉 `useTaskQuery`；即便不导航，`liveRefetchInterval` 仅在 `isActive && WS 未开` 时轮询（可删任务非活跃，故不轮询），且 `useTaskQuery` 对 404 已禁重试——无循环。

## Risks / Trade-offs

- **[软删除任务的成本仍计入聚合]** → **有意**（审计/结算完整性，§6），已列入 Non-Goals。list 行内 per-task 成本因列表过滤自然不显示；CostDashboard 租户/组织级聚合**仍含**软删除任务（用户无 UI 痕迹）——如需排除另起 change。
- **[列表性能：加 `deleted_at IS NULL` 过滤]** → 用部分索引 `(tenant_id,user_id,status) WHERE deleted_at IS NULL` 保持列表查询走索引。
- **[活跃任务删除竞态]**（检查后、UPDATE 前版本变活跃）→ 软删除 UPDATE 不破坏互斥（不动 task_versions）；活跃版本仍在跑，worker 只写 cost/checkpoint/heartbeat，不写 tasks 主表（§4.2），删除只是隐藏任务。可接受：极端竞态下任务被隐藏但其活跃版本自然跑完/被 reaper 收尾。落地时活跃校验与 UPDATE 同一事务，降低窗口。
- **[前端 AlertDialog 未 vendored]** → 已核实 `components/ui/` **无**确认原语，故 vendoring shadcn `AlertDialog`（+ `@radix-ui/react-alert-dialog`，Tailwind-4 兼容形态）是**必做**项，非可选；记入 Impact 与 tasks。
- **[已删任务的直链]** → 用户保存的 `/tasks/{id}` 在删除后返回 404 detail → 复用现有 `task-not-found` 渲染态。

## Open Questions

- 删除成功响应体形状（`200 {deleted:true}` vs 空 data）—— 落地时与现有 rollback/control 响应风格对齐拍板。
- 列表过滤用部分索引还是查询内过滤 —— 迁移时按 explain 实测定（倾向部分索引）。
- TaskList 删除按钮是图标还是行 hover 显隐 —— 落地视觉细节，不影响契约（testid 稳定即可）。
