# Review Suggestions: add-semantic-task-title

## S1 重投/接管路径下标题会重复生成，(run_id, seq) 幂等挡不住
- **严重度**: blocker
- **位置**: design.md D2/D6、specs/worker-execution-runtime/spec.md "Semantic Title Generation"、tasks.md §2
- **问题**: spec delta 的触发条件是 "when a consumed `TaskExecuteMessage` carries `gen_title=true`"，即**每次消费**都会生成。但既有 `worker-messaging` "Idempotent Consumption" 明确存在三条再消费路径：crash 后 broker requeue、stale-heartbeat takeover（"proceed from latest checkpoint"）、attempt_no>1 重投。这些路径下 `gen_title` 仍为 `true`，Worker 会再做一次 LLM 调用、再发一条 `kind="title"` 事件。design D6 声称 "`(run_id, seq)` 冲突时整体 no-op（既有幂等语义覆盖标题更新）"——这个安全性主张不成立：`EventPublisher` 的 seq 拒重仅限 "within a process lifetime"（worker-messaging "Event Publisher"），接管进程的 seq 计数器与原进程不保证对齐（恢复后续发事件必须避开已用 seq，否则合法事件反被 dedup 吞掉），因此重新生成的 title 事件大概率携带**新的 seq**，ingest 视为全新事件照常落库并再次更新 `tasks.title`。后果：每次重投多烧一次 LLM 成本；不同次生成结果不同导致标题在任务执行中/结束后"翻面"；且 D6 不做 terminal 守卫使晚到的重复标题必然生效。仓库内已有同型问题的处理先例：`worker-artifact-inheritance` 明确 "the worker SHALL skip inheritance entirely when the run is not fresh (a prior checkpoint exists)"，并补充了正确性不依赖该跳过的兜底（`(version_id, oss_key)` upsert）。本提案两层兜底都没有。
- **建议**: ① 在 worker-execution-runtime delta 中增加 fresh-run 守卫：仅当 `ctx.checkpoint_store.latest()` 为 `None`（且可叠加 `attempt_no == 1`）时才调用 TitleGenerator，并补一个 "Redelivered run does not regenerate the title" scenario；② 在 design 中如实陈述残余窗口（首个 checkpoint 写入前 crash 的重投会再生成一次，参照 artifact-inheritance 的措辞声明可接受），删除 D6 中 "(run_id, seq) 幂等覆盖重复生成" 的错误论证；③ tasks.md §2 增加对应单测条目（带 checkpoint 的重投不调 LLM、不发 title 事件）。

## S2 realtime-gateway 规格的 kind 枚举与 `kind="title"` 冲突，"零改动"主张不成立
- **严重度**: major
- **位置**: proposal.md "不改：realtime-gateway"、specs 缺少 realtime-gateway delta
- **问题**: `openspec/specs/realtime-gateway/spec.md` "Subscription Protocol" 是规范性条文："The server→client event frame shape MUST be exactly `{topic, kind, seq, ts, payload}` where `kind ∈ {status, log, step, artifact, error}`"。`title`（以及更早的 `plan`，那是既有遗留欠账）不在枚举内。提案一边把 ARCHITECTURE §5.2 的 WS kind 枚举改为含 `title`（proposal What Changes 最后一条、tasks 5.1），一边声称 realtime-gateway 规格零改动——归档后 specs 与 ARCHITECTURE 将直接互相矛盾。代码层面 gateway 按 `event.#` 透传或许真不用动，但规格层面该枚举必须更新。
- **建议**: 增加一个最小的 `specs/realtime-gateway/spec.md` MODIFIED delta：把枚举改为开放式措辞（如 "kind is the worker event's kind verbatim; the currently emitted kinds include status/log/plan/step/artifact/error/title"）或至少把 `title`（顺带补 `plan`）加入枚举，与 §5.2 的更新同步；tasks.md §5 增加对应条目。

