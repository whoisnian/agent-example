# 评审报告：add-web-artifacts-views

总评：2 blocker / 4 should-fix / 3 nice-to-have。

提案整体方向正确，端点形状、nullable 语义、owner-scoped 404、"presign 不缓存"等核心契约描述与 `openspec/specs/artifacts-api/spec.md` 逐字一致，slice 结构忠实复用了 `features/costs/` 与 `features/tasks/` 的既有模式。主要问题集中在 presign 的 **toast 分层模型**（与既有 mutation 约定冲突，会双重弹 toast）以及 **400 malformed-UUID 分支在 spec 中完全缺失**。

---

## Blocker（必须改）

### B1 — presign mutation 的 toast 分层会双重弹出，违反既有两层 toast 约定
- **位置**：`design.md` D1 第 28 行、D4 第 43 行；`tasks.md` 1.3 / 1.4 / 2.2；`specs/web-artifacts-views/spec.md` "presign action" 段。
- **问题**：D4 明确写 presign "是我们唯一让其 toast on error 的 read（`toastOnError` default true）"，同时 D1 把 presign 建模为 `useMutation`。但 `web/src/services/query-client.ts` 第 43-45 行的 `mutationCache.onError` 对所有 mutation 默认弹 toast，**除非** `meta.silent === true`。现有三个 mutation（`web/src/features/tasks/mutations.ts` 第 24/44/68 行）无一例外都是 `meta:{silent:true}` + 对应 `api` 层 `toastOnError:false` 配对（见第 66-67 行注释 "Paired with controlTask's toastOnError:false"）。提案让 presign 走 `toastOnError:true`（transport 层弹一次）**又**不设 `meta.silent`（mutationCache 再弹一次）→ 同一个 presign 失败会弹 **两个** toast。再叠加 D4 说 "`onError` 可能再设一个 per-row hint"，最多三处。
- **建议**：明确二选一并写进 spec 与 tasks。推荐与既有约定一致：presign mutation 设 `meta:{silent:true}`，错误处理放在调用处的 `onError`（toast 或 per-row hint，由组件决定），`getArtifactPresign` 走 `toastOnError:false`。若坚持"transport 层兜底 toast"，则 mutation 必须 `meta:{silent:true}` 以避免 mutationCache 二次弹出——无论如何，"`toastOnError:true` 的 mutation 且不 silent" 是不能落地的组合。tasks.md 1.4 / 2.2 需补上 `meta` 与 `toastOnError` 的确定取值。
- **置信度**：高

### B2 — spec/scenario 完全没有覆盖 malformed UUID → 400 分支
- **位置**：`specs/web-artifacts-views/spec.md` 全部 5 个 scenario；`specs/web-tasks-pages/spec.md` 全部 scenario；`tasks.md` 4.x。
- **问题**：后端契约 `artifacts-api/spec.md` 第 19 行（list）与第 55 行（presign）都规定 "malformed `{version_id}`/`{artifact_id}` UUID MUST return `400 invalid_input`，先于 ownership 解析"，并各有独立 scenario（第 43-45、76-78 行）。提案对 404 着墨很多，但 `400 invalid_input` 在两个 spec 文件、所有 scenario、tasks 测试清单里**一次都没出现**。list 的 `retry`/`meta.silent` 写法只豁免了 404（mirror `useTaskCostQuery`），400 会落入默认 retry（最多 2 次）且**不** silent → 会弹全局 toast，这一行为既未声明也未必符合预期。
- **建议**：至少在 `web-artifacts-views` 加一条 requirement 文字与一条 scenario，声明 400（malformed UUID / `invalid_input`）的客户端行为：是否 skip-retry、是否 silent、如何渲染。考虑到 versionId 来自服务端 DTO（VersionNode.id）、artifactId 来自服务端 list，正常路径不会构造非法 UUID，可声明 "400 视为不可重试的错误态、按标准错误路径处理"，但必须显式写出而非留白。tasks 4.x 相应补一条断言。
- **置信度**：高

---

## Should-fix（应改）

