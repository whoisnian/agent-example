# Review: add-artifact-download-proxy

针对四份工件（proposal / design / tasks / specs delta）的架构与安全评审，所有论断均对照仓库现状代码核实过。每条注明依据与置信度。

---

## 必须修复

### 1. design D2 的关键假设「token confusion 不可能」与代码现实不符 —— Bearer 路径今天拒绝下载 token 纯属偶然

- **问题**：D2（design.md L43-49）断言「Verify 要求 aud 匹配，access token 与 download token 互不可用（token confusion 不可能）」。前半句只对**新增的 download Verifier** 成立；反方向（download token 混入 Bearer 中间件）**没有任何 aud 防护**：
  - `api/internal/auth/auth.go:90-98` 的 `Verifier.Parse` 用 golang-jwt v5（go.mod:11，v5.3.1），解析选项只有 `WithValidMethods` / `WithIssuer` / `WithExpirationRequired` / `WithLeeway`。**golang-jwt v5 默认不校验 `aud`**（仅在传入 `jwt.WithAudience` 时校验），所以一个带 `aud="artifact-download"` 的 token 不会因 aud 被 access 校验路径拒绝。
  - 它今天会被拒绝的真实原因是：下载 token 不含 `tid` claim → `auth.go:102` `uuid.Parse(c.Tenant)`（空串）失败 → `ErrInvalidToken`。这是**偶然防护**，不是设计防护。一旦未来有人「顺手」给下载 token 加上 `tid`（比如为了审计），Bearer 中间件会把它当成合法 access token 接受，且 `Principal.UserID = artifact_id` —— 一个公开 query 参数里流转的短 token 升格为全 API 面凭据。
- **依据**：auth.go:39-42（claims 结构）、auth.go:90-108、middleware.go:134-153；golang-jwt v5 aud 校验语义。
- **建议**：
  1. 把 tasks 1.2 的「access token Verify 路径加 aud 拒绝**或确认现状已拒绝**」改为**强制实现**：access `Parse` 显式拒绝任何携带非空 `aud` 的 token（一行：`len(c.Audience) > 0 → ErrInvalidToken`），不要依赖 `tid` 缺失这个副作用。spec delta L11 已写 "download tokens MUST NOT be accepted by the Bearer middleware"，与此对齐，design D2 的措辞改为「显式双向隔离」。
  2. 顺带：D2 未提下载 token 的 `iss`。建议下载 Verify 同样 pin `iss = "agent-api"`（与现有 Verifier 一致），并在 tasks 1.1 的单测清单中加「缺 iss / 错 iss」用例。
- **置信度**：高（已读源码 + 库行为明确）。

### 2. Migration Plan 的「旧前端天然兼容」论证错误：现行 CSP `frame-src` 没有 `'self'`

- **问题**：design Migration Plan（L94）声称「旧前端拿到相对 URL 仍能用…… CSP `'self'` 本就放行同源」。核实 `web/index.html:10`：现行策略是 `img-src 'self' data: https: http://localhost:9000; frame-src https: http://localhost:9000` —— **`frame-src` 不含 `'self'`**。「先 API 后 web」的过渡窗口内，旧前端在 http 环境（本地 dev / 局域网 `http://<lan-ip>:5173`，恰是本变更的动机场景）拿到相对 download URL 后，HTML iframe 预览会被 CSP `frame-src` 直接拦截（`https:` scheme-source 不匹配 http 同源；`http://localhost:9000` 不匹配 `:5173`）。`img-src` 有 `'self'`，图片不受影响；下载是顶层导航，不受页面 CSP 约束；只有 HTML 渲染预览破。生产若是 https，则被 `https:` 恰好放行 —— 仍是巧合而非设计。
- **依据**：web/index.html:10；design.md L94。
- **建议**：三选一并写回 Migration Plan：(a) 接受过渡期 HTML 预览短暂破损并明示（同仓库同发版时窗口≈0，最省事）；(b) 把 web 的 CSP 改造拆两步——先发「`frame-src` 加 `'self'`（保留 OSS 来源）」，API 上线后再发「删 OSS 来源」；(c) API 与 web 改动合入同一次发布并声明二者绑定。当前文本以一个不成立的事实为论据，必须改。
- **置信度**：高（CSP 源文件逐字核对）。

