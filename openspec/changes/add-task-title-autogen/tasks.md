## 1. Domain 层派生逻辑

- [ ] 1.1 `api/internal/domain/task/validation.go`：新增 `deriveTitle(prompt string) string` 纯函数（首个非空行、≤64 rune 且 ≤200 字节的 rune 边界双截断 + `…` 后缀、全空白兜底 `Untitled task`），并将 `validateTitle` 语义改为仅校验显式非空 title
- [ ] 1.2 `api/internal/domain/task/service.go`：`CreateTask` 校验顺序调整为 task_type → prompt → title；title trim 后为空时改走 `deriveTitle(prompt)`
- [ ] 1.3 domain 单测：覆盖 ASCII 长行、CJK 长行、emoji、首行前空行、全空白 prompt、显式 title 超长仍 400、显式 title 正常路径不变

## 2. HTTP 层与契约测试

- [ ] 2.1 `api/internal/interfaces/http/tasks.go`：更新 `Title` 字段注释为可选语义（无 binding 改动）
- [ ] 2.2 `tasks_integration_test.go`：反转 "missing title → 400" 用例为 "missing title → 201 且 title 为派生值"；新增显式 title 生效、超长 title 仍 400 的用例

## 3. 验证与规格

- [ ] 3.1 `api` 全量 `make test`（或等价 go test）+ lint 通过
- [ ] 3.2 `openspec validate add-task-title-autogen` 通过
