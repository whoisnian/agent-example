## Context

Task Detail 已是"一回合 = 一版本"的对话布局（`ConversationTurn`），但五个断点破坏体验（见 proposal）。关键现状事实：

- **worker 不发 artifact 事件**：`AgentBase.run()` 在 loop 结束后才批量 `insert_artifact`（幂等 upsert，唯一键 `(version_id, oss_key)`）；gateway/ingest 早已支持 `kind="artifact"` 透传与持久化，纯粹是发射端缺失。
- **artifacts 表没有 `path`**：相对路径只存在于 `oss_key`，而 `oss_key` 布局是确定的 `{tenant_id}/{task_id}/{version_id}/{path}`（`compute_oss_prefix`），公开 DTO 又不许泄漏 `oss_key`，所以前端拿不到文件名。
- **下载代理已就位**（`add-artifact-download-proxy`）：HS256 + `aud="artifact-download"` 的短时 token、流式回流、CSP sandbox 头；新端点全部沿用这套模式。
- **前端**：`useTaskLive` 只失效 task/versions/events 三类缓存；`EventLog` 只挂在 current 回合下且裸渲染 JSON；HTML 预览 iframe 的 src 是单文件下载 URL，相对引用无从解析。

## Goals / Non-Goals

**Goals:**

- 产物在执行过程中即出现在对话流里，结束后无需刷新。
- 回合内顺序 = 对话顺序：prompt → 执行过程 → 产物 → （非当前回合的回滚 footer）。
- 同版本产物聚合为一张卡片，支持 zip 整包下载；预览面板仍可逐文件浏览/下载。
- 历史回合的执行过程可回看（Iterate 不再"吞掉" v1）。
- 事件流按 kind 分型为对话样式；HTML 预览能加载相对路径 css/js。

**Non-Goals:**

- 不改 WS 帧协议、gateway 转发、event ingest（全部已兼容）。
- 不做产物树形浏览器 / 在线编辑 / diff 视图（Post-MVP）。
- 不支持 HTML 中**绝对路径**（`/css/a.css`）或跨版本引用的资源；只解析同版本下的相对路径。
- 不支持预览文档内**脚本发起的** `fetch()`/`XHR` 相对资源（opaque origin 跨源、API 不返 CORS 头，有意失败）；只支持标签型子资源（link/script/img）。
- 不为 zip 引入异步打包任务或缓存；按需流式打包。
- 历史回合事件不做 "load more"；>1 页时显示首页 + 截断提示。

## Decisions

### D1 产物实时化：step 级持久化 + `kind="artifact"` 事件（worker 侧发射，web 侧失效）

`run_agent_loop` 在每个 step 收尾处通过注入的 `persist_artifacts` 回调把该 step 产出的文件落库并逐个发出 `kind="artifact"` 事件，payload `{artifact_id, path, mime, bytes, sha256}`，seq 走 `ctx.next_event_seq()`。`AgentBase.run()` 结尾的批量 insert 移除（step 级已覆盖；resume 时已完成 step 不重跑，其产物早已入库——比现状更稳）。

**关键时序：产物落库 + 事件 seq 预留必须在该 step 的 checkpoint 写入之前**（沿用现有 step 事件的 cadence，`loop.py:186` 注释已解释同一原理）。否则有两条真实事故路径：① persist 在 checkpoint 之后、crash 落在二者之间 → resume 跳过该 step（checkpoint 已推进），OSS 对象永远没有 DB 行，而末尾批量 insert 已被本变更移除、无补偿；② artifact 事件 seq 若不被 checkpoint 的 `event_seq` 快照覆盖 → resume 后 `restore_event_seq` 重新发放这些 seq，被 ingest 的 `(run_id, seq)` 幂等静默丢弃（含 summary）。因此回调内顺序固定为：upsert 行 → 预留 step+artifact 全部 seq → 写 checkpoint → 发 step 事件 + artifact 事件。crash-before-checkpoint 时整 step 重跑、upsert 收敛，发放的 seq 全在恢复高水位之上，不冲突。继承产物（`inherit.py`）保持 run 开始时一次性落库，同样写 `path` 并发 `artifact` 事件，前端在 v2 回合开头即可见继承自 v1 的文件。

web 侧 `useTaskLive` 的 `version:` 分支增加：`kind === "artifact"` 或 `kind === "status"` 时失效该版本的产物查询缓存。选择"失效 + refetch"而非用 payload 直接写缓存：列表的排序/空态契约由服务端权威，避免客户端拼装偏差；artifact 事件频率低（文件级），多一次 GET 可接受。

> 备选：仅靠终态 status 失效（不改 worker）——无法满足"执行过程中实时展示"，弃。

### D2 `path` 列：迁移 0010 + 精确回填，DTO 透出