### 3. 遗漏对 `web-artifacts-views` 的 spec delta，且两个既有 capability 的 Purpose 在归档后会变成谎言

- **问题**：proposal L16 说「不改 `web-artifacts-views` 数据访问层**代码**」—— 代码确实不用改，但规格文本会失真：
  - `openspec/specs/web-artifacts-views/spec.md:5`（Purpose）：“an on-demand single-object presign action … that re-mints a short-lived **OSS URL** on each invocation” —— 变更后 url 是 API 自签相对路径，不再是 OSS URL。
  - `openspec/specs/artifacts-api/spec.md:5`（Purpose）：“**The API never proxies object bytes** — … presigned GET URLs that the browser uses to download **directly from OSS**” —— 与本变更正面冲突。delta 只含 ADDED/MODIFIED requirements，**不会自动改写 Purpose**；归档后该 capability 的 Purpose 与自己的 requirements 互相矛盾。
- **依据**：两份既有 spec 原文；delta 文件中无 web-artifacts-views 目录、无 Purpose 改写说明。
- **建议**：(a) 增加 `specs/web-artifacts-views/spec.md` delta（或最小限度在该 spec 的 Purpose/Requirement 措辞上把 "OSS URL" 改为 "API-signed download URL (opaque relative path)"）；(b) 在 tasks 5.x 增加一条「归档时同步改写 artifacts-api 与 web-artifacts-views 的 Purpose 段」，避免依赖归档者自觉。
- **置信度**：高。

### 4. design D4 首句残缺且自相矛盾

- **问题**：D4（design.md L63）原文：「构造时保留 `*s3.Client`，presign client 仍在用于…… 不再用于 GET 签名，presign client 可整体移除」。「仍在用于……」是未写完的句子，且「保留/移除 presign client」前后矛盾；而 D5 与 tasks 2.2 明确「删除 presign client 与 `PresignGet`」。design 是实现依据，这句必须定稿。
- **依据**：design.md L63；tasks.md 2.2；oss/client.go:29-37（现在 `Client` 结构体**只持有** `presign *s3.PresignClient`，没有保留裸 `*s3.Client` —— 即「构造时保留 `*s3.Client`」也与现状不符，新实现需要把字段从 PresignClient 换成 `*s3.Client`）。
- **建议**：改写为：「`Client` 的 `presign *s3.PresignClient` 字段替换为 `*s3.Client`（GetObject 需要裸 client）；presign client 与 `PresignGet` 整体删除（D5）。」
- **置信度**：高。

---

## 建议

### 5. `ArtifactPresigner` 接口的入参必须从 `oss_key` 换成 `artifact_id`，design/tasks 均未写明；tasks 2.1 还把改造点放错了层

- **问题**：现接口 `PresignGet(ctx, key string)`（domain/task/artifact_read_service.go:19-21）以 `oss_key` 为入参；新 token 的 `sub` 是 **artifact_id**（D2），与 oss_key 无关 —— 接口签名必须换形（如 `SignDownload(artifactID uuid.UUID) (url string, expiresAt time.Time, err error)`），presign 路径不再消费 `oss_key`。同时 tasks 2.1 写「`application/task` 的 `PresignArtifact`」，但真正的逻辑在 **domain** 层（artifact_read_service.go:79-102），application 层只是一行透传（application/task/artifact_queries.go:30-32）。另外 download 路径需要「按 id 取 oss_key + mime（无 owner 条件）」：是复用 `GetArtifactWithOwner`（querier.go:51，已含 mime/oss_key，忽略 owner 列即可，无需新 sqlc 查询）还是新增查询，tasks 3.1 应二选一写死，避免实现期临时发明。
- **依据**：上列文件行号；design D2/D5、tasks 2.1/3.1 原文。
- **建议**：tasks 2.1 改为「`domain/task` 的 `PresignArtifact` + `ArtifactPresigner` 接口换形（artifact_id 入参）」；tasks 3.1 指明复用 `GetArtifactWithOwner`（推荐，零 sqlc 变更）。一个顺手的好处：接口保持在 domain 定义、JWT 签发实现放 `internal/auth` 或 `infrastructure`，分层不动。
- **置信度**：高。

