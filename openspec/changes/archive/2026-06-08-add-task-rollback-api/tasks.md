## 1. Queries

- [x] 1.1 Add `SwitchTaskCurrentVersion :exec` to `api/queries/tasks.sql`: `UPDATE tasks SET current_version = $2, updated_at = now() WHERE id = $1` — pointer + timestamp only, NO status write (contrast with `UpdateTaskCurrentVersion` which seeds `status='pending'`)
- [x] 1.2 Widen `LockTaskForControl` in `api/queries/tasks.sql` to also `SELECT task_type` (branch needs it for the execute payload/topic; this avoids the unscoped `GetTaskByID` re-read the iterate flow uses). `task-control-api`'s `Apply` ignores the new column — non-breaking.
- [x] 1.3 Run `make sqlc`; confirm the generated `SwitchTaskCurrentVersion` method + the widened `LockTaskForControlRow.TaskType` exist and `make sqlc` reports no further diff

## 2. Domain: RollbackTask

- [x] 2.1 In `domain/task/service.go` add `RollbackMode` (`branch`/`switch`) + `IsValidRollbackMode`, `RollbackInput{TaskID, TargetVersionID, Mode, Prompt, Params, Lane}`, `RollbackOutput{VersionID, VersionNo, Status, Mode}` (switch echoes the existing current version; branch echoes the new one)
- [x] 2.2 Add `RollbackTask(ctx, owner Owner, in RollbackInput)`: begin tx; `LockTaskForControl(owner, taskID)` → `ErrTaskNotFound` on miss (the locked row now carries `task_type` + `status` + `current_version`); non-active guard (`IsActive(locked.status)` → `ErrActiveVersionExists` enriched via `GetActiveVersionByTask`); `GetVersionByTaskAndID(target, taskID)` → `ErrVersionNotFound` on miss
- [x] 2.3 Branch path: auto-fill `prompt` to `"rollback to version <target.version_no>"` when empty, THEN `validatePrompt`/`validateParams`/`resolveLane`; `versionNo = MaxVersionNoForTask+1`; call `createActiveVersion(parentVersionID=target, parentArtifactRoot=target.artifact_root, taskType=locked.task_type, ...)` (use `locked.task_type` — do NOT re-read via the unscoped `GetTaskByID`); `UpdateTaskCurrentVersion(status='pending')`; commit; return the new version
- [x] 2.4 Switch path: assert the target is terminal via the row's generated `is_active` column (`target.IsActive == true` → `ErrInvalidState` → 409 invalid_state); `SwitchTaskCurrentVersion(taskID, target)` (no status write, no run, no outbox); commit; return the target as current with the status from the **already-locked row** (no post-update re-SELECT)
- [x] 2.5 Domain unit tests (fake/stub queries or the integration harness): branch happy (version+run+outbox, current_version moved, status pending); branch on active → `ErrActiveVersionExists`; switch happy (only current_version moves; NO new version/run/outbox; status untouched); **switch target active/non-terminal (incl. a `cancelling` version) → `ErrInvalidState`**; unknown target → `ErrVersionNotFound`; unowned task → `ErrTaskNotFound`; invalid mode → invalid_input

## 3. Application layer

- [x] 3.1 In `application/task/` add `RollbackTaskCommand{TaskID, Owner, TargetVersionID, Mode, Prompt, Params, Lane}` and `Service.RollbackTask` thin wrapper delegating to the domain service (mirrors `IterateTask`)

## 4. HTTP handler

- [x] 4.1 Add the rollback handler (request/response types `RollbackTaskRequest{target_version_id, mode, prompt, params, lane}` / branch `201 {version_id, version_no, status}` / switch `200 {current_version_id, version_no, status}`); parse + validate `task_id`/`target_version_id` UUIDs and `mode` (400 invalid_input); read principal via `principalOrAbort`; register `POST /tasks/:task_id/rollback` in `TaskHandlers.Register`
- [x] 4.2 Reuse `handleError`/`MapError` so `active_version_exists` carries its `{active_version_id, active_version_status}` data block; add a `tasks_rolled_back_total{mode, outcome}` counter that EXTENDS the single-label `tasks_iterated_total{outcome}` pattern with a `mode` label (wire in `observability/metrics.go`). Increment on EVERY exit path (mirroring the iterate handler's parse-error labelling); pre-`mode`-parse `400`s use `mode="unknown"`
- [x] 4.3 HTTP contract tests: branch 201 shape; switch 200 shape; 400 (bad mode / missing target / bad uuid); 404 task_not_found (unowned) + version_not_found; 409 active_version_exists (with data block) + 409 invalid_state (switch to a non-terminal target)

## 5. Integration test + docs

- [x] 5.1 Integration test (testcontainers, `interfaces/http/*_integration_test.go` or domain): branch writes exactly one `execute` outbox row (carrying parent artifact root) and moves current_version; switch writes NO outbox row and leaves `tasks.status` unchanged while moving current_version
- [x] 5.2 Reconcile `docs/ARCHITECTURE.md §6.5`: note that `switch` requires a non-active task (status-sync safety) and writes only `current_version` (not `tasks.status`) — so the doc reflects the implemented guard rather than the looser "不受互斥约束" wording (AGENTS §1)

## 6. Gates

- [x] 6.1 From `api/`: `go vet ./...`, `golangci-lint run ./...` (0 issues, gocritic strict incl. tests), `go test -race ./...`, `make sqlc` (no diff), `make test-integration`
- [x] 6.2 `gofmt -w` only the touched files
- [x] 6.3 `openspec validate add-task-rollback-api --strict` from repo root passes