### S1 — presign SDK 失败返回 500，scenario 只笼统说 "a 500"，未对齐契约 code
- **位置**：`specs/web-artifacts-views/spec.md` 第 11 行与 "Presign failure surfaces as an error" scenario（第 36-38 行）；`tasks.md` 3.1 / 4.2 / 4.3。
- **问题**：后端 `artifacts-api/spec.md` 第 95-98 行规定 presign SDK 签名失败为 HTTP `500` 且 `code = "internal_error"`，凭据不得出现在 body/log。提案 scenario 只写 "a `500`"，未点名 `internal_error`，MSW fixture 任务（3.1）只列了 `url`+`expires_at` 的成功态，500 变体仅在 D5 第 46 行口头提及 "presign-`artifact_not_found`/500 variants"，未进 tasks 的 fixture 清单。
- **建议**：scenario 中把 500 明确为 `code = "internal_error"`；tasks 3.1 增列 presign 的 `artifact_not_found`(404) 与 `internal_error`(500) 两个 `server.use()` 变体，tasks 4.3 增加 "500 → 弹错误且不导航" 的断言（与 404 同路径）。
- **置信度**：高

### S2 — design Risks 漏了 B1 的 toast 双弹风险与 presign URL "出现在 jsdom 测试断言/history" 之外的泄漏面
- **位置**：`design.md` "Risks / Trade-offs" 第 50-54 行。
- **问题**：Risks 覆盖了跨域 download 文件名、URL 泄漏、过多请求、jsdom 导航、bytes 精度，较全面。但**未**列出 B1 指出的"两层 toast 模型在 mutation 路径下的双弹/三弹"这一最可能落地踩坑的风险。此外"URL leakage"一节（第 51 行）声称 "never logged by the app"，但 `window.location.assign(url)` 会把 presigned URL 写入浏览器 history/地址栏——这是真实泄漏面（已弱化于短 TTL），值得一句缓解说明。
- **建议**：Risks 增列一条 toast 分层一致性风险（指向 B1 的决定）；并在 URL leakage 一条补充 "导航会落入浏览器 history，靠短 TTL 兜底" 的明示。
- **置信度**：中

### S3 — list 查询 retry 写法与 spec 文字一致，但 staleTime 去重声明与"presign 永不缓存"措辞需收紧
- **位置**：`design.md` D1 第 29 行、第 52 行；`specs/web-artifacts-views/spec.md` presign 段。
- **问题**：D1 第 29 行把 list query 的 retry 写成 `retry: (n,e) => !(e instanceof ApiError && e.status===404) && n<2`，与 `web/src/features/costs/queries.ts` 第 33-34 行 `useTaskCostQuery`（`failureCount < 2`）逐字一致，正确。但 design 第 52 行说 "React Query `staleTime`（30s default）de-dupes re-expands"——`web/src/services/query-client.ts` 第 25 行确为 30_000，但这是 **gc 之前的 staleTime**，collapse 后再 expand 是否复用取决于 `gcTime`（5min）与组件是否卸载查询；表述 "de-dupes re-expands" 在快速折叠/展开下成立，但 30s 后展开仍会 refetch。措辞可更精确。presign 段 "never cached" 与 mutation 模型一致，OK。
- **建议**：把 D1/Risks 的去重表述改为 "30s 内的快速重复展开命中缓存；超过 staleTime 的再展开会按 React Query 默认刷新"，避免给实现者"永不重复请求"的错误预期。
- **置信度**：中

### S4 — `web-tasks-pages` 新 requirement 未声明对既有 `aria-expanded`/testid 契约的测试约定
- **位置**：`specs/web-tasks-pages/spec.md` 整条 requirement；`tasks.md` 2.3 / 4.4。
- **问题**：`web/src/components/tasks/VersionTree.tsx` 现有稳定的 `data-testid`：`version-tree`、`version-node`、`current-marker`、`data-current`（第 53/59/68 行）。tasks 2.3 提到加 `aria-expanded` 与 local `Set<string>`，4.4 要求 "existing badges/current-marker assertions still pass"，方向对。但 spec/tasks 未给新展开控件与 ArtifactList 约定 testid（如 `version-expand-toggle`、`artifact-list`、`artifact-row`、`artifact-download`），而 4.4/4.3 的断言（"expanding fires exactly one list request"、"Download asserts navigation"）需要稳定选择器才能落地。
- **建议**：在 tasks 2.1/2.3 明确新增 testid 命名（沿用 kebab-case，如 `artifact-list` / `artifact-row` / `artifact-download` / `version-expand-toggle`），spec scenario 可不写死 testid 但 tasks 应固定，保证 scenario→测试可映射。
- **置信度**：中

---

## Nice-to-have（可选）

