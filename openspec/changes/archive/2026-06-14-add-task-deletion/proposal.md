## Why

用户目前无法删除任务——列表里失败/废弃/试验性的任务会一直堆积，没有清理手段。本 change 增加任务删除能力（后端 `DELETE` 路由 + 前端按钮），采用**软删除**：任务从列表/详情隐藏，但保留 `task_versions` / `cost_events` 等行供审计与成本结算完整性（不触 `AGENTS.md §6` 成本红线）。

## What Changes

- **新增 `DELETE /api/v1/tasks/{task_id}`**（owner-scoped、软删除、幂等）：在 `tasks` 上写 `deleted_at = now()`，返回统一 envelope。
  - **活跃任务保护**：任务存在 `is_active` 版本时，删除 MUST 失败并复用既有 `active_version_exists` 冲突错误（与 iterate/rollback 一致），提示先取消。
  - **幂等**：对已软删除（或不存在/非本人）的任务，按 owner-scoped 读规则返回 `404 task_not_found`，不泄露存在性。
- **数据模型**：`tasks` 增加可空列 `deleted_at TIMESTAMPTZ`；所有 owner-scoped 读（list/detail/versions）与列表索引按 `deleted_at IS NULL` 过滤。`status` CHECK 枚举**不变**（软删除走 `deleted_at`，不引入 `deleted` 状态）。
- **读 API**：`GET /tasks` 列表排除软删除任务（`total` 同步排除）；`GET /tasks/{id}`、`/tasks/{id}/versions` 对软删除任务返回 `404 task_not_found`（与未拥有同义，不泄露）。
- **前端**：TaskList 行内 + TaskDetail 头部增加删除按钮（`data-testid` 稳定），带确认弹窗；活跃任务按钮 disabled 并给原因（复用 `task.status` 活跃判定）；删除成功 toast + 失效 `taskKeys.lists` 前缀（列表/Recents 刷新），TaskDetail 删除后导航回列表。

**非目标（明确排除）**：硬删除 / 级联删除 `task_versions`/`artifacts`/`cost_events` 行或 OSS 对象（保留供审计与结算）；批量删除；恢复（undelete）UI（`deleted_at` 为恢复预留但本次不做入口）；worker 侧改动（软删除不需要 OSS 清理）。**CostDashboard / 成本聚合口径不改**——软删除任务的成本**仍计入**租户/组织级聚合（§6 结算完整性，刻意为之；如需在面板排除另起 change）；list 行内的 per-task 成本因列表已过滤而自然不显示。

## Capabilities

### New Capabilities
- `task-delete-api`: `DELETE /api/v1/tasks/{task_id}` 的契约——owner-scoped、软删除、活跃任务冲突（`active_version_exists`）、幂等 404、统一 envelope 与状态机方法（禁裸 UPDATE）。

### Modified Capabilities
- `task-data-model`: **MODIFY** `Tasks Table`——列表加 `deleted_at TIMESTAMPTZ`（可空），并把 `(tenant_id,user_id,status)` 全索引**替换**为 `WHERE deleted_at IS NULL` 的部分索引（迁移 drop 旧 + create 新）；`status` 枚举不变。
- `task-read-api`: **MODIFY** `List Tasks Endpoint`（items/total 排除软删除）、`Task Detail Endpoint` 与 `Version List (Tree) Endpoint`（软删除任务及其下版本读 → `404 task_not_found`，含 `/versions/{id}`）。
- `web-tasks-pages`: **ADD** `Task Deletion Control`——TaskList/TaskDetail 删除按钮 + 确认 + 活跃禁用 + 成功失效 lists 前缀 + 详情删除后回列表。

## Impact

- **DB/迁移**：`tasks` 加列 `deleted_at` + 迁移；sqlc 查询（list/get/versions 加 `WHERE deleted_at IS NULL`，新增软删除 UPDATE）。
- **后端（api/，Golang）**：`interfaces/http`（新 `task_delete.go` handler + 路由注册）、`application`（delete 用例）、`domain`（任务软删除状态机方法，禁裸 UPDATE，活跃校验复用现有 active-version 检查）、`infrastructure`（sqlc 生成）。错误走统一 `{code,message,trace_id}`；日志带 `trace_id`/`task_id`。
- **前端（web/）**：`features/tasks/api.ts`（`deleteTask`）、`mutations.ts`（`useDeleteTaskMutation`，失效 `taskKeys.lists`）、`TaskList.tsx` + `TaskDetail.tsx`（按钮/确认/禁用/导航）、契约测试（MSW）。**确认弹窗需 vendoring shadcn `AlertDialog`**（`components/ui/` 当前无确认原语，已核实）——新增依赖 `@radix-ui/react-alert-dialog`（Tailwind-4 兼容形态）。把 route-local 的 `isConflictData`（现于 `TaskDetail.tsx`）上移到 `features/tasks/types.ts` 供 TaskList/TaskDetail 共用。
- **测试**：domain 单测（软删除、活跃拒绝、owner 校验）+ HTTP 契约测试（200/404/409-active）+ 前端契约测试（按钮、确认、禁用、失效、导航）。
- **规格**：新 `task-delete-api` + `task-data-model`/`task-read-api`/`web-tasks-pages` delta。
- **依赖关系**：独立 change，不依赖设计系统三部曲。
- **PR 体量（§7 ~500 行）**：偏大且可拆——后端（迁移+sqlc+domain+handler+测试）与前端（AlertDialog vendoring+按钮+测试）是各自自洽的单元。落地时**后端先行**（建立 API 契约），前端随后；若单 PR 超限则拆成 `…-api` 与 `…-web` 两个 change。