`artifacts` 新增 `path TEXT`（nullable）。回填确定可靠：`oss_key` 前缀即 `{tenant}/{task_id}/{version_id}/`，UPDATE 时按行剥前缀；剥不出的（理论不存在）留 NULL。新写入一律带 `path`。列表 DTO 增加 `path: string | null`（present-and-null 序列化，沿用既有口径；presign 响应不需要——前端标注一律来自列表数据）；`oss_key` 仍不泄漏。前端文件名显示 `path`，NULL 回退到现状的 `kind · mime` 标签。

> 不选 NOT NULL：避免迁移因脏数据失败；公共契约按 nullable 设计一次到位。

### D3 zip 整包下载：版本级 token + `archive/zip` 流式打包

两个端点，完全复刻单产物 presign/download 的模式，只是 scope 从 artifact 升到 version：

- `GET /api/v1/versions/{id}/artifacts/archive/presign` — Bearer 鉴权，owner 校验（`version_not_found` 不区分不存在/不属于），签发 `aud="artifact-archive"`、`sub=<version_id>`、TTL=`OSS_PRESIGN_TTL` 的 token，返回 `{url, expires_at}`。
- `GET /api/v1/versions/{id}/artifacts/archive?token=` — 公开路由，token 校验同下载代理（单一 `403 invalid_download_token`），然后 `archive/zip` 直写 ResponseWriter，逐个从 OSS 拉对象写 entry（entry 名 = `path`，NULL 回退 `artifact-<id>`），`Content-Disposition: attachment; filename="artifacts-<version_id>.zip"`。流中途 OSS 失败只能断连 + log/metric（zip 无法事后报错）。零产物版本返回合法空 zip（前端在 0 产物时不渲染下载按钮，此分支仅防御）。

> 不选客户端 JSZip：N 次 presign + N 次 fetch + 浏览器内存打包，大产物集不可控；服务端 stdlib 流式更简单。

### D4 目录化预览路由：token 走路径段，让相对 URL 自然解析

iframe 内 HTML 的相对引用（`./css/a.css`）按 URL 规则相对**当前文档路径**解析，query string 不会被继承——所以 token 必须进路径段：

- `GET /api/v1/versions/{id}/preview` — Bearer 鉴权 + owner 校验，签发 `aud="version-preview"`、`sub=<version_id>` 的 token，返回 `{base_url, expires_at}`，`base_url = /api/v1/versions/{id}/preview/<token>`。
- `GET /api/v1/versions/{id}/preview/<token>/<filepath...>` — 公开路由。校验 token（aud/sub/exp，失败一律 `403 invalid_download_token`）；filepath 经 URL 解码后做净化（`path.Clean`、拒绝 `..` 段 / 反斜杠 / 绝对路径 / 空或 `.`）；按 `(version_id, path)` 精确查 artifacts 行，无则 `404 artifact_not_found`（页面内资源 404 静默呈现即可）；命中则从 OSS 回流字节，响应头与下载代理一致（DB mime 权威、`CSP: sandbox allow-scripts`、nosniff、no-referrer、no-store）。token 在**路径段**里，故该路由不能复用默认中间件（会原样记录含 token 的 path）：必须记 token 脱敏形式（如 route template `…/preview/:token/*filepath`），也不记 query。

web 侧：HTML 渲染视图改为 mint 一次 preview base，iframe `src = base_url + "/" + encodePath(artifact.path)`；同一文档内的相对 css/js/img 自动落在同一 token 前缀下。`path` 为 NULL 的 HTML 产物退回现状单文件渲染。文本/图片/源码视图与单文件下载路径不变。

> 安全权衡：preview token 的授权面是"该版本全部产物"（单文件 token 是单对象）。可接受：mint 时已做 owner 校验、TTL 同样短、能力只读、版本本就是产物的归属单元。Service Worker / blob 重写方案能避免新路由，但复杂度（拦截、MIME 推断、SW 生命周期）远超一条对称的代理路由，弃。
>
> 解析面边界：opaque origin 下，**标签型**子资源（`<link>`/`<script src>`/`<img>`）不受 CORS 约束，能正常加载——问题④验收满足；但脚本发起的 `fetch()`/`XHR` 相对请求是 opaque→API 的跨源请求，API 不返 CORS 头、有意失败（见 Non-Goals）。

### D5 回合重排 + 聚合产物卡片

`ConversationTurn` 内顺序调整为：prompt（右对齐）→ `children`（执行过程）→ 产物 → 回滚 footer。产物从平铺多卡改为**单张聚合卡**：图标 + "N file(s) · 总大小" + 文件名摘要（前几个 `path`）+ "Download zip" 按钮（archive presign → `window.location.assign`）。即使版本只有单个文件也统一走 zip，保持行为可预期（逐文件下载仍可在右栏预览面板进行）。点击卡片主体 = 现有 `selectArtifact(versionId, firstArtifactId)`，右栏预览面板展开该版本文件列表（面板已具备列表+逐文件预览/下载，仅加 `path` 显示）。