### N1 — `formatBytes` 进位边界与单位标签未在 spec/tasks 锁定
- **位置**：`design.md` D2 第 35 行；`tasks.md` 1.2 / 4.1。
- **问题**：D2 决定用十进制 1000、标签 KB/MB，`bytes` 直接转 `number`，论证（money 才用 decimal-string、int8 远小于 2^53）正确且与 `features/costs/format.ts` 的 decimal 纪律不冲突。tasks 4.1 已要求测 0/<1KB/MB/GB 边界。仅 `1000`（=1KB？1000B？）这种刚好进位的临界值、以及负数/`NaN` 防御未点名。
- **建议**：tasks 4.1 补一个恰好进位（如 1000、1_000_000）与 0 的断言，明确显示形如 "1.0 KB" 还是 "1 KB"。属打磨。
- **置信度**：中

### N2 — `kind` 渲染与 `artifact_root` 字段澄清
- **位置**：`proposal.md` 第 8 行；`specs/web-artifacts-views/spec.md` 第 7 行。
- **问题**：spec 正确声明 `kind` 为 opaque free-text、不枚举（对齐契约第 21 行 "today every produced file is `kind = "file"`"）。`web/src/features/tasks/types.ts` 第 84 行的 `VersionNode.artifact_root` 是 OSS 前缀、与本 change 的 artifact list 无直接关系，提案未误用它，正确。可在 design 一句话点明 "不复用 `artifact_root`，artifacts 仅来自新端点"，避免实现者混淆。
- **建议**：design 加一句澄清即可，非必须。
- **置信度**：低

### N3 — 跨域 `download` 属性与同标签导航的取舍可在 spec scenario 留痕
- **位置**：`design.md` D4 第 42-43 行；`specs/web-tasks-pages/spec.md` "Download mints a fresh URL" scenario。
- **问题**：D4 对 "跨域 `download` attr 被忽略、靠 OSS `Content-Disposition`、不 `target=_blank`（防弹窗拦截）" 的论证合理且与契约 "browser 直连 OSS、不代理字节" 一致。spec scenario 只断言 "navigate to returned url"，未提是否同标签——这是实现细节，留 design 即可，但 jsdom 里 `window.location.assign` 的断言方式（spy）建议在 tasks 4.3 写死为 "spy `window.location.assign`"（目前 4.3 已写 "spy `window.location.assign`/anchor click"，可二选一固定以减少实现分叉）。
- **建议**：tasks 4.3 固定一种导航实现+断言方式，避免实现与测试不一致。
- **置信度**：低

---

## 处置记录（作者核实后）

每条均对照真实文件核实后落地；其中 B2 经核实**降级**（理由见下）。

- **B1** ✅ 已应用 — 核实 `query-client.ts:43-45` `mutationCache.onError` 对非 silent mutation 必弹 toast，原设计 `toastOnError:true`+非 silent 确会双弹。改为 presign `toastOnError:false` + mutation `meta:{silent:true}`，组件 `onError` 为唯一错误面（与 `features/tasks/mutations.ts` 三个 mutation 一致）。改了 design D1/D4、web-artifacts-views spec presign 段+scenario、web-tasks-pages presign scenario、tasks 1.3/1.4/2.2。
- **B2** ⚠️ 降级为声明性条款（非 blocker）— 核实刚归档的 `web-cost-views` 对**同构**的 `/tasks/{id}/cost` 400 分支同样无 scenario（IDs 均来自服务端 DTO，正常路径构造不出非法 UUID）。要求完整 requirement+scenario 与既有先例不一致。改为在 web-artifacts-views spec 加一句声明（非 404 错误含不可达的 400 按 `useTaskCostQuery` 同 posture 处理），不新增 scenario、不特判 400。
- **S1** ✅ 已应用 — 500 点名 `internal_error`（对齐 artifacts-api spec:95-98）；tasks 3.1 增列 presign 404/500 fixture 变体，4.3 增 500 断言。
- **S2** ✅ 已应用 — Risks 增 "toast 双弹" 一条；URL-leakage 一条补充 `window.location.assign` 写入 history 的泄漏面+短 TTL 兜底。
- **S3** ✅ 已应用 — Risks 去重表述收紧为 "staleTime(30s) 内命中缓存，超出则按默认刷新"。
- **S4** ✅ 已应用 — tasks 2.1/2.3 固定新增 testid：`artifact-list`/`artifact-list-empty`/`artifact-row`/`artifact-download`/`version-expand-toggle`，并声明保留既有 `version-tree`/`version-node`/`current-marker`。
- **N1** ✅ 已应用 — tasks 4.1 增恰好进位（`1000`/`1_000_000`）与 0 边界、锁定单位标签/小数位。
- **N2** ✅ 已应用 — design D3 加一句澄清不复用 `VersionNode.artifact_root`，artifacts 仅来自新端点。
- **N3** ✅ 已应用 — tasks 4.3 固定为 spy `window.location.assign` 单一机制（删去 anchor-click 备选）。
