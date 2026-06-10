## Context

`POST /api/v1/tasks` 当前在 domain 层（`api/internal/domain/task/validation.go` 的 `validateTitle`）强制 `title` 必填（trim 后 1..200 字符），HTTP DTO（`tasks.go`）只做透传、不带 binding 约束。前端将改为聊天式创建入口，不再提供标题输入；AGENTS 边界规定 `api/` 不直接调用 LLM，因此自动标题只能用确定性派生。`tasks.title` 列约束（非空）与读侧 DTO 均不变。

## Goals / Non-Goals

**Goals:**
- `title` 缺省/空白时由服务端从 `prompt` 确定性派生，创建仍返回 201。
- 显式非空 `title` 的行为完全不变（trim 后 1..200，超长 400）。
- 派生结果永远满足落库约束（非空、≤200 字节）。

**Non-Goals:**
- 不做 LLM 生成标题（API 边界禁止；如未来需要语义化标题，应由 Worker 通过事件异步回写，属 Post-MVP，另行提案）。
- 不改 iterate / rollback / 读侧任何契约；execute 消息不含 title，Worker 不受影响。
- 不做存量数据迁移（旧任务都有 title）。

## Decisions

### D1 派生位置：domain 层 `CreateTask` 内，纯函数实现

在 `validation.go` 增加 `deriveTitle(prompt string) string` 纯函数；`CreateTask` 中将 `validateTitle` 替换为"trim 后为空 → `deriveTitle(prompt)`，否则走原校验"。不放在 HTTP 层（保持 handler 薄、规则可单测）；不放在 SQL 层（保持 schema 不变）。

### D2 校验顺序：先 `validatePrompt` 再派生

派生依赖合法的 prompt。顺序调整为 task_type → prompt → title（缺省时派生），保证 prompt 缺失时报 `invalid_input(prompt)` 而非派生出空标题。注意 `validatePrompt` 不 trim（代码生成 prompt 的前导空白有意义），因此全空白 prompt 是合法输入 → 派生必须有兜底（D3）。

### D3 派生规则：首个非空行 + 双重截断 + 兜底

1. `strings.TrimSpace(prompt)` 后取第一个非空行；
2. 截断到 **≤64 个 rune 且 ≤200 字节**（两个上限同时生效，在 rune 边界截断——`maxTitleLen=200` 是字节数，CJK/emoji 下 64 rune 可能超 200 字节，必须双重约束）；截断发生时追加 `…`；
3. 结果为空（全空白 prompt）→ 字面量 `"Untitled task"`。

64 rune 取自聊天产品标题的常见可读长度；确定性、无随机成分，同 prompt 必得同 title，测试可断言精确值。

### D4 HTTP 层与文档

`tasks.go` 的 `Title` 字段保持 `json:"title"` 透传（无 binding 改动），仅更新注释说明可选语义。错误码、字段名维持英文。

## Risks / Trade-offs

- [截断派生的标题可读性有限（如 prompt 以代码开头）] → MVP 接受；语义化标题留给 Post-MVP 的 Worker 异步生成方案。
- [契约语义反转可能让依赖 "empty title → 400" 的客户端测试失效] → 仓库内唯一消费方是 web 前端（即将移除 title 输入）；集成测试同步反转断言。
- [字节/rune 双截断实现易错] → domain 单测覆盖：ASCII 长行、CJK 长行、emoji、首行空白、全空白 prompt、恰好 200 字节边界。
