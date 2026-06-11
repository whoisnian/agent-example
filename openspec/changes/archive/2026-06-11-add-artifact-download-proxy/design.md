# add-artifact-download-proxy — Design

> 本文档已按 `suggestions.md` 评审意见修订（v2）：D2 双向隔离改为显式实现、Migration 修正 CSP 事实错误、D4 定稿 oss.Client 改造与响应头全集、D6 纳入 connect-src 收紧。

## Context

产物读取链路现状（`artifacts-api` + `web-artifact-preview`）：

1. 前端调 `GET /api/v1/artifacts/{id}/presign`，domain 层 `ArtifactReadService.PresignArtifact` 经 `ArtifactPresigner` 接口（入参 `oss_key`）让 `oss.Client` 签出指向 `OSS_ENDPOINT` 的 S3 预签名 GET URL（application 层只是透传）。
2. 浏览器拿 `url` 直连 OSS：下载点击导航、`<img>`、沙箱 `<iframe>`、文本 `fetch`（受 CORS）。
3. `web/index.html` CSP 为此放行：`img-src 'self' data: https: http://localhost:9000`、`frame-src https: http://localhost:9000`（**注意 `frame-src` 不含 `'self'`**）、`connect-src 'self' ws: wss: http: https:`（`http: https:` 即为 OSS 文本 fetch 放行）。

约束与既有事实：

- 预签名 URL 的 host 必须是浏览器可达的 OSS 地址 → 浏览器与开发机不同机即失败（本变更动机）。
- `<img>`/`<iframe>`/导航式下载**无法携带 Authorization 头**，任何替代方案都必须支持 URL 自含凭据。
- API 已有先例：WS 升级用 `?token=<jwt>` query 鉴权（`middleware.go` publicRoutes，键形是 gin 模板串 `c.FullPath()`）；access log / recovery / tracing fallback 均只记 `c.Request.URL.Path`，不记 query —— token-in-query 不会进 API 自身日志（已逐处核实）。
- `internal/auth` 已有 HS256 Issuer/Verifier（`AUTH_JWT_SECRET`，pin 算法与 `iss`、单一 `ErrInvalidToken` 哨兵）。**重要现实**：golang-jwt v5 默认不校验 `aud`，现 access `Verifier.Parse` 也未传 `WithAudience` —— 一个带 `aud` 的外来 token 今天会被拒绝，仅仅因为它缺 `tid` claim（`uuid.Parse("")` 失败），属偶然防护而非设计防护。
- `add-artifacts-api` design D1/D2 明确「产物字节不经过 API 进程」；本变更是对它的显式反转，需同步改 ARCHITECTURE.md。

## Goals / Non-Goals

**Goals:**

- 浏览器侧产物加载只依赖 API 同源可达；`OSS_ENDPOINT` 退化为纯服务端配置。
- 保持 presign 端点响应结构与前端数据访问层（`web-artifacts-views`）完全不变：`url` 是不透明字符串。
- CSP 全面收紧：`img-src` / `frame-src` 删 OSS 来源，`connect-src` 删 `http: https:` 通配（其放行理由——OSS 跨源 fetch——随本变更消失）。
- 同源伺服不洁 HTML 不得引入新的 XSS 面；access/download 两类 token 显式双向隔离。

**Non-Goals:**

- 不支持 Range/断点续传、不做 API 侧对象缓存（MVP 产物小，全量流式足够）。
- 不改 Worker 上传路径、不改 `artifacts` 表结构、不新增 sqlc 查询（download 路径复用 `GetArtifactWithOwner`，忽略 owner 列）。
- 不删 `/presign` 路由名（语义仍是「签发短时下载 URL」，签发者从 OSS 换成 API）。
- 不做带 `Content-Disposition: attachment` 的强制下载变体（保持与现状一致的浏览器默认行为，后续需要时另提变更）。
- NoSuchKey 与连接失败的 404/502 细分留作后续（见 D3）。

## Decisions

### D1. 复用 `/presign` 端点签发 API 相对 URL，而非新增"获取下载地址"端点

