## Why

Task Detail 的对话体验存在五个断点，导致"提交 → 观察 → 取回产物"的闭环不顺：

1. **产物不实时**：worker 只在 run 结束时批量写 `artifacts` 行，且从不发出 `kind="artifact"` 事件（gateway 早已支持转发该 kind）；前端 `useTaskLive` 也不失效产物缓存——用户必须手动刷新才能看到产物。
2. **产物卡片位置与形态**：每个版本的产物列表渲染在执行详情（EventLog）**上方**，不符合对话顺序；多个关联文件平铺为多张卡片，无法一键下载整组产物。
3. **对话历史被截断**：EventLog 只为 `current_version` 渲染，Iterate 之后 v1 的执行过程消失，对话不连续。
4. **HTML 预览资源加载失败**：预览 iframe 加载的是单文件下载代理 URL，HTML 中相对路径引用的 css/js 无法解析（且产物元数据没有 `path`，前端连文件名都显示不出来——只有 `kind="file"`）。
5. **事件流是裸 JSON**：EventLog 对 plan/step/log/summary 等 kind 统一渲染截断 JSON，不是对话样式。

## What Changes

- **Worker**：每个 step 结束即落 `artifacts` 行（幂等 upsert 已具备）并发出 `kind="artifact"` 事件（payload 含 `artifact_id, path, mime, bytes, sha256`）；产物行写入新的 `path` 列（继承复制的产物同样写 `path`）。
- **数据模型**：`artifacts` 表新增 `path TEXT` 列（存量行可空，尽力从 `oss_key` 回填）。
- **API（artifacts-api 面）**：
  - 列表 DTO 增加 `path` 字段（nullable）。
  - 新增版本级**压缩包下载**：`GET /versions/{id}/artifacts/archive/presign` + `GET /versions/{id}/artifacts/archive?token=`，流式打包该版本全部产物为 zip（zip 内路径 = `path`）。
  - 新增版本级**目录化预览路由**：`GET /versions/{id}/preview/{token}/{path...}`，按 `path` 解析同版本产物并回流字节，使 iframe 内 HTML 的相对路径 css/js 能在同一 token 前缀下正确加载；配套 presign 端点返回预览 base URL。
- **Web**：
  - `useTaskLive` 收到 `version:` 主题的 `artifact` 或 `status` 帧时失效该版本产物缓存（实时出卡片，无需刷新）。
  - 会话回合（ConversationTurn）重排：产物卡片移到执行详情**下方**；同版本产物合并为单张聚合卡片（文件数 + 总大小 + 下载 zip 按钮），点击卡片在右栏预览面板展开文件列表。
  - 对话连续性：每个版本回合都渲染自己的执行过程（历史版本默认折叠、展开时按需拉取事件；当前版本展开并实时追加）。
  - EventLog 按 kind 分型渲染（对话样式）：`summary` 为助手消息正文、`plan` 为步骤清单、`step` 为带 verdict 的进度行、`artifact` 为文件徽标、`status`/`log` 为弱化行、`error` 为醒目错误；未知 kind 才回退紧凑 JSON。
  - HTML 渲染预览改用目录化预览路由（保留单文件下载与文本/图片预览路径不变）。

## Capabilities

### New Capabilities

（无 —— 压缩包与预览路由并入 `artifacts-api` 这一既有 HTTP 产物读取面。）

### Modified Capabilities

- `task-data-model`: `artifacts` 表新增 `path` 列（迁移 + 回填规则 + `(version_id, path)` 部分唯一索引）。
- `task-read-api`: 版本详情 DTO（`GET /versions/{id}`）新增 `summary`（nullable），供历史回合折叠行显示而无需 eager 拉事件。
- `worker-agent-orchestration`: step 级产物持久化时机 + `kind="artifact"` 事件发射 + 写 `path`。
- `worker-artifact-inheritance`: 继承复制的产物行同样携带 `path`。
- `artifacts-api`: DTO 增加 `path`；新增 archive presign/download 端点；新增版本预览文件路由（token 走路径段以支持相对引用）。
- `web-artifacts-views`: 类型与数据访问增加 `path`、archive presign、preview presign。
- `web-tasks-pages`: 回合布局重排（产物卡片在下）、聚合产物卡片、全版本对话连续性、按 kind 的对话式事件渲染、artifact 帧驱动的缓存失效。
- `web-artifact-preview`: 预览面板文件列表显示 `path`；HTML 渲染视图改用目录化预览 URL。

## Impact

- `worker/worker/agents/loop.py`、`base.py`、`inherit.py`、`core/persistence.py`（artifact 写入时机与 path）
- `api/migrations/`（0010 path 列 + `(version_id, path)` 部分唯一索引）、artifacts 相关 sqlc 查询、handler 与路由（archive、preview、token 新 aud）、`read_dtos.go` 的 `VersionFull` 增 `summary`
- `web/src/components/tasks/{ConversationTurn,EventLog}.tsx`、`TaskDetail.tsx`、`features/tasks/use-task-live.ts`、`features/artifacts/*`、`ArtifactPreviewPanel.tsx`
- 不改：realtime-gateway（`artifact` kind 本就转发）、task-event-ingest（kind 透传）、web-realtime-client（帧协议不变）
- 安全：archive / preview token 沿用 HS256 + 专用 `aud`（`artifact-archive` / `version-preview`）、短 TTL、mint 时校验所有权；预览路由沿用下载代理的 CSP sandbox / nosniff / no-referrer 头；路径解析拒绝 `..` 与绝对路径
