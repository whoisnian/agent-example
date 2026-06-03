# 评审报告：add-api-auth-jwt

总评：**1 blocker / 4 should-fix / 4 nice-to-have**。提案对现有代码/契约的事实描述总体准确（5 个 handler struct 的 `Dev*` 字段、WS `4001`、信封 `httpapi.Error`、config `required:"true"` 模式、migration 无 users/tenants 表均经逐一核对属实），安全取舍（HS256 固定、`alg=none` 测试、常数时间比较、密钥 fail-fast、fail-closed、`c.FullPath()` 防欺骗）覆盖到位。主要问题是**漏改一个被影响的既有 spec**，以及若干"同 OSS_* 既有行为"的措辞与代码现状不符。

---

## Blocker（必须改）

### B1. 漏掉 `task-cost-api` 的 MODIFIED delta（身份来源被写死进契约）
- **位置**：缺失文件 `specs/task-cost-api/spec.md`（MODIFIED delta）；对应既有契约 `openspec/specs/task-cost-api/spec.md:183`，Requirement "Owner-Scoped Reads Hide Unowned Resources"。
- **问题**：该 requirement 正文第 183 行原文写死了身份来源：
  > "The owner identity is `(tenant_id, user_id)` resolved from the request **(the MVP dev-mode middleware fills these from env)**。"

  本变更的核心正是把身份来源从"dev-mode middleware / env"换成"token claims"，所以这句话在变更落地后即为**错误描述**。proposal.md:19 与 design Context 都声称受影响的 modified capability 只有 `realtime-gateway`、并称 `task-read-api` 等"行为不变、只换身份来源、spec 没把身份来源写死"——对 `task-read-api` 成立（其 §"Owner-Scoped Reads Hide Unowned Resources" 只说 "the caller's `tenant_id`/`user_id`"，未提 env），但对 `task-cost-api` **不成立**：它把 env 来源写进了契约正文。

  对照：`task-read-api/spec.md:117-119`（泛指 caller，不写死）vs `task-cost-api/spec.md:183`（显式写死 "dev-mode middleware fills these from env"）。
- **建议**：在变更中新增 `specs/task-cost-api/spec.md`，以 `## MODIFIED Requirements` 复制 "Owner-Scoped Reads Hide Unowned Resources" 整段，并把第 183 行改为：
  > "The owner identity is the authenticated `Principal{tenant_id, user_id}` resolved from the request context (set by the Bearer-token middleware from the JWT claims). A row counts as "owned" iff …（其余不变）"

  同时更新 proposal.md 的 "Modified Capabilities" 与 Impact，把 `task-cost-api` 列入受影响 spec（"行为不变、仅身份来源由 env→token"）。
- **置信度**：高。

---

## Should-fix（应改）

### S1. "与 OSS_* 相同的 redaction 规则 / config-dump log line"——既有行为并不存在
- **位置**：proposal.md:24（"The config-dump line MUST exclude the secret (same rule as `OSS_*`)"）；design.md:50（"excluded from the config-dump line (same path as `OSS_*`)"）；spec `api-auth` Requirement "JWT and Credential Configuration"（"the config-dump log line MUST exclude it (same redaction rule as the OSS credentials)"）；tasks 2.2。
- **问题**：仓库当前**没有** config-dump / config 日志行。`config.go:76` 原文自陈："the loader has no config-dump line, and if one is ever added it must redact OSSAccessKeyID / OSSAccessKeySecret"。`main.go` 也只有 `api_starting`（仅 addr）这类行，全仓 `grep` 不到任何打印 cfg 的语句，OSS 凭证**事实上从未被 redact**（因为根本没地方打印）。因此"same path/rule as OSS_*"会让实现者误以为存在一条可复用的 redaction 代码路径。需求本身（密钥绝不可入日志/响应）正确，但"沿用既有 OSS redaction"的事实前提不成立。
- **建议**：把措辞从"沿用既有 config-dump 的 OSS redaction"改为"该项目当前没有 config-dump 行（见 `config.go` 注释）；若本变更或后续新增任何 config 打印，`AUTH_JWT_SECRET`（以及 `AUTH_DEV_PASSWORD`、既有 `OSS_*` 凭证）MUST 被排除"。即把它表述为"约束/不变量"而非"复用既有路径"。
- **置信度**：高。