## S3 "iterate/rollback 不带 gen_title" 的规范性条款放错了 requirement，且 rollback 根本不在本 delta 覆盖的 capability 里
- **严重度**: minor
- **位置**: specs/task-write-api/spec.md "Create Task Endpoint" 第三段；tasks.md 3.2
- **问题**: "Iterate and rollback execute payloads never carry `gen_title: true`" 这句 MUST 级约束写在 Create Task Endpoint requirement 内部，但 iterate 的 payload 契约由同文件的 "Iterate Task Endpoint" requirement 管辖（本 delta 未 MODIFIED 它），rollback(branch) 的 execute payload 则属于 `task-rollback-api` capability（完全无 delta）。tasks.md 3.2 要测试 "iterate/rollback payload 不含该标记"，但该断言在它所属的 requirement/capability 中没有规范落点；归档后读 Iterate/rollback 规格的人看不到这条约束。
- **建议**: 要么把该句改为非规范性说明（"only the create path may set it"），并给 "Iterate Task Endpoint" 加一个最小 MODIFIED（payload 契约句补 "and MUST NOT set `gen_title`"）、给 task-rollback-api 同样补一句；要么在 design 中明确以 "payload 缺省即 false、只有 create 显式写 true" 的白名单语义自洽，并把 3.2 的 iterate/rollback 断言标注为回归测试而非新契约。

## S4 ARCHITECTURE §6.3 的 resume 重发 execute 路径未被讨论
- **严重度**: minor
- **位置**: design.md D1/D7；docs/ARCHITECTURE.md §6.3
- **问题**: ARCHITECTURE §6.3 写明 pause 时 Worker "ack 当前 execute message"、"resume 时 API 重新 publish execute message"。已实现的 `worker-control-handling` 实际采用进程内阻塞（pause_token），并无 resume 重发，所以**当前**不会因 resume 复制出第二条带 `gen_title: true` 的 execute 消息；但本提案正在扩展 §5.3 的 execute 契约，却完全没提 §6.3 这条文档化路径——若将来按 §6.3 落地 resume 重发，API 重组 payload 时是否携带 `gen_title` 无据可依（携带则又撞上 S1 的重复生成）。
- **建议**: 在 design D1（或 Risks）补一句：resume/retry 等任何由 API 重发的 execute 消息 MUST NOT 重置 `gen_title: true`（或说明 S1 的 fresh-run 守卫使其无害）；tasks 5.1 改 §5.3 时顺带在字段说明里注明 "create-only flag"。

## S5 标题生成不检查 cancel token，且发生在 agent 解析之前
- **严重度**: minor
- **位置**: design.md D2；specs/worker-execution-runtime/spec.md "Semantic Title Generation"
- **问题**: 生成发生在 claim 之后（控制绑定已建立、cancel 可达），但 TitleGenerator 不是 step boundary，spec 未要求其检查 `ctx.cancel_token`：cancel 到达后仍会完成最多 10s 的 LLM 调用并发出 title 事件，然后才 dispatch agent、在第一个 boundary 抛 `CancelledError`。另外生成先于 `ExecutionDispatcher.dispatch`，对未注册 `task_type` 的消息（既有规格走 `AgentNotImplementedError` → error 事件 → DLX）会先白烧一次 LLM 调用。
- **建议**: 在 requirement 中补两句：调用 LLM 前检查 `ctx.cancel_token`，已置位则跳过生成（计入 skip 而非 failure）；并建议先确认 `AgentRegistry` 能解析该 `task_type` 再生成（或明确接受这笔浪费并写进 Risks）。

## S6 `WORKER_TITLE_MODEL_KEY` 缺省回退 "agent 默认 model key" 语义不明确
- **严重度**: minor
- **位置**: design.md D3；specs/worker-execution-runtime/spec.md 第三段；tasks.md 2.2
- **问题**: "falling back to the agent default model key when unset" —— `worker-agent-orchestration` 中 model_key 是**按 agent（task_type）**配置的（"Agents MUST obtain their chat model through an injected `ModelFactory.get(model_key)`"），而 TitleGenerator 在 consumer 层、agent 装配之前运行，"the agent default model key" 既可能指当前消息 task_type 对应 agent spec 的 key，也可能指某个全局缺省。两种实现成本与可测性不同，spec 应可判定。
- **建议**: 明确写成其一，例如 "falls back to the model key of the agent registered for the message's `task_type`"，或干脆定义一个全局缺省 key 常量；tasks 2.2 的措辞同步。