`GET /artifacts/{id}/presign` 的响应 `{url, expires_at, bytes, mime, sha256}` 结构不变，`url` 改为 `/api/v1/artifacts/{id}/download?token=<jwt>`。前端把 `url` 当不透明字符串（导航/`src`/`fetch` 都直接用），因此 web 数据层零改动，dev 环境天然走 Vite proxy 同源。

*替代方案*：新增 `/download-url` 端点并废弃 `/presign` —— 多一轮路由改名、前端 hook 改名，无行为收益。`/presign` 名字保留，含义微调为「API 签发的短时 URL」。

### D2. 下载鉴权用专用 scope 的短时 JWT（query 参数），两类 token 显式双向隔离

`internal/auth` 新增产物下载 token 的 Issue/Verify（与 access token 同一 `AUTH_JWT_SECRET`、同 HS256 pin、同 `iss = "agent-api"` pin），claims：`aud = "artifact-download"`、`sub = artifact_id`、`exp = mint + OSS_PRESIGN_TTL`。隔离必须**双向显式实现**，不依赖 claim 形状差异的副作用：

- 下载 Verify：pin 算法 + pin `iss` + **要求 `aud` 匹配**（`jwt.WithAudience`）+ 校验 `sub == 路径 artifact_id`，所有失败收敛为单一哨兵。
- access Verify（`Verifier.Parse`）：**新增显式拒绝任何携带非空 `aud` 的 token**（`len(c.Audience) > 0 → ErrInvalidToken`）。现状它拒绝下载 token 只是因为缺 `tid`（偶然防护）；一旦未来给下载 token 加 `tid`（如审计），它会被当作合法 access token、`Principal.UserID = artifact_id` —— 必须现在就关死。

token 只绑定 `artifact_id`：所有权在 mint 时已经由 `GetArtifactWithOwner` 校验过，download 端不重复做 owner 检查（与 S3 预签名「持 URL 即持权」语义一致，泄露面同样由短 TTL 控制）。JWT `exp` 是秒级时间戳：mint 时刻先截断到整秒再加 TTL，保证 `expires_at`（RFC3339）与 token `exp` 严格一致。

*替代方案①* 直接要求 download 路由带 Bearer access token —— `<img>`/`<iframe>` 做不到，pass。
*替代方案②* 把 access token 放 query —— 权力过大（全 API 面）且 TTL 长，泄露代价高，pass。
*替代方案③* 独立 HMAC 方案（自拼 `artifact_id|exp|sig`）—— 重新发明 JWT，还要自己处理编码与时钟偏移，pass。

### D3. download 路由是 public route + 自带 token 校验，错误码对齐 S3 语义

`GET /api/v1/artifacts/:artifact_id/download` 注册进 publicRoutes（同 WS 先例；键必须用 gin 模板串 `:artifact_id` 形式，publicRoutes 按 `c.FullPath()` 匹配），handler 内自行校验：

- token 缺失/签名错/过期/iss 或 aud 不符/`sub != artifact_id` → **403 `invalid_download_token`**（单一错误码，不区分原因 —— 对齐 `auth.ErrInvalidToken` 的不可枚举原则，也对齐 S3 对过期预签名 URL 回 403 的行为；前端「iframe 内错误不可探测、Refresh 重签恢复」的既有契约不变）。
- token 合法但 artifact 行已不存在 → 404 `artifact_not_found`。
- OSS 取对象失败（含对象被生命周期清掉）→ 502 `oss_unavailable`（区别于 API 自身 500，不泄露 oss_key 与 OSS 错误内幕）。**实现机制**：`kindToHTTP` 最高只到 503，无 502 落点 —— 采用「哨兵错误 + `MapError` switch 直映射」（仿 `ErrArtifactNotFound` 分支），不新增 Kind，不得用 `KindUnavailable`（那会拿到 503）。SDK 完全能区分 NoSuchKey（`errors.As(&types.NoSuchKey)`），MVP 先归并为 502，细分为 404 留作后续；运维侧注意 502 告警阈值勿把产物对象缺失误判为 API 集群故障。

