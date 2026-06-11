# add-artifact-download-proxy — Tasks

## 1. API：下载 token 签发/校验（双向隔离）

- [x] 1.1 `internal/auth`：新增产物下载 token 的 Issue/Verify（HS256、同 `AUTH_JWT_SECRET`，pin `iss="agent-api"`，`aud="artifact-download"`、`sub=artifact_id`、`exp=mint(截秒)+TTL`；Verify pin 算法 + pin iss + 要求 aud + 校验 sub 匹配，所有失败收敛为单一哨兵），单测覆盖：合法 token、过期、aud 缺失（access token 混用）、sub 不匹配、iss 缺失/错误、alg 混淆
- [x] 1.2 access `Verifier.Parse` **强制实现**显式拒绝任何携带非空 `aud` 的 token（`len(c.Audience) > 0 → ErrInvalidToken`）——现状拒绝下载 token 仅靠缺 `tid` 的副作用（golang-jwt v5 默认不校验 aud），不得依赖；回归单测：带 aud 且补齐 `tid`/`sub` 形状的 token 也必须被 Bearer 路径拒绝

## 2. API：presign 路径改造（domain 层）

- [x] 2.1 `domain/task`：`ArtifactPresigner` 接口换形 `PresignGet(ctx, key string)` → `SignDownload(artifactID uuid.UUID) (url, expiresAt, err)`（token sub 是 artifact_id，不再消费 `oss_key`）；`ArtifactReadService.PresignArtifact` 改为调新接口拼相对 URL，所有权解析 `GetArtifactWithOwner` 不变；接口实现挂 `internal/auth`（或 infrastructure 薄封装），application 层透传不动
- [x] 2.2 `infrastructure/oss`：`Client` 的 `presign *s3.PresignClient` 字段替换为 `*s3.Client`，删除 `PresignGet`，新增 `GetObject(ctx, key)`（返回 body reader + content-length，调用方负责 Close）；更新包注释（不再是 presign-only，bytes 流经 API）
- [x] 2.3 更新 presign 相关单测/契约测试：`url` 断言改为相对路径 + token 可被下载 Verifier 解出且 `sub`/`aud`/`iss`/`exp` 正确、`expires_at` 与 token `exp` 同秒

## 3. API：download 反向代理路由

- [x] 3.1 domain/application：新增取流方法（如 `OpenArtifactObject(ctx, artifactID) (meta, io.ReadCloser, error)`）——按 id 复用 `GetArtifactWithOwner`（忽略 owner 列，零 sqlc 变更）取 `oss_key`/`mime`，行缺失 → `ErrArtifactNotFound`，OSS 失败 → 新哨兵错误；单测注入 fake reader
- [x] 3.2 `interfaces/http`：新增 `GET /api/v1/artifacts/:artifact_id/download` 薄 handler——`parseUUIDParam` → token 校验（失败统一 403 `invalid_download_token`）→ 调取流方法 → 设头 → `io.Copy`；GetObject 用 `c.Request.Context()`（客户端断开经 ctx 取消传播），body 所有路径 Close
- [x] 3.3 注册 public route：键必须用 gin 模板串 `/api/v1/artifacts/:artifact_id/download`（publicRoutes 按 `c.FullPath()` 匹配）；确认 access log 不记录该路由 query string
- [x] 3.4 成功响应头齐套：`Content-Type`（DB mime 优先，空则 octet-stream）、`Content-Length`（ContentLength 非 nil 即设置，含 0）、`Content-Security-Policy: sandbox allow-scripts`、`Referrer-Policy: no-referrer`、`X-Content-Type-Options: nosniff`、`Cache-Control: private, no-store`
- [x] 3.5 `MapError`：用「哨兵错误 + switch 直映射」（仿 `ErrArtifactNotFound`）增加 403 `invalid_download_token` 与 502 `oss_unavailable`——`kindToHTTP` 无 502 落点，不得用 `KindUnavailable`（503）；headers 已发出后的流中断走断连 + error log
- [x] 3.6 metrics：新增 `OSSDownloadTotal{status: success|token_invalid|not_found|oss_error|stream_aborted}` 与 `OSSDownloadBytes`；调整 `OSSPresignTotal` 注释（本地签发）
- [x] 3.7 契约测试（httptest）：合法 token 流出字节与全套响应头（含 0 字节对象的 `Content-Length: 0`）；403（缺 token / 过期 / access token 混用 / sub 不匹配，单一错误码）；400 malformed uuid；404 行已删除；502 OSS 不可达；token 不出现在日志字段
- [x] 3.8 改写既有 MinIO 集成测试（`artifact_reads_integration_test.go`，原「presign → 直连 URL」回环必破）：改为「presign → 经 httptest server GET download 路由 → 断言字节与响应头全套」，补一条「对象在 OSS 缺失 → 502」的真实路径用例