## S7 截断规则中 `…` 是否计入上限、以及 "column constraint" 表述不准确
- **严重度**: minor
- **位置**: specs/worker-execution-runtime/spec.md 净化段；specs/task-event-ingest/spec.md "Oversized payload title is truncated" scenario
- **问题**: 两处 delta 都写 "truncate ... to at most 64 runes AND at most 200 bytes (appending `…` when truncation occurs)"，未说明追加的 `…`（3 字节 / 1 rune）是否计入上限——原 task-write-api 的 scenario 用 "suffixed with `…`, AND MUST NOT exceed 200 bytes" 消除了字节侧歧义，本 delta 的 worker 侧 scenario 只说 "within 64 runes and 200 bytes with a trailing `…`"，rune 侧依旧两可（64+1 还是 ≤64 含 `…`），两端实现若取不同口径会导致 ingest 对 worker 已净化的标题二次截断。另外 ingest scenario 说 "never violates the column constraint"，但 `task-data-model` 中 `tasks.title` 是 `TEXT NOT NULL`，并无长度列约束——该断言空转，真正要守的是应用层 200 字节规则。
- **建议**: 两处统一为与原规格 scenario 相同的明确措辞（最终串含 `…` 后仍 ≤64 rune 且 ≤200 字节），并把 ingest scenario 的 "column constraint" 改为 "the `NOT NULL` constraint and the application-level 64-rune/200-byte rule"。

## 处置结果（已逐条核实并落实）

- **S1 采纳**：核实成立（`EventPublisher` seq 拒重确为 "within a process lifetime"；`worker-artifact-inheritance` 确有 fresh-run 先例）。worker-execution-runtime delta 增加 fresh-run 守卫（`ctx.checkpoint_store.latest()` 为 `None`）+ "Redelivered run with a checkpoint does not regenerate the title" scenario；design D2 列前置条件并声明残余窗口接受、D6 删除错误幂等论证改为 last-write-wins；ingest delta 补 last-write-wins 条文；tasks 2.3/2.5 补守卫与单测。
- **S2 采纳**：核实成立（gateway 规格确有规范性 `kind ∈ {...}` 枚举）。新增 `specs/realtime-gateway/spec.md` MODIFIED delta：枚举改为 "worker kind 原样透传" 措辞并纳入 `title`（补 `plan` 欠账），加 title 透传 scenario；proposal 同步。
- **S3 采纳（取建议的后一方案）**：Create Task Endpoint delta 中该句改为 create-only 白名单语义的说明（缺省即 false、唯一产生者是 create 派生路径），不再对 iterate/rollback 下规范性 MUST；tasks 3.2 标注为回归断言。不给 Iterate/task-rollback-api 加 delta，避免本变更范围膨胀。
- **S4 采纳**：design D1 明确 "API 任何重发（含未来 §6.3 resume 重发）不得重置 `gen_title: true`"；tasks 5.1 要求 ARCHITECTURE §5.3 注明 create-only flag。叠加 S1 守卫双重兜底。
- **S5 采纳**：worker-execution-runtime delta 前置条件加入 cancel token 检查（计 skip 不计 failure）与 AgentRegistry 解析检查（DLX-bound 消息不白烧 LLM），各补 scenario；design D2 / tasks 2.3-2.5 同步。
- **S6 采纳**：回退语义固定为 "该消息 `task_type` 所注册 agent 的 model key"（S5 的注册检查保证此时 agent 必已注册）；design D3 / spec / tasks 2.2 同步。
- **S7 采纳**：worker 与 ingest 两侧截断统一为 "最终串含 `…` 后 ≤64 rune 且 ≤200 字节"；ingest scenario 改述为应用层规则（`tasks.title` 为 `TEXT NOT NULL`，无长度列约束）。

## 总评

提案整体方向正确：触发标记由唯一知情方（API）决定、生成与回写严格沿 Worker→事件→ingest 链路，未触碰 "api 不调 LLM / worker 不直写 tasks 主表 / LLM 必过 cost_meter" 三条红线；MODIFIED 的 Create Task Endpoint 完整复制了原 requirement 全部条文与 scenario，仅做语义叠加；D7 的部署顺序与未知字段容忍、ingest 与事件落库同事务的设计也与既有规格自洽。web 侧 "零改动" 的主张经核对成立（Task Detail 对任意 `task:` frame 失效查询、事件日志对未知 kind 有通用渲染路径）。主要缺口集中在两点：一是幂等论证（S1）——design 把 `(run_id, seq)` 幂等当作重复生成的兜底，这在重投/接管路径下不成立，必须按 artifact-inheritance 的先例补 fresh-run 守卫，这是合入前必须解决的问题；二是 realtime-gateway 规格的 kind 枚举（S2）——"零改动" 在代码上成立、在规格上不成立，需要一个一句话级别的 delta。其余为契约落点与措辞精确性问题，修订成本都很低。prompt 注入与 10s 阻塞的权衡在 Risks 中已有合理交代，未发现需额外整改之处。