### 6. download handler 的分层归属应写明，避免在 HTTP 层堆业务

- **问题**：tasks 3.1 把「token 校验 → 查 artifact 行 → OSS GetObject → 流式回写」全部描述为 handler 行为。按 AGENTS.md §4.1 的 `interfaces ↔ application ↔ domain ↔ infrastructure` 分层与本仓库读服务先例（artifact_reads.go 的 handler 只做参数解析 + 调 App + 错误映射），「查行 + 取对象」应下沉为一个 domain/application 方法（如 `OpenArtifactObject(artifactID) (meta, io.ReadCloser, error)`），HTTP 层只负责 token 校验、响应头与 `io.Copy`。
- **依据**：AGENTS.md §4.1；artifact_reads.go:32-73 的既有模式。
- **建议**：tasks 3.1 拆为「domain/application 提供取流方法（单测用 fake reader）」+「handler 组装」两条；design D4 加一句分层归属。
- **置信度**：高（约定明确，属可实现性/一致性而非阻塞）。

### 7. CSP `connect-src` 的收紧机会被放过了

- **问题**：现行 `connect-src 'self' ws: wss: http: https:`（index.html:10）—— `http:`/`https:` 通配当年正是为「文本预览跨源 fetch OSS」放行的（旧 web-artifact-preview spec L133 原文点名）。本变更后该理由消失，但 delta（spec L132）只字未提，反而写 "The existing `connect-src` already permits the same-origin text-preview fetch" 维持现状。留着 `http: https:` 等于允许页面脚本向任意源发请求（XSS 后的外传通道）。
- **依据**：index.html:10；旧 spec 与 delta 对照。
- **建议**：把 `connect-src` 收紧为 `'self' ws: wss:` 纳入本变更（与 img-src/frame-src 同一动机：OSS 跨源依赖消失），或在 design 显式说明为何暂不收（如 ws: wss: 之外还有别的依赖——核实过没有）。
- **置信度**：高（方向）；中（是否有其它隐性依赖 `http:`/`https:` 的 connect 调用——建议实现期跑全量 vitest + 手测 WS 确认）。

### 8. token-in-query 泄露面：补 `Referrer-Policy`，并记录反向代理日志这一部署期残余风险

- **问题**：design Risks（L86）只论证了浏览器历史与 API 自身 access log（后者核实属实：middleware.go:101-102、recovery.go:26、tracing fallback 均只记 path）。两处遗漏：
  1. **Referrer**：HTML 产物文档自身可以加载跨源子资源（响应级 CSP `sandbox` 不限制网络加载）。现代浏览器默认 `strict-origin-when-cross-origin` 不会把 query 带给第三方，但该默认可被文档内 `<meta name=referrer content=unsafe-url>` 覆盖 —— 恶意产物可主动把自己的带 token URL 外送（尽管该 token 只解锁它自己，影响有限，见「已验证」第 3 条）。
  2. **反向代理日志**：生产部署若前置 nginx/Caddy，默认 access log 记完整 request line（含 query）——token 会落入 API 进程之外的日志。
- **建议**：(a) download 响应加一行 `Referrer-Policy: no-referrer`（零成本，杜绝整类问题），写进 spec 响应头清单与 tasks 3.3；(b) 在 design Risks 或 DEVELOPMENT.md 部署注意事项里记录「前置代理需关闭/脱敏该路由的 query 记录，或接受 5m TTL 的残余风险」。
- **置信度**：高（机制）；Referrer 实际可利用性为低危。