### S2. 新增 `POST /api/v1/auth/login` 未同步 ARCHITECTURE，且未声明偏离
- **位置**：proposal.md:7；spec `api-auth` Requirement "JWT Issuance via Login"；对照 `docs/ARCHITECTURE.md` API 路由表（约 515-531 行）与 §9（1013-1022 行）。
- **问题**：ARCHITECTURE 路由表里**没有** `/auth/login` 这一行（全文 `grep` 不到 `login`/`/auth`），§9 也只列 "JWT(短期)+Refresh+SSO" 而无登录端点。AGENTS.md §1 规定"任何与 ARCHITECTURE 冲突/未覆盖的实现必须先更新文档或在提案中显式声明偏离原因"，§7 也要求"遇到 ARCHITECTURE 未覆盖的细节先在 design 决策"。本提案新增了一个面向公网的 public 路由却既未更新文档、也未在 design 显式记一条"ARCHITECTURE 路由表新增 `/auth/login` 行"的待办。
- **建议**：二选一并写明——(a) 在 design Migration Plan / tasks 增加一条"ARCHITECTURE.md API 路由表新增 `POST /auth/login` 行（capability `api-auth`）"；或 (b) 在 design 加一句显式声明"该端点是 §9 'JWT(短期)' 的落地入口，路由表后续随 doc-sync 补齐"。当前 tasks 6.4 只提到更新 `api/README`/`.env`，未覆盖 ARCHITECTURE。
- **置信度**：高。

### S3. WS 验证从 `4001-presence` 翻成 `4001-validated`——`web-realtime-client` 契约虽不破，但应在 delta 里点名"不破"的依据
- **位置**：spec `realtime-gateway`（MODIFIED）Requirement "WebSocket Endpoint and Connection Lifecycle"；对照既有 `openspec/specs/web-realtime-client/spec.md:8,14,24`。
- **问题**：MODIFIED delta 把 "missing/empty → 4001" 扩成 "missing/empty/malformed/bad-sig/expired → 4001"。核对 `web-realtime-client`：client 仅 "append `?token=<jwt>`"（:8）、"treat 4001 as auth expired → clear token + redirect"（:14,:24），不区分 4001 的具体原因，因此扩大触发面**不破** client 契约（client 行为对所有 4001 一致）。这点判断正确；但 delta 正文未明确点出"close code 仍恒为 4001、未新增其它 close code"，实现者可能误以为可对 expired 用不同 code。另外既有 spec 文件第 11、13 行仍保留 "presence only / stub identity / 4001 gates presence" 的旧叙述，归档时需确保被本 delta 整段替换（delta 的 MODIFIED 正文已重写该段，符合 OpenSpec 的"整段替换"语义，核对无误）。
- **建议**：在 delta 正文补一句"close code 对所有失败原因恒为 `4001`（不新增 close code），以保持 `web-realtime-client` 对 4001 的统一处理不变"。
- **置信度**：中。

### S4. design Risks 未列"rollout 瞬时全量 401/4001"对**正在运行的 WS/在途请求**的影响面
- **位置**：design.md:51 "Risks — Locking out the WS/web on rollout"。
- **问题**：现有 risk 只说"flipping the stub 会拒绝无有效 token 的 client，这是预期行为、web login 随后落地"。但更具体的运维风险是：部署瞬间**所有现存 WS 长连接**（`main.go` 里长生命周期、shutdown 时才 1001）下次重连即 4001、**所有在途/缓存的无 token REST 调用**立即 401。当前没有用户表、token 只能由新 login 端点签发，意味着"先部署 API、web login 未上线"窗口内整站不可用。design 把它当"预期行为"一笔带过，低估了 ordering 依赖（API 与 `add-web-auth-login` 的上线顺序、以及对已连接 WS 的即时影响）。
- **建议**：在该 risk 下补一句 ordering/缓解："本变更与 `add-web-auth-login` 必须协调上线顺序（或先发 dev 凭证 + 文档让本地可登录）；现存 WS 连接在重连时会收到 4001，属预期。" tasks 6.4 的 README/.env 文档应包含 dev `POST /auth/login` 的可用示例（已有，保留）。
- **置信度**：中。

---

## Nice-to-have（可选）

### N1. allowlist 用 `c.FullPath()` 对 health/metrics 路由的匹配值需确认
- **位置**：design D4；tasks 4.1（"matched on `c.FullPath()`"）。
- **问题**：`/healthz`、`/readyz`、`/metrics` 注册在 `e`（根 engine）上而非 `/api/v1` group（见 `server.go:54-56`），它们的 `FullPath()` 即 `/healthz` 等，与 allowlist 字面相符；`POST /api/v1/auth/login` 的 `FullPath()` 为 `/api/v1/auth/login`。这点成立。仅提示：allowlist 应是 `{method,path}` 而非纯 path（login 仅 `POST` 公开；`/healthz` 等为 `GET`），design D4 文字提到 "`POST /api/v1/auth/login`" 但 tasks 4.1 把四项并列为路径集合，建议在 tasks 明确 login 项带 method，避免把其它动词也放行。
- **置信度**：中。

