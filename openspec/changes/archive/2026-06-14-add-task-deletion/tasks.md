## 1. 数据模型与迁移

- [x] 1.1 迁移：`tasks` 加列 `deleted_at TIMESTAMPTZ`（可空，默认 NULL）
- [x] 1.2 迁移：加部分索引 `(tenant_id, user_id, status) WHERE deleted_at IS NULL`（保留列表索引性能）
- [x] 1.3 读过滤：`ListTasks`/`CountTasks` 加 `AND deleted_at IS NULL`（SQL）。**`GetTaskByID` 保持不变**（iterate 也用它且把 not-found 包成 500）——改在 read_service Go 层加 `DeletedAt.Valid` 守卫：`GetTask`、`ownedTask`、`ownedVersion` 三个 choke point，一处覆盖 detail、versions、version-by-id 与实时 `OwnsTask`/`OwnsVersion`，且不动 iterate 写路径
- [x] 1.4 新增软删除 query `SoftDeleteTask`：`UPDATE tasks SET deleted_at=now(), updated_at=now() WHERE id=$1 AND deleted_at IS NULL`（RETURNING/RowsAffected 用于幂等判定）

## 2. 后端领域与用例（api/，禁裸 UPDATE）

- [x] 2.1 domain：`SoftDeleteTask(ctx, owner, taskID)` 状态机方法（单事务）——复用 `LockTaskForControl`（owner-scoped + FOR UPDATE，无行→`ErrTaskNotFound`）→ `IsActive(status)` 则 `GetActiveVersionByTask` 构造 `ErrActiveVersionExists` → 否则执行 `SoftDeleteTask` exec（`WHERE deleted_at IS NULL` 影响 0 行→`ErrTaskNotFound`，保证幂等）
- [x] 2.2 application：delete 用例编排（鉴权主体 → domain 方法 → 映射结果/错误）
- [x] 2.3 可观测性：`slog`（`trace_id`/`task_id`）+ metric `task_deleted_total`

## 3. 后端 HTTP 接口

- [x] 3.1 `interfaces/http/task_delete.go`：`DELETE /api/v1/tasks/{task_id}` handler——owner-scoped；成功 200 统一 envelope；活跃 → 409 `active_version_exists`（`data` 带 active_version_id/status）；不存在/非本人/已删 → 404 `task_not_found`
- [x] 3.2 路由注册（与现有 task 路由一致的中间件/鉴权）

## 4. 后端测试

- [x] 4.1 domain 单测：软删除成功（deleted_at 置位、status 不变、versions/cost 行保留）；活跃任务拒绝；owner 不匹配拒绝；幂等（二次软删除影响 0 行）
- [x] 4.2 HTTP 契约/集成测试：200 首删；409 active（含 data 形状）；404 `task_not_found`（不存在/非本人/已删，三者同义不泄露）；删除后 list 不含（含 `total`）、detail 404、versions 404、**version-by-id `/versions/{id}` 404**
- [~] 4.3 实时鉴权：`OwnsTask`/`OwnsVersion` 包装 `ownedTask`/`ownedVersion`（已加 deleted 守卫），由 versions-list-404 与 version-by-id-404 集成测试**间接覆盖**同一守卫路径；未写专门断言 `OwnsTask` 的 WS 测试（可后续补）

## 5. 前端数据访问与变更

- [x] 5.1 `features/tasks/api.ts`：`deleteTask(id)`（`method: "DELETE"`）
- [x] 5.2 `features/tasks/mutations.ts`：`useDeleteTaskMutation`——成功失效 `taskKeys.lists` 前缀（+ 详情场景失效该 task detail）；错误透传 `ApiError`
- [x] 5.3 把 `TaskDetail.tsx` 局部的 `isConflictData` 上移到 `features/tasks/types.ts`（紧邻 `ActiveVersionConflict`），TaskList/TaskDetail 共用

## 6. 前端 UI

- [x] 6.1 Vendoring shadcn `AlertDialog` 进 `components/ui/`（`components/ui/` 现无确认原语，已核实）+ 装 `@radix-ui/react-alert-dialog`；保持 Tailwind-4 兼容形态（cva + token 类，无裸色）
- [x] 6.2 TaskList 行内删除按钮（`data-testid` 稳定）+ 确认；活跃任务 disabled + title 原因
- [x] 6.3 TaskDetail 头部控制条删除按钮 + 确认；活跃禁用；成功 `navigate('/tasks')`
- [x] 6.4 错误处理：`active_version_exists` → warning toast；404 → 视为成功/no-op，不报错 toast

## 7. 前端测试

- [x] 7.1 契约测试（MSW）：确认后才发 DELETE；成功失效 lists（列表移除）；活跃任务按钮 disabled+原因；详情删除后导航回列表；409 warning 不乐观移除；既有 testid 不破

## 8. 规格与验收

- [x] 8.1 4 份 spec delta（task-delete-api 新增 + data-model/read-api/web-tasks-pages）与落地一致
- [x] 8.2 后端：`go test ./...` 全绿（含新 domain/HTTP 测试）
- [x] 8.3 前端：`npm run typecheck && npm run lint && npm run test && npm run build` 全绿
- [x] 8.4 手动验证：列表/详情删除、活跃禁用、确认、删除后消失 + Recents 刷新