### 9. 错误码与 metric 的两处机制性缺口

- **问题**：
  1. **MapError 没有 502 的落点**：kindToHTTP（errors.go:96-119）最高只有 `KindUnavailable → 503`，且 403 的默认码是 `permission_denied`。tasks 3.4 说「MapError 增加映射」，但未说机制——需要新哨兵错误直映射（仿 `ErrArtifactNotFound` 的 switch 分支）或新增 Kind；二者都行，写死一个，避免实现者用 `DomainError{Kind: KindUnavailable}` 拿到 503 而背离 spec 的 502。
  2. **流中断的 metric 标签未定义**：spec delta L26 限定 outcome ∈ {success | token_invalid | not_found | oss_error}，而 D4（L70）要求 headers 发出后的流失败「记 log + metric」——归哪个标签？四个都不贴切。
- **建议**：(1) tasks 3.4 注明采用「哨兵错误 + MapError switch 直映射」；(2) 增加 `stream_aborted` 标签（或明文归入 `oss_error`），同步 spec L26 与 D5。
- **置信度**：高。

### 10. `Content-Length` 与 `exp` 的两个边界不一致

- **问题**：
  1. design D4（L66）写「ContentLength（**>0 时**）」，spec delta L22 写 "when known"。SDK 的 `GetObjectOutput.ContentLength` 是 `*int64`，0 字节对象返回合法的 0；按「>0」会让空产物走 chunked 编码。应统一为「非 nil 即设置」。
  2. spec delta L108 场景要求 "the embedded token's `exp` MUST equal the same instant"——JWT `exp` 是秒级 unix 时间戳，若 mint 时刻带亚秒，`expires_at`（RFC3339）与 `exp` 不可能严格相等。铸造时把 mint 时刻截断到秒（或场景措辞放宽到秒级）。
- **依据**：aws-sdk-go-v2 GetObjectOutput 类型；jwt NumericDate 语义；两份工件原文互证。
- **置信度**：高（1）；高（2，措辞问题）。

### 11. 既有 MinIO 集成测试会破，且它恰是本变更最该有的测试——tasks 未显式覆盖

- **问题**：`artifact_reads_integration_test.go`（文件头注释）当前流程是「presign 拿 URL → 直接 HTTP GET 该 URL 验证字节回环」。url 变成相对路径后该测试必然要改写为「经 httptest server GET download 路由」——这正好是新链路（token 校验 + GetObject 流式 + 响应头）唯一的真实 S3 回环验证。tasks 2.3 只说「更新 presign 相关单测/契约测试」，3.6 的契约测试看不出是否含真实 MinIO 回环；遗漏会导致集成测试红着合不进去或被顺手删掉。
- **建议**：tasks 3.6 拆出/明示一条：「改写 MinIO 集成测试：presign → GET download 路由 → 断言字节与响应头全套；补一条对象缺失 → 502 的真实路径用例」。
- **依据**：artifact_reads_integration_test.go:1-9；api/go.mod:19（minio testcontainers 模块已在）。
- **置信度**：高。

### 12. 流式转发的资源占用与清理细节缺任务

- **问题**：(a) `GetObjectOutput.Body` 必须 `Close`（含错误路径），tasks 未提；(b) 客户端断开依赖 request ctx 取消传播到 SDK body 读取——用 `c.Request.Context()` 调 GetObject 即可，应写明；(c) 核实 `server.go:108` 只设了 `ReadHeaderTimeout`，**无 WriteTimeout**——好消息是大文件流不会被掐，坏消息是慢客户端可无限期占住一条 OSS 连接 + goroutine。MVP 可接受，但 design Risks 应记录（现只写了「带宽」，没写「连接占用」），后续可用 `http.ResponseController.SetWriteDeadline` 做逐写超时。
- **建议**：tasks 3.1 补 body.Close/ctx 要点；design Risks 补慢客户端条目。
- **置信度**：高。