路径参数沿用 `parseUUIDParam`（400 `invalid_input` 先于一切）。

### D4. 流式转发 + 响应级 CSP sandbox 防同源 XSS；业务下沉，handler 保持薄

`oss.Client` 改造：`presign *s3.PresignClient` 字段**替换**为 `*s3.Client`（GetObject 需要裸 client）；presign client 与 `PresignGet` 整体删除（presign 路径改为本地签 token，见 D5）；新增 `GetObject(ctx, key)` 返回 body reader + content-length。包注释同步改写（不再是 presign-only，bytes 流经 API）。

分层归属（AGENTS.md §4.1，对齐 artifact_reads.go 既有模式）：domain/application 提供取流方法（如 `OpenArtifactObject(ctx, artifactID) (meta, io.ReadCloser, error)`——按 id 复用 `GetArtifactWithOwner` 取 `oss_key`/`mime`、忽略 owner 列，再调 OSS）；HTTP handler 只做 `parseUUIDParam` → token 校验 → 调方法 → 设头 → `io.Copy`。GetObject 必须用 `c.Request.Context()`（客户端断开经 ctx 取消传播到 SDK body 读取），body 在所有路径上 `Close`。

响应头全集：

- `Content-Type`：取 artifact 行的 `mime`，空则 `application/octet-stream`（不信任 OSS 回的 content-type，统一以 DB 元数据为准）。
- `Content-Length`：OSS `GetObjectOutput.ContentLength` **非 nil 即设置**（0 字节对象合法，按 `>0` 判断会让空产物走 chunked）。
- **`Content-Security-Policy: sandbox allow-scripts`**：关键安全头。HTML 产物从 OSS 跨源变为 API 同源伺服后，若用户把下载 URL 直接开在顶层标签页，文档内脚本将运行在 API 源上（存储型 XSS）。响应级 CSP `sandbox` 强制该文档进入 opaque origin —— 顶层打开也无法触碰 API 源的存储/凭据；`allow-scripts` 保住 iframe 渲染预览能力（与 iframe `sandbox` 属性取交集，行为同现状）。
- **`Referrer-Policy: no-referrer`**：CSP `sandbox` 不限制文档内网络加载，恶意 HTML 可用 `<meta name=referrer content=unsafe-url>` 覆盖浏览器默认策略、把自己的带 token URL 经 Referrer 外送 —— 此头一行杜绝整类问题（纵深防御；该 token 本就只解锁它自己）。
- `X-Content-Type-Options: nosniff`、`Cache-Control: private, no-store`（URL 短时效，缓存无意义且有泄露面）。

中途流失败（headers 已发出）只能断连接，不可能再写 JSON envelope —— 断连 + error log + metric（`stream_aborted` 标签，见 D5），接受这一固有限制。

### D5. presign 路径不再外呼 OSS；presigner 接口换形；指标拆分

domain 层 `ArtifactPresigner` 接口换形：`PresignGet(ctx, key string)` → `SignDownload(artifactID uuid.UUID) (url string, expiresAt time.Time, err error)` —— token 的 `sub` 是 artifact_id，与 `oss_key` 无关，presign 路径不再消费 `oss_key`。接口留在 domain，实现挂 `internal/auth`（或 infrastructure 薄封装），分层不动；`ArtifactReadService.PresignArtifact`（domain）的所有权解析不变，application 层继续透传。`expires_at = mint(截秒) + OSS_PRESIGN_TTL` 语义不变。

指标调整：

- `OSSPresignTotal` 保留（现在度量本地签发，error 仅剩签名失败的罕见路径，注释同步）。
- 新增 `OSSDownloadTotal{status: success|token_invalid|not_found|oss_error|stream_aborted}` 与 `OSSDownloadBytes` counter —— 满足 AGENTS.md「每新增外部调用至少加一个 metric」。

### D6. CSP 全面收紧；CORS 降级仅存在于 spec 与注释层

`web/index.html`：`img-src 'self' data:`、`frame-src 'self'`、**`connect-src 'self' ws: wss:`**（删 `http: https:` 通配 —— 其当年的放行理由就是 OSS 文本 fetch，理由已消失；留着等于给 XSS 后的任意外传开门。`ws: wss:` 保留给 realtime WS。实现期跑全量 vitest + 手测 WS 确认无其它隐性依赖）。

