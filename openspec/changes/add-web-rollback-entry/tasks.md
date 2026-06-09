## 1. Client layer (DTOs + api + mutation)

- [ ] 1.1 Add rollback DTO mirrors to `features/tasks/types.ts`: `RollbackMode` (`"branch"|"switch"`), `RollbackTaskRequest` (`target_version_id`, `mode`, optional `prompt`/`params`/`lane`), `RollbackBranchResponse` (`{version_id, version_no, status}`), `RollbackSwitchResponse` (`{current_version_id, version_no, status}`)
- [ ] 1.2 Add `rollbackTask(taskId, body)` wrapper to `features/tasks/api.ts` with `toastOnError:false` (mirror `iterateTask`)
- [ ] 1.3 Add `useRollbackTaskMutation` to `features/tasks/mutations.ts`: keyed on `RollbackVars { taskId, body }` (like `IterateVars`/`ControlVars`), data typed `RollbackBranchResponse | RollbackSwitchResponse`, `meta:{silent:true}`, `onSettled` invalidates `taskKeys.detail(taskId)` + `taskKeys.versions(taskId)` (mirror `useIterateTaskMutation`). The mode is carried by `vars.body.mode`, NOT the response (the two response bodies share no discriminator)

## 2. UI: per-version rollback control

- [ ] 2.1 Create `components/tasks/RollbackControl.tsx` — presentational: props `{ branchDisabled, branchReason?, switchDisabled, switchReason?, pending, onRollback(mode, prompt?) }` (the leaf control has no version id); inline disclosure with a Switch action and a Branch action that reveals an optional prompt textarea; disabled buttons explain via `title` (mirror `ControlBar` split). Stable testids: `rollback-button`, `rollback-switch`, `rollback-branch`, `rollback-prompt`, `rollback-submit`
- [ ] 2.2 Wire `RollbackControl` into each non-current `VersionTree` row: pass `branchDisabled = task active`, `switchDisabled = task active || row.is_active`, with the matching reasons; never render it on the current version row. Add an `onRollback(versionId, mode, prompt?)` prop to `VersionTreeProps` and have the row close over `node.id` when calling the leaf control's `onRollback(mode, prompt?)`

## 3. TaskDetail wiring

- [ ] 3.1 Instantiate `useRollbackTaskMutation` in `TaskDetail.tsx`; add an `onRollback(versionId, mode, prompt?)` handler that calls `rollback.mutate({taskId, body:{target_version_id: versionId, mode, prompt}})`
- [ ] 3.2 Success toast naming the mode (read from the mutation `vars.body.mode`, not the response) + resulting `version_no` (common to both response shapes); no optimistic `task.status` mutation (status flows via the live/polling pipeline)
- [ ] 3.3 Error handling inline (reuse the iterate/control shape): `409 active_version_exists` → warning naming the active version from `data`; `409 invalid_state` → warning; other `ApiError` → error toast; none retried

## 4. ARCHITECTURE doc reconcile

- [ ] 4.1 Reword `docs/ARCHITECTURE.md` line 522 — `switch` is also 409-gated on an active task (drop "仅切指针不受约束", point to §6.5)
- [ ] 4.2 Reword `docs/ARCHITECTURE.md` line 788 — UI disables 迭代/回滚 (both modes) on active, not just 回滚-branch

## 5. Tests + gates

- [ ] 5.1 `RollbackControl.test.tsx` — disabled states + reasons (task active disables both; row.is_active disables switch only), branch prompt disclosure, `onRollback` payloads incl. the empty-prompt branch case
- [ ] 5.2 Extend `VersionTree.test.tsx` — current row offers no rollback; non-current rows wire the control with correct disabled flags
- [ ] 5.3 Extend `TaskDetail.test.tsx` — branch 201 + switch 200 happy paths invalidate queries; `409 active_version_exists` and `409 invalid_state` surface inline and do not retry
- [ ] 5.4 Web gates green: `pnpm typecheck`, `pnpm lint`, `pnpm test`; `npx prettier --write` only the touched files
- [ ] 5.5 `openspec validate add-web-rollback-entry --strict` passes