### D6 对话连续性：每回合自带执行过程，历史折叠 + 懒加载

去掉 "EventLog 只挂 current" 的特判：每个回合渲染自己的执行过程区。历史回合默认**折叠**为一行摘要（"Execution log · 展开查看"，有 summary 时显示 summary 文本），展开时才发起该版本的 events 查询（React Query `enabled: expanded`，避免 N 版本 N 个 eager 请求）；当前回合保持展开 + 实时追加（现有 live/poll 路径不动）。

折叠行的 summary 文本来源是**版本详情 DTO 的新 `summary` 字段**（`GET /versions/{id}`，见 task-read-api delta）——`TurnPrompt` 本就为每个回合拉这个详情取 `prompt`，折叠行复用同一查询、零额外请求，且不触发 events 拉取。`task_versions.summary` 此前只用于 execute 的 history 注入，从未经任何读 DTO 暴露，故必须扩 DTO（不能从 events 取，否则折叠态就被迫 eager 拉事件）。

事件懒加载的分页边界：events 读是 `after_id`+`limit`（默认 200）。当前回合靠 live 追加 + gap-fill 补满；历史回合展开只发首页查询，>200 事件的 run 仅显示首页并**显式提示截断**（MVP 不做 "load more"，留后续提案），不静默丢尾。

### D7 按 kind 的对话式事件渲染

`EventLog` 拆出 per-kind 渲染器，原则：**对话内容用正文，过程信息用弱化行，绝不裸 JSON**：

- `summary` → 助手消息正文（普通段落文本，是回合的"回答"主体）。
- `plan` → 有序步骤清单（`payload.steps`）。
- `step` → 进度行：verdict 图标（pass/finish ✓ / retry ↻）+ `title` + `summary` 弱化文本。
- `artifact` → 文件徽标行（文件图标 + `path`），点击联动预览选中。
- `status` → 居中弱化的状态变更行（"running → succeeded" 风格的小字）。
- `log` → 弱化等宽小字。
- `error` → 现状的 destructive 行保留。
- `title` 及其它非对话 kind → 不渲染（任务标题已在页头；cost 走独立 `cost.events` exchange，从不进 `task.events`/WS）。
- 未知 kind → 现状的紧凑 JSON 截断作为兜底。

payload 字段缺失时各渲染器降级到兜底样式，不抛错。`artifact` 行按 `artifact_id` 去重（resume 可能以新 seq 重发同一文件），同一文件只显示一行。

## Risks / Trade-offs

- [step 级落库后 run 失败/取消，产物已可见] → 符合预期：失败回合的部分产物本就该可见可下载（与"可恢复"语义一致）；UI 上回合状态徽标已标明 failed。
- [artifact 事件与 DB 写入竞态：前端收到事件去 refetch 时行已落库？] → 发射顺序固定为"先 insert（拿到 artifact_id）后 publish"，refetch 必然可见。
- [zip 流式打包长连接占用 + 中途失败无法报错] → 沿用下载代理的 abort+metric 口径；TTL 短、owner-mint 限制滥用面；MVP 不做大小上限，记 metric 观察。
- [preview token 版本级授权面扩大] → 见 D4 权衡；token 不可用于写、不可跨版本（sub 钉死）、不进日志。
- [历史回合懒加载造成展开瞬间的 loading 闪烁] → 折叠行保留 summary 文本兜底，展开 loading 用 skeleton。
- [`path` 含特殊字符（空格、中文）] → 预览 URL 按段 encode，zip entry 原样 UTF-8（zip flag bit 11）。

## Migration Plan

1. 迁移 0010：加列 + 回填 + `(version_id, path)` 部分唯一索引（幂等，可重跑）；down = drop index + drop column。
2. 发 worker：worker 直写 DB，必须先跑迁移再发 worker（写 `path` 需要列存在）。artifact 事件对旧 web 无害——旧 web 不消费该 kind 的失效逻辑，事件只是多写一行 `task_events`。
3. 发 API：版本详情 DTO 增 `summary` + 新增 archive/preview 端点。
4. 发 web：实时失效、回合重排、对话连续性、对话式渲染、目录化预览。
全程向后兼容，无破坏性变更；回滚按逆序摘除即可（`path` 列与 `summary` 字段保留不影响旧代码）。

## Open Questions

- 聚合卡片中"文件名摘要"展示几个 `path` 截断规则（实现时按视觉调整，不影响契约）。
- zip 大小是否需要硬上限（MVP 先 metric 观察，超限策略留给后续提案）。
