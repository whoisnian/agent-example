# Review Suggestions — improve-artifact-conversation-ux

> 评审范围：proposal.md / design.md / tasks.md / specs/**（7 个 delta），对照既有 specs 与 worker / api / web 现实代码。
> 按严重度排序（高 → 中 → 低）。

---

## 高

### S1（高 / 正确性）step 级产物落库与 checkpoint / 事件 seq 的相对顺序未钉死 → 崩溃窗口下行丢失 + resume 后事件被 ingest 静默丢弃

**问题**：D1 与 `specs/worker-agent-orchestration/spec.md` 只说产物 upsert 与 `kind="artifact"` 事件发生在 "step 收尾处（紧邻现有 `kind="step"` 事件）/ alongside the step's existing `kind="step"` event"，没有规定它相对于**该 step 的 checkpoint 写入**的顺序。现有 loop 顺序是：执行 step → 预留 step 事件 seq → 写 checkpoint（state 含 `event_seq` 高水位）→ 发 step 事件（`worker/worker/agents/loop.py` L185–219）。两种错误排序各有事故：

1. **persist 放在 checkpoint 之后**：crash 落在 "checkpoint 已写、产物未落库" 之间 → resume 跳过该 step（`_load_or_create_plan` 直接从 `latest.step_seq` 继续），该 step 的 OSS 对象**永远没有 DB 行**——且本变更已移除末尾批量 insert，没有任何补偿路径。
2. **artifact 事件 seq 在 checkpoint 快照之后消费**：artifact 事件用 `ctx.next_event_seq()` 取 seq（H+1…H+k），但 checkpoint 里记录的高水位是 H；crash 后 resume `restore_event_seq(H)`，后续事件**复用 H+1…H+k**，ingest 的 `(run_id, seq)` 幂等会把它们当重复**静默丢弃**（包括 summary 事件）——这正是既有 "Resume-Safe Event Sequencing" requirement 明文警告的故障模式（`worker/worker/core/run_context.py` L121–130）。

**证据**：`loop.py` L185–219（seq 预留 → checkpoint → emit 的现有 cadence）；delta spec 仅有 "insert-then-publish"（DB vs MQ 顺序），未约束 vs checkpoint 顺序；tasks 2.2 同样未提。

**建议**：在 `worker-agent-orchestration` delta 中明确规定：**该 step 的产物 upsert + artifact 事件发射必须发生在该 step 的 checkpoint 写入之前**，使（a）crash-before-checkpoint 时 resume 重跑 step、upsert 收敛；（b）checkpoint 的 `event_seq` 快照覆盖所有已发 artifact 事件的 seq。补一条 scenario（"crash between artifact persistence and the step checkpoint → resume re-executes the step and the upsert leaves one row; no post-resume event reuses a persisted seq"）。tasks 2.2/2.4 相应补充单测断言顺序。

### S2（高 / 一致性）web-artifact-preview 未把 "Artifact Preview Panel" requirement 列为 MODIFIED → 归档后同一 spec 内两条 MUST 互相矛盾

**问题**：delta 只 MODIFIED 了 "Lightweight Artifact Content Preview" 并 ADDED "Artifact Path Display"。但既有 "Artifact Preview Panel" requirement 明文要求：列表行 "each row showing `kind`, a mime label…"，toolbar "shows the selected artifact's identity (its `kind` and a mime label)"，且配套 scenario "Toolbar shows the selected artifact identity … MUST show that artifact's `kind` and mime label"（`openspec/specs/web-artifact-preview/spec.md` L8–34；实现 `ArtifactPreviewPanel.tsx` L80、L307–308）。新 ADDED requirement 则要求行主标签与 toolbar 标题用 `path`。归档后两条 requirement 同时生效、彼此冲突——archive 是整块替换，不会自动修掉旧块里的 kind 措辞。

**建议**：把 "Artifact Preview Panel" 一并放进 MODIFIED（完整复制原文，更新 row/toolbar 标签语句与对应 scenario），或将 path 显示直接合并进该 requirement 而不是另立 ADDED。注意原 requirement 还包含 selection invariant、testid 清单等内容，复制必须完整。

### S3（高 / 完备性）历史回合折叠行要显示 `version.summary`，但没有任何 HTTP 读 DTO 暴露 summary —— 按现 delta 无法实现

**问题**：`specs/web-tasks-pages/spec.md`（Task Detail Page，MODIFIED）要求历史回合折叠行 "showing the version's `summary` text when present"；design D6、tasks 5.2 同。但 `task_versions.summary`（迁移 0009）目前**只**被 `task-conversation-history` 用于 execute 消息的 `history` 注入；版本读 DTO 不含它：`api/internal/domain/task/read_dtos.go` 的 `VersionNode`（L85–93）与 `VersionFull` 均无 `summary` 字段，`task-read-api` spec 也未要求；web 侧 `VersionNode`/`VersionFull`（`web/src/features/tasks/types.ts` L78–109）同样没有。折叠态的设计初衷是**不**eager 拉事件，所以也不能从 events 里取 summary。

**建议**：三选一并落到提案里：（a）追加 `task-read-api` delta —— `GET /tasks/{id}/versions` 的 `VersionNode` 增加 `summary: string | null`（present-and-null），连带 web types/MSW/契约测试任务；（b）降级折叠行为固定 "Execution log" 标签（删掉 summary 文案，proposal/design/tasks 同步改）；（c）折叠行展示走其它已有字段。当前四件套（proposal/design/specs/tasks）在此点上集体指向一个不存在的数据通道，必须修。

---

## 中

### S4（中 / 一致性）worker-agent-orchestration 其余 requirement 残留 "end-of-run artifact upload" 引用

**问题**：delta 只改了 "Artifact Upload on Success"。但同 spec 内 "Planner / Executor / Critic Step Loop" 的 scenario "Critic finish ends the loop" 仍写 "…and proceed to artifact upload"（`openspec/specs/worker-agent-orchestration/spec.md` L62–64）；"Run Summary Event" 写 "after artifact upload, before returning"（L173）。移除末尾批量 insert 后，这些表述与新 requirement（"There is no separate end-of-run artifact persistence pass"）矛盾。

**建议**：把这两个 requirement 也纳入 MODIFIED（最小化措辞修订："proceed to artifact upload" → "proceed to the run-summary emission"；"after artifact upload" → "after the final step's artifact rows are persisted"）。

### S5（中 / 安全）preview token 走路径段 → 默认 access log 记录 path 即泄漏 token；delta 只有 prose、无 scenario，tasks 一笔带过

**问题**：现有下载代理的 "token 不进日志" 依赖 "标准中间件只记 path 不记 query"（`artifacts-api` spec L104、gateway 同款），而 preview token **就在 path 里**——不特例化该路由，标准 access log / `handleError` 的 path 字段会原样落 token。delta D4 与 ADDED requirement 有一句 "the route's logging MUST omit the token path segment"，但没有像既有 "Token never reaches the access log" 那样的 scenario；archive 端点同样缺。tasks 3.4 仅 "日志省略 token 段" 五个字。

**建议**：给 preview serve 与 archive download 各补一条 "token never reaches the access log" scenario；tasks 3.5 增加契约/单测断言（访问日志行不含 token 段）。实现提示也值得写进 design：该路由需要替换/改写 path 记录（如以 route template `…/preview/:token/*filepath` 记录），不能依赖默认行为。

### S6（中 / 完备性）opaque origin 下 HTML 内 `fetch()`/XHR 相对资源会因 CORS 失败 —— 应写入 Non-Goals

**问题**：预览响应带 `CSP: sandbox allow-scripts`、iframe sandbox 不含 `allow-same-origin`，文档运行在 opaque origin。`<link>`/`<script src>`/`<img>` 等标签型子资源不受 CORS 约束、可正常加载；但生成的 HTML 若用 `fetch("./data.json")`/XHR 加载相对资源，则是 opaque origin → API origin 的跨源请求，API 不返回 CORS 头 → 失败。问题④的验收（css/js 标签加载）能满足，但 "目录化预览" 的隐含预期可能被脚本型加载打破，且 sandbox 内失败对宿主不可见。

**建议**：在 design Non-Goals / D4 与 `artifacts-api` ADDED requirement 中明确：仅支持标签型子资源的相对引用；脚本发起的 `fetch`/XHR 不在支持面（不加 CORS 头是有意的安全决策）。tasks 7.3 手动验证用例据此圈定。

### S7（中 / 完备性）历史回合事件懒加载未处理分页：limit=200 之后的事件被静默截断

**问题**：events 读是 `after_id` + limit（前端 `EVENTS_LIMIT = 200`，`use-task-live.ts` L19）。当前版本靠 live 追加 + gap-fill 补满；历史回合展开只发一次首页查询，>200 事件的 run 只显示前 200 条，delta spec / design D6 / tasks 5.2 均未提"加载更多"或上限语义。

**建议**：spec 至少声明边界（"展开加载首页（≤N 条），更早事件通过 load-more 拉取"或"MVP 明确只显示首页并提示截断"），避免实现各凭直觉；组件测试覆盖 >limit 用例。

### S8（中 / 正确性）回填与 preview 路由的空 path 边界

**问题**：（a）迁移回填按剥前缀实现时，`oss_key` 恰好等于 `{tenant}/{task}/{version}/`（零长后缀）会得到 `path = ''` 而非 NULL，违反 "剥不出的留 NULL" 的意图，且空串会参与 preview 精确匹配与 DTO 显示；（b）`GET …/preview/<token>/`（空 filepath）delta 未明确——`path.Clean("")` 为 `"."`，应显式拒绝。worker 侧 `_normalize_key` 拒绝空 key（`storage.py` L43），新写入无此问题，纯属回填/路由边界。

**建议**：task-data-model delta 的回填规则补 "剥出后为空串视为不匹配（留 NULL）"；artifacts-api preview requirement 的净化清单加 "empty path（或 Clean 后为 `.`）→ 404"；tasks 1.1 / 3.4 补对应断言。

### S9（中 / 一致性）"终态 status" vs "任意 status" 失效口径，proposal 与 delta/tasks 不一致

**问题**：proposal What Changes 写 "收到 `version:` 主题的 `artifact` / **终态** `status` 帧时失效产物缓存"；`specs/web-tasks-pages` Live Observation delta 与 tasks 4.2 是 **任意** `kind === "status"` 帧都失效（scenario 仅以终态举例）。两种都能工作（status 频率低），但四件套必须同一口径。

**建议**：统一为 "任意 status 帧失效"（实现最简单、与 delta 一致），修订 proposal 措辞。

---

## 低

### S10（低 / 范围）移除末尾批量 insert 后 `LoopResult.artifacts` 成为死契约

`run_agent_loop` 仍聚合 `produced` 并经 `LoopResult.artifacts` 返回（`loop.py` L165–234），唯一消费者就是被删除的 `AgentBase.run()` 批量 insert（`base.py` L158–166）。tasks 2.2 应同步说明：要么删字段，要么注明保留原因（如测试断言），避免留下无人消费的返回值。

### S11（低 / 完备性）resume 重发 artifact 事件 → 持久化事件流出现同一文件的重复 file 行

delta 允许 "MAY re-emit artifact events under fresh seq values"。这些重发事件会作为新 `(run_id, seq)` 持久化进 `task_events`，按 kind 分型渲染后 EventLog 中同一 `path` 出现多行 file 徽标。建议 `web-tasks-pages` 的 Conversation-Style Event Rendering 明示按 `artifact_id`（或 path）去重，或明确接受重复行为已知表现。

### S12（低 / 一致性）"forwarded cost events" 例子不成立

design D7 与 `web-tasks-pages` delta 把 "forwarded cost events" 列为不渲染的非对话 kind，但 cost 事件走 `cost.events` exchange，从不进 `task.events` / WS（gateway spec 的 kind 清单亦无 cost）。删掉该例子，保留 "title 及其它非对话 kind 不渲染" 即可。

### S13（低 / 一致性）requirement 名 "Artifact Upload on Success" 名实不符

新语义是 per-step 持久化、失败 run 的部分产物同样可见（design Risks 第一条），"on Success" 已经误导。MODIFIED 无法改名；如在意，可 REMOVED + ADDED 重命名为 "Per-Step Artifact Persistence and Events"；不改也不阻塞。

### S14（低 / 一致性）tasks 5.1 的回合顺序漏掉 result line

delta spec 是 prompt → **result line** → 执行区 → 产物 → 回滚 footer；tasks 5.1 与 design D5 写 "prompt → 执行区 → 产物 → 回滚 footer"。实现照 tasks 走会把 result line 的位置留给猜测。对齐三处表述。

### S15（低 / 正确性）`GetArtifactByVersionAndPath` 仅靠 `artifacts_version_idx` 支撑

preview 路由按 `(version_id, path)` 精确查；现仅有 `version_id` 单列索引（0002 L139）。每版本产物量小，MVP 可接受；若顺手，可在 0010 加 `(version_id, path) WHERE path IS NOT NULL` 的部分唯一索引——兼作 "同版本 path 唯一"（zip entry 名 / preview 解析唯一性）的硬保证，目前该唯一性只是 `oss_key = prefix + path` 推导出的软性质。

### S16（低 / 文档）design 残留草稿口吻

Migration Plan 第 2 步 "（写 path + 发 artifact 事件——旧 API 忽略未知列?）注意：…" 的自我修正句式应清理为结论句；D5 中 "单文件版本直接走单文件下载省一层 zip？——不，…" 同理。不影响契约，影响可读性。

---

## 总体结论

提案对五个断点的归因准确、与代码现实核对扎实（gateway 已转发 `artifact` kind、ingest kind 透传、`(version_id, oss_key)` upsert 幂等、`compute_oss_prefix` 确定性布局、下载代理安全头与 token 隔离均查证属实）；D4 "token 进路径段以承接相对 URL 解析" 的技术判断正确，insert-then-publish、单一 403 口径、`aud`/`sub` 双向隔离等安全设计延续得当；artifacts-api / task-data-model / web-artifacts-views 三个 MODIFIED 块是对原文的忠实超集，未发现 AGENTS.md 红线违规（worker 写面仍限 artifacts 表，未触碰 tasks/task_versions 与互斥索引）。

但有三个高严重度问题必须在 apply 前修正：**S1**（step 级持久化相对 checkpoint 的顺序未定义，存在产物行永久丢失与 resume 后事件被幂等丢弃两种真实事故路径）、**S2**（web-artifact-preview 漏改 "Artifact Preview Panel"，归档后 spec 自相矛盾）、**S3**（折叠行依赖的 `version.summary` 在任何读 DTO 中都不存在，需追加 task-read-api delta 或降级该设计）。中等项以补 scenario / 对齐措辞为主，工作量小。范围上无过度设计——zip 服务端流式、目录化预览路由、按 kind 渲染都贴着问题走；变更体量大但 Migration Plan 的分段顺序（迁移 → worker → API → web）正确，建议按该顺序拆 PR 落地。

---

## 处置记录（评审后逐条核实并修订提案）

> 已对 3 条高 + 关键中低意见核实代码证据后修订提案，`openspec validate --strict` 通过。

| 编号 | 核实结论 | 处置 |
|---|---|---|
| S1 | **成立**（`loop.py:186-192` 现有 step 事件正是"先预留 seq 再 checkpoint"，新增产物路径必须同样处理） | 采纳。`worker-agent-orchestration` delta 把 "Planner/Executor/Critic Step Loop" 与 "Artifact Upload on Success" 一并 MODIFIED，钉死"upsert 行 + 预留 seq → checkpoint → 发事件"顺序，补 crash-before-checkpoint scenario；design D1、tasks 2.2/2.4 同步 |
| S2 | **成立**（`web-artifact-preview` 既有 "Artifact Preview Panel" 强制 `kind` 标签，与新增 path 显示冲突） | 采纳。把 "Artifact Preview Panel" 列入 MODIFIED（path 优先、null 回退 kind），删除独立的 ADDED "Artifact Path Display"，scenario 折叠并入 |
| S3 | **成立**（`read_dtos.go` 的 `VersionNode`/`VersionFull` 均无 `summary`，web types 同） | 采纳，且优化：加到 **`VersionFull`**（`GET /versions/{id}`）而非 subagent 建议的 `VersionNode`——回合的 `TurnPrompt` 已为每个回合拉该详情取 prompt，折叠行复用同一查询零额外请求。新增 `task-read-api` delta + proposal/design/tasks(3b) |
| S4 | **成立**（spec 第 64、173、181 行残留 "artifact upload" 引用） | 采纳。"Step Loop" 与 "Run Summary Event" 一并 MODIFIED，修订措辞 |
| S5 | **成立**（preview token 在路径段，默认中间件会记 path 即泄漏） | 采纳。artifacts-api delta 给 archive/preview 各补 "token never reaches access log" scenario + 实现提示（脱敏 route template）；tasks 3.5 加断言 |
| S6 | **成立**（opaque origin 下脚本型 `fetch`/XHR 跨源失败） | 采纳。design Non-Goals + artifacts-api preview requirement 明确仅支持标签型子资源 |
| S7 | **成立**（events `limit=200`，历史回合展开只发首页） | 采纳。`web-tasks-pages` delta + design D6 声明 MVP 首页 + 截断提示、不做 load-more |
| S8 | **成立**（空串回填 / 空 filepath 边界） | 采纳。task-data-model 回填规则补"空串留 NULL"；artifacts-api preview 净化加"空或 `.` → 404" |
| S9 | **成立**（proposal "终态 status" vs delta "任意 status"） | 采纳。统一为"任意 status 帧失效"，改 proposal 措辞 |
| S10 | **成立**（`LoopResult.artifacts` 唯一消费者被删） | 采纳。tasks 2.2 注明同步删字段/注明保留理由 |
| S11 | **成立**（resume 重发 artifact 事件 → EventLog 重复行） | 采纳。`web-tasks-pages` Conversation-Style Event Rendering 明示按 `artifact_id` 去重 + scenario |
| S12 | **成立**（cost 走独立 exchange，不进 task.events） | 采纳。删 design D7 / delta 中 "forwarded cost events" 例子 |
| S13 | 成立但非阻塞（MODIFIED 无法改名） | **不采纳**：保留 "Artifact Upload on Success" 名，避免 REMOVE+ADD 丢失归档连续性（与评审者"不改也不阻塞"一致） |
| S14 | **成立**（tasks/design 漏 result line） | 采纳。tasks 5.1 顺序补 result line（delta 本已正确） |
| S15 | **成立且有价值**（`(version_id, path)` 唯一性目前仅软推导） | 采纳。0010 迁移加 `(version_id, path) WHERE path IS NOT NULL` 部分唯一索引，兼作 preview 查询索引 + 硬唯一保证；task-data-model delta 补 scenario |
| S16 | 成立（草稿口吻） | 采纳。清理 design D5 / Migration Plan 的自问自答句式 |

**净结果**：新增 1 个 delta（`task-read-api`），3 个 delta 扩写（worker-agent-orchestration、web-artifact-preview、artifacts-api、task-data-model、web-tasks-pages），proposal/design/tasks 同步；tasks 新增第 3b 组。全部 16 条意见经代码核实均成立，15 条采纳、1 条（S13）有意保留。
