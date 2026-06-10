## Why

前端 New task 页面将改为 Claude.ai 风格的聊天输入框（见 `refactor-web-chat-style-polish`），不再向用户索取标题；标题应由后端在创建任务时自动生成。当前 `POST /api/v1/tasks` 把 `title` 作为必填字段（domain 层校验 trim 后 1..200，缺失返回 `400 invalid_input`），前端无法省略。

## What Changes

- `POST /api/v1/tasks` 的 `title` 字段从必填改为**可选**：缺省或 trim 后为空时，服务端从 `prompt` 自动派生标题（API 层不调用 LLM，遵守 AGENTS 边界；派生策略为 prompt 首行截断，截断长度在 design 决策）。
- 显式提供非空 `title` 时维持现有校验语义（trim 后 1..200，超长仍 `400 invalid_input`）。
- **BREAKING（语义放宽）**：原 "empty title → 400 invalid_input" scenario 反转为 "empty/absent title → 派生标题 + 201"。对既有客户端无破坏（原必填请求仍合法），但契约测试断言需反转。
- 派生后的标题落库仍满足 `tasks.title` 既有约束（非空、≤200 字符）；DB schema 不变。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `task-write-api`: "Create Task Endpoint" requirement 增加标题派生语义；"404 and 400 Outcomes" requirement 中 "Missing required field"（empty title → 400）scenario 改为仅对显式超长/非法 title 报 400，empty/absent 走派生路径。

## Impact

- **规格**：`openspec/specs/task-write-api/spec.md`（两个 requirement 的 delta）。
- **代码**：`api/internal/domain/task/validation.go`（`validateTitle` 拆分为显式校验 + 派生函数）、`api/internal/domain/task/service.go`（Create 入参 title 可空时调用派生）、`api/internal/interfaces/http/tasks.go`（DTO 注释/binding 不再要求 title）。
- **测试**：`api/internal/interfaces/http/tasks_integration_test.go` 中 "missing title" 用例语义反转（断言 201 + 派生标题），新增显式 title 仍生效与超长仍 400 的用例；domain 单测补派生函数用例。
- **下游**：`web` 端移除 title 输入放在独立变更 `refactor-web-chat-style-polish`，依赖本变更先落地；`worker` / MQ 契约不受影响（execute 消息不含 title）。