### 13. tasks 4.2 要删的「CORS 专用降级文案分支」在代码里不存在

- **问题**：核实 `ArtifactPreviewPanel.tsx`：文本 fetch 失败只落到通用 `error` phase（L428-429 → L515-523 “Preview unavailable. Download the file instead.”），**没有** CORS 专用分支；CORS 只活在注释里（L366-371 “subject to OSS CORS”、L419、L475 “Bytes load straight from OSS”）。「CORS 降级」是旧 spec 的要求（旧 web-artifact-preview L73），实现当时就把它折叠进了通用错误。proposal L32 / D6 / tasks 4.2 描述的删除对象不存在，实现者会白找。
- **建议**：tasks 4.2 改为「更新三处过时注释（366-371 / 419 / 475）+ 确认通用错误路径不变」；proposal/D6 的措辞同步改为「删除 spec 层的 CORS 降级要求（代码本无专用分支）」。
- **置信度**：高。

### 14. 杂项（小）

- **publicRoutes 键形**：注册必须用 gin 模板串 `/api/v1/artifacts/:artifact_id/download`（publicRoutes 以 `c.FullPath()` 匹配，middleware.go:121-127），不是 `{artifact_id}` 形式——tasks 3.2 加半句防错。
- **ARCHITECTURE.md 行 148**：除 tasks 5.1 已点名的路由表（L529）与预览小节（L152），**设计系统 bullet（L148）**也写着「图片预览需 CSP img-src 含 OSS（当前 https:）；文本预览经 OSS fetch（受 CORS 约束…）」——别漏。
- **MSW mock**（handlers.ts:201-209）现返回 `https://oss.test/...`，tasks 4.3 已覆盖，确认 download mock 也要返回带正确 `Content-Type` 的字节体，否则文本预览测试（`res.text()`）拿不到内容。

---

## 可选

### 15. NoSuchKey 与连接失败可以区分，502 兜底的语义代价可再权衡

- S3 SDK 完全支持区分：`errors.As(err, &types.NoSuchKey)`（或按 smithy http status）。现方案把「对象被清」也归 502 `oss_unavailable`，与旧行为（OSS 直接回 404）相比对客户端是语义降级——前端反正不区分，MVP 归并可接受，但 design D3 可补一句「NoSuchKey 区分为 404 留作后续」，并确认 502 不会被前置代理/监控误判为 API 集群故障（502 告警通常很敏感）。置信度：高（SDK 能力）；权衡本身见仁见智。

### 16. 顶层打开 HTML 产物会把带 token 的 URL 写入浏览器历史

- 已被 TTL=5m + 单对象 scope 缓解（design Risks 已记）。若要彻底消除，未来的 `Content-Disposition: attachment` 变体（已 Non-Goal）顺带解决。无需本次行动，仅确认 Risks 条目保留。

---

## 已验证、无需修改（正面记录）

1. **access log / recovery / tracing 均只记 path 不记 query**（middleware.go:101-102、recovery.go:26、tracing fallback L52）——design L15 的断言属实，token 不会进 API 自身日志。
2. **无 cookie 泄露面**：本项目认证纯 Bearer（无 cookie）；同源 fetch 默认 `credentials: 'same-origin'` 会带 cookie，但没有 cookie 可带；download 路由也不读 cookie。结论成立。
3. **响应级 `CSP: sandbox allow-scripts` 方案成立且 `allow-scripts` 必要**：CSP sandbox 与 iframe `sandbox` 属性取**交集**，去掉 `allow-scripts` 会破坏既有「HTML 预览可执行脚本」契约；CSP 头只对文档加载生效，不影响 `<img>` 与文本 fetch；顶层打开进入 opaque origin，无法触碰 API 源存储。恶意产物即使把自己的带 token URL 外送，token 也只解锁它自己——单对象 scope 把爆炸半径压到了零附近。
4. **无 SSRF/路径注入面**：`oss_key` 来自 DB 行（Worker 写入），`artifact_id` 经 `parseUUIDParam` 钉死，bucket 固定——用户输入无法影响 API 外呼目标。
5. **错误码命名与既有体系一致**：`invalid_download_token` / `oss_unavailable` 符合 snake_case 习惯，403 单一不可枚举码与 `auth.ErrInvalidToken` 原则对齐（机制缺口见建议 9）。
6. **相对 URL 在 dev 可用**：vite.config.ts 已有 `/api` → `:8080` 代理，前端 `apiFetch` 本就用相对路径，同源假设全仓一致。