### N2. `iss`/`aud` 校验未在 spec 层固定
- **位置**：design D2（claims 含 `iss`）；spec `api-auth` "Bearer Token Authentication"。
- **问题**：design 让 token 带 `iss`，但 spec/`Verifier.Parse` 未要求**校验** `iss`（以及未提 `aud`）。MVP 单签发者下影响小，但既然签了 `iss` 就应校验，否则是装饰性 claim。
- **建议**：在 `Verifier.Parse` 任务里加"校验 `iss` 等于配置值"，或在 design 注明"MVP 不校验 `iss`，仅签发占位"。
- **置信度**：低。

### N3. `AUTH_DEV_EMAIL`/`AUTH_DEV_PASSWORD` 缺省值与 required 语义未定
- **位置**：spec "JWT and Credential Configuration"；tasks 2.1。
- **问题**：`AUTH_JWT_SECRET` 明确 `required:"true"`、`AUTH_JWT_TTL` 有默认；但 dev 凭证对（email/password）未说明是 required 还是带默认。若带默认（如 `dev@example.com`/某弱口令）会成为人人皆知的可登录后门；若 required 则需在 spec 里和 SECRET 一样 fail-fast。当前留白。
- **建议**：明确二者语义。倾向 `AUTH_DEV_PASSWORD` 设为 `required:"true"`（无默认，避免出厂弱口令），并随 SECRET 一起纳入"绝不入日志"集合（见 S1）。
- **置信度**：中。

### N4. tasks 6.2 "make sqlc 无 diff" 与本变更不涉 SQL 一致，但可顺带说明 cost handler 测试迁移面
- **位置**：tasks 5.4 / 6.x。
- **问题**：proposal Impact 列了 5 个 handler 的测试迁移，但 `task_cost_reads.go` 还含 `/pricing`（owner-agnostic，每个 caller 同响应，见 `task-cost-api/spec.md:154`）这类不依赖 principal 的端点；迁移测试时无需为其注入 principal 差异。属提示性，避免迁移时对 owner-agnostic 端点过度断言。
- **建议**：在 tasks 5.4 备注"owner-agnostic 端点（`/pricing`）测试无需 per-principal 隔离断言"。
- **置信度**：低。

---

## 处置记录（作者核实后）

逐条对照真实文件核实；**本轮 9 条全部成立、全部应用**（无降级）。

- **B1** ✅ 已应用 — 直接读 `task-cost-api/spec.md:183`，确认原文写死 "(the MVP dev-mode middleware fills these from env)"（首轮 grep 因 `| head` 截断而漏看，reviewer 正确）。新增 `specs/task-cost-api/spec.md` MODIFIED "Owner-Scoped Reads Hide Unowned Resources"，把身份来源改为 token principal；proposal Modified Capabilities + Impact 补列 task-cost-api。并 sweep 全部 specs 确认仅 task-cost-api + realtime-gateway 把身份来源写进契约，其余无需 delta。
- **S1** ✅ 已应用 — 核实 `config.go:76-77` 自陈"无 config-dump 行"，OSS 凭证从未实际 redact。proposal/design/spec/tasks 的"沿用 OSS_* redaction 路径"改为"无 dump 行；密钥+dev 口令为 never-log 不变量，若将来新增打印须排除"。
- **S2** ✅ 已应用 — ARCHITECTURE 路由表无 `/auth/login`。design Migration Plan + tasks 6.4 增"同步 ARCHITECTURE 路由表新增 `POST /auth/login` 行（AGENTS §1）"；proposal Impact 增 Docs 条。
- **S3** ✅ 已应用 — realtime-gateway MODIFIED 正文补"close code 对所有失败原因恒为 4001、不新增 code，保持 web-realtime-client 处理不变"。
- **S4** ✅ 已应用 — design rollout risk 展开：部署瞬间在途无 token REST→401、现存 WS 重连→4001，且 token 仅能由新 login 签发 → 与 add-web-auth-login 须协调上线顺序。
- **N1** ✅ 已应用 — allowlist 改为 `(method, FullPath)`，login 仅 POST 公开；design D4 + spec + tasks 4.1 同步。
- **N2** ✅ 已应用 — `Verifier.Parse` 增"校验 iss == 配置值"+ 固定 HS256；design D2 + tasks 1.2。
- **N3** ✅ 已应用 — `AUTH_DEV_PASSWORD` 设 `required:"true"`（无默认弱口令），纳入 never-log；spec + config tasks 2.1/2.3 + fail-fast scenario 同步。
- **N4** ✅ 已应用 — tasks 5.4 备注 owner-agnostic `/pricing` 无需 per-principal 隔离断言。