代码现实：`ArtifactPreviewPanel.tsx` **没有** CORS 专用降级分支 —— 文本 fetch 失败只落通用 error phase（"Preview unavailable. Download the file instead."），CORS 只活在三处注释里。因此 web 侧改动是：更新过时注释 + CSP 收紧 + MSW mock 改相对 URL；「CORS 降级」的删除发生在 **spec 层**（旧 web-artifact-preview L73 的 CORS 门槛要求），不是代码层。

## Risks / Trade-offs

- [产物字节占用 API 带宽/连接] → MVP 产物以 KB~MB 级文本/图片为主；流式转发不占堆内存；后续大产物可再提变更引入 CDN/网关直通，本变更不堵死该路（presign 端点契约未变，签发者可再换回）。
- [慢客户端长期占用连接] → `server.go` 只设了 `ReadHeaderTimeout`、无 `WriteTimeout`（大文件流不会被掐的另一面）：慢客户端可长期占住一条 OSS 连接 + goroutine。MVP 接受；后续可用 `http.ResponseController.SetWriteDeadline` 做逐写超时。
- [token 出现在 URL，可能被浏览器历史/Referrer/前置代理日志泄露] → TTL 短（默认 5m）、单对象 scope、`Cache-Control: no-store`、`Referrer-Policy: no-referrer`（D4）；API 自身日志已核实只记 path；**部署期残余风险**：前置 nginx/Caddy 默认 access log 记完整 request line（含 query）—— 在 DEVELOPMENT.md 部署注意事项记录「关闭/脱敏该路由的 query 记录，或接受 5m TTL 残余风险」。
- [同源伺服恶意 HTML] → 响应级 `CSP: sandbox allow-scripts`（D4）；iframe 侧 sandbox 属性继续禁 `allow-same-origin`，双保险。
- [`expires_at` 与 JWT `exp` 的 30s verify leeway 不一致] → leeway 只放宽不收紧，客户端契约（过期重签）不变，可接受。
- [流中断时无法回写错误 envelope] → 前端既有「iframe 内失败不可探测 + Refresh 恢复」契约本就覆盖此类故障；fetch 路径表现为网络错误走内联错误分支；`stream_aborted` metric 可观测。
- [对象缺失归并 502 的语义降级] → 旧行为是 OSS 直接回 404；前端不区分，MVP 可接受；NoSuchKey→404 细分留作后续（D3）。
- [偏离 D1/D2「字节不经 API」] → 在 ARCHITECTURE.md 显式改写该决策并注明动机（拓扑解耦 > 进程带宽），保留回退路径（见第一条）。

## Migration Plan

**API 与 web 改动绑定同一次发布**（同仓库同 PR/同 tag 上线）。原因：现行 CSP `frame-src` 是 `https: http://localhost:9000`，**不含 `'self'`** —— 若先 API 后 web 分两次发布，过渡窗口内旧前端在 http 环境（本地 dev、局域网 `http://<lan-ip>:5173`，恰是本变更的动机场景）拿到相对 download URL 后，HTML iframe 预览会被 `frame-src` 拦截（`https:` 不匹配 http 同源）；`img-src` 有 `'self'` 不受影响，下载是顶层导航也不受影响。反过来「先 web 后 API」则新 CSP 拦截旧 OSS 绝对 URL，同样破。同发版把窗口压到 ≈0；若确需分步，按「web 先加 `frame-src 'self'`（保留 OSS 来源）→ API 上线 → web 删 OSS 来源」三步走。回滚同理整体回退。无数据迁移；无新环境变量（`OSS_PRESIGN_TTL`/`AUTH_JWT_SECRET` 均为既有配置）。

## Open Questions

（无 —— 错误码机制、token claims、响应头集合、接口换形、CSP 范围均已定案；实现期若发现 SDK 的 GetObject 流语义有出入，按 D4 原则就地处理并回写本文档。）