---

## 总评

方向正确：拓扑解耦的动机真实（局域网场景核实成立），「显式偏离 D1/D2 并回写 ARCHITECTURE」的处理符合仓库规则，token 设计（短时、单对象、aud 隔离）和响应级 CSP sandbox 是对的安全形态。必须修复的四条里，#1（aud 偶然防护）和 #2（frame-src 无 'self'）是工件断言与代码现实不符的硬伤；#3、#4 是规格一致性问题。修完这四条即可进入实现。

---

## 处置记录（评审后修订 v2）

逐条核实后**全部采纳**，关键事实（auth.go 无 `WithAudience`、index.html `frame-src` 无 `'self'`、`ArtifactPresigner` 以 `oss_key` 为入参、`kindToHTTP` 无 502、集成测试直连 URL 回环、面板 CORS 仅注释、ARCHITECTURE L148）已独立抽查确认。落点：

- **#1** → design D2 改「双向显式隔离」+ iss pin；spec ADDED 要求 access verifier 显式拒非空 aud + 新增 "Download token is not an access token" 场景；tasks 1.2 改强制实现。
- **#2** → design Migration Plan 重写：API 与 web 绑定同一次发布（选项 c），分步走三步法备选；proposal 加部署绑定条目。
- **#3** → `web-artifacts-views` requirement 正文无 OSS-URL 断言（仅 Purpose 段），按「无 requirement 级行为变化不出 delta」原则不加 delta，改为 tasks 5.4 归档时改写两个 Purpose。
- **#4** → design D4 定稿：字段 PresignClient→`*s3.Client`，presign client 整体删除。
- **#5/#6** → design D4/D5 + tasks 2.1/3.1/3.2：接口换形 `SignDownload(artifactID)`、取流方法下沉 domain/application、复用 `GetArtifactWithOwner`。
- **#7** → `connect-src 'self' ws: wss:` 纳入 spec delta（web-artifact-preview CSP requirement + 场景）、design D6、tasks 4.1。
- **#8** → `Referrer-Policy: no-referrer` 进 spec 响应头清单/场景、design D4、tasks 3.4；前置代理日志风险进 design Risks + tasks 5.2。
- **#9** → 哨兵 + MapError 直映射写死（design D3、tasks 3.5）；`stream_aborted` 标签进 spec/design D5/tasks 3.6。
- **#10** → ContentLength 非 nil（含 0）与 exp 截秒措辞同步 spec/design。
- **#11** → tasks 3.8 独立任务：MinIO 集成测试改写 + 对象缺失 502 用例。
- **#12** → body Close/ctx 进 tasks 3.1/3.2；慢客户端连接占用进 design Risks。
- **#13** → tasks 4.2 改为更新三处注释；proposal/design D6 措辞改为「spec 层删除 CORS 要求，代码本无专用分支」。
- **#14** → gin 模板串键形进 tasks 3.3 与 design D3；ARCHITECTURE L148 进 tasks 5.1；MSW download mock Content-Type 进 tasks 4.3。
- **#15（可选）** → design D3/Risks 补「NoSuchKey→404 留作后续 + 502 告警误判提示」。
- **#16（可选）** → 维持现状（Risks 已覆盖），无改动。
