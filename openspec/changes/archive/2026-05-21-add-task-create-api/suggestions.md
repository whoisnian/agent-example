# Review Suggestions — add-task-create-api

## 严重缺陷（实现前必须修正）

### S1. D2 Step 5：事务已中止，不能在同一 tx 内重读（`design.md`）

`design.md` D2 写道：
> "re-read the active version inside the same transaction (it has not committed)"

PostgreSQL 触发 SQLSTATE `23505` 后，当前事务进入 **aborted** 状态，后续任何查询都会报
`current transaction is aborted, commands ignored until end of transaction block`，
无法在同一 tx 内继续执行 `GetActiveVersionByTask`。

**修正方案**：在 INSERT 前设置 `SAVEPOINT sp_insert_version`；捕获到约束冲突后
`ROLLBACK TO SAVEPOINT sp_insert_version`，读取 active version，再 `ROLLBACK` 整个 tx 返回 409。
或：回滚整个 tx，开新只读事务查 active version，再返回 409。

需在 `design.md` D2 和 `tasks.md` 2.7 中删去歧义的"or re-open one"，明确首选方案。

---

### S2. 缺少 `GetVersionByTaskAndID` 查询（`tasks.md` Section 1）

D12 要求：`base_version_id` 若提供，**MUST belong to the path `task_id`**。
但 `tasks.md` Section 1 的 5 条 sqlc 查询中没有"按 `(task_id, version_id)` 查 task_versions"的查询，
实现 iterate 时会直接遇到缺口。

**修正方案**：在 tasks.md Section 1 中补充（现有 1.6 挪为 1.7）：
```
- [ ] 1.6 Add `GetVersionByTaskAndID` query:
        SELECT * FROM task_versions WHERE id = $1 AND task_id = $2
```

---

### S3. `parent_artifact_root` 解析逻辑未定义（`design.md` D8 / `tasks.md` 2.10）

outbox payload 中 `parent_artifact_root` 应为 base version 的 OSS URI，但：
- 数据来源未说明：是 `task_versions` 的某列，还是 `artifacts` 表的关联记录？
- base version 尚未产出 artifact 时（如刚创建即被 iterate），fallback 值未定义。

**修正方案**：在 D8 中补充字段来源和 fallback 规则（建议：字段不存在时置 `null`，
不报错）；在 tasks.md 2.10 中同步说明。

---

## 设计缺陷（影响正确性或可维护性）

### S4. `clock` / `idgen` 未声明接口，单测 mock 无根据（`tasks.md` 2.3 / 7.1）

`Service` struct 依赖 `clock` 和 `idgen`，但没有在 domain 包中定义对应接口。
若直接调用 `time.Now()` 和 `uuid.NewV7()`，Task 7.1 要求的单测将无法固定时间和 ID，
也就无法断言 `msg_id`、`deadline_ts` 等 outbox payload 字段。

**修正方案**：在 `domain/task/` 的 `ports.go`（或 `service.go`）中显式定义：
```go
type Clock interface { Now() time.Time }
type IDGenerator interface { NewV7() (uuid.UUID, error) }
```
并在 tasks.md 2.3 中点明这两个依赖需作为接口注入。

---

### S5. `tasks.status` 活跃集合定义顺序不一致（`design.md` D2 vs D12）

- D2 Step 3：`pending|running|paused|cancelling|queued`
- D12：`pending|queued|running|paused|cancelling`

两处排列不同，容易在 review 时引发"是否遗漏某个状态"的误判。

**修正方案**：统一按状态机流转顺序（`pending → queued → running → paused ↔ cancelling`）
描述，两处引用均改为"参见 `status.go`（Task 2.2）"。

---

### S6. `tasks.status` 写权限的债务未给出迁移路径（`design.md` D9 / Risks）

设计承认 `tasks.status` 按架构 §4.3 是"derived"，但这里由 API service 直接写入。
Risks 节仅说"acceptable for MVP"，没有明确后续由谁接管、在哪个变更中清还。

**修正方案**：在 Open Questions 中增加第 4 条，记录该债务及预期接管时机
（建议：在 `task-control-api` 或 `add-worker-event-loop` 中引入状态机 service 时一并处理）。

---

## 任务列表改进点

### S7. Task 2.4 未提及 `attempt_no` 赋值

D8 的 outbox payload 硬编码 `"attempt_no": 1`，但 tasks.md 2.4 描述 `CreateTask` 时
只列出"inserts tasks + task_versions + task_runs + outbox"，未显式提及
`task_runs.attempt_no = 1`，实现者可能遗漏。

**修正方案**：在 tasks.md 2.4 和 2.5 中分别注明 `task_runs.attempt_no = 1`（首次运行固定值）。

---

### S8. Task 7.6 并发测试断言不完整

当前描述：
> "exactly one 201, one 409 with `active_version_exists`"

未验证 409 响应体中 `data.active_version_id` 是否等于 201 返回的 `version_id`，
即未覆盖 D2 的 active_version_id 回填逻辑。

**修正方案**：在 tasks.md 7.6 中补充断言：
```
assert response_409.data.active_version_id == response_201.version_id
```

---

### S9. Task 8.3 缺少集成测试运行前提说明

`make test-integration` 依赖 testcontainers + Docker（CI 中可能需要 DinD 或 Colima），
当前任务描述中无此说明，容易造成 CI 静默失败。

**修正方案**：在 tasks.md 8.3 中补注前置条件，并在 `api/README.md` 的
"集成测试"小节中同步说明。

---

## 轻微 / 一致性问题

| # | 位置 | 问题 | 建议 |
|---|---|---|---|
| S10 | `proposal.md` Impact | "`api/queries/outbox.sql` — add InsertOutbox"暗示新建文件，实为追加 | 改为"append `InsertOutbox` to existing `api/queries/outbox.sql`" |
| S11 | `proposal.md` Modified Capabilities | 声明"none"，但 `errors.go` 确实被修改 | 将 api-bootstrap 的错误目录列为 modified |
| S12 | `tasks.md` 1.6（现 1.7） | 未说明 sqlc 是否需要运行中的 Postgres | 加注：若 `sqlc.yaml` 未配置 `database.uri`，无需 DB；否则需要 |
| S13 | `design.md` D6 | 说"absent or empty"触发 fallback，但 D12 的验证规则最少 1 字符，空字符串会先被拦截 | D6 改为"absent (field omitted)"，与 D12 的 optional 语义对齐 |