## 4. Web：CSP 收紧与预览适配

- [x] 4.1 `web/index.html` CSP：`img-src 'self' data:`、`frame-src 'self'`、`connect-src 'self' ws: wss:`（删 `http: https:` 通配），同步注释；`web/AGENTS.md` CSP 小节更新；跑全量 vitest + 手测 WS 确认 connect-src 收紧无隐性依赖
- [x] 4.2 `ArtifactPreviewPanel.tsx`：更新三处过时 CORS 注释（约 L366-371 / L419 / L475，"Bytes load straight from OSS" 等），确认通用 fetch 失败错误路径不变——代码本无 CORS 专用分支，无逻辑删除
- [x] 4.3 MSW mocks（`handlers.ts`，现返回 `https://oss.test/...`）：presign handler 改返回相对 `/api/v1/artifacts/{id}/download?token=...`，新增 download 路由 mock（须返回带正确 `Content-Type` 的字节体，否则文本预览测试 `res.text()` 拿不到内容）；更新受影响的组件/契约测试断言
- [x] 4.4 跑 `npm test`（vitest）确认 web-artifact-preview / web-artifacts-views 既有契约测试全绿

## 5. 文档同步

- [x] 5.1 `docs/ARCHITECTURE.md`：路由表（L529 附近）加 download 行、presign 行改语义；预览小节（L152 附近，沙箱 iframe / CSP `frame-src` 表述）与**设计系统小节（L148 附近，img-src 含 OSS / 文本预览经 OSS fetch 受 CORS 约束）**改为同源；显式改写「产物字节不经过 API」的决策并注明动机与回退路径
- [x] 5.2 `docs/DEVELOPMENT.md`：说明 `OSS_ENDPOINT` 仅需 API/Worker 可达，浏览器（含局域网访问 `:5173`）不再依赖它；常见问题区补局域网预览说明；部署注意事项补「前置 nginx/Caddy 默认 access log 记完整 request line（含 query token），需关闭/脱敏该路由的 query 记录或接受 5m TTL 残余风险」
- [x] 5.3 `api/README.md` / `web/README.md` 受影响小节同步（presign 语义、`OSS_PRESIGN_TTL` 现为 token TTL）
- [x] 5.4 归档（`/opsx:archive`）时同步改写两个 capability 的 Purpose 段：`artifacts-api`（"The API never proxies object bytes … directly from OSS" → 反向代理语义）与 `web-artifacts-views`（"re-mints a short-lived OSS URL" → "API-signed relative download URL (opaque)"）——delta 不自动改 Purpose，勿漏

## 6. 端到端验证

- [x] 6.1 按 DEVELOPMENT.md 起依赖栈 + API + Worker + web，本机跑通建任务 → 产物预览（HTML 渲染 / 文本 / 图片 / 下载），浏览器 Network 面板确认全部走同源 `/api/v1/.../download`、无 CSP violation
- [x] 6.2 从局域网另一 IP 访问 `http://<lan-ip>:5173`，验证预览与下载全部成功（原始 bug 复现路径）；顺带验证「下载 URL 直接开顶层标签页」时文档处于 opaque origin（响应级 CSP sandbox 生效）
