# add-artifact-download-proxy

## Why

产物预览/下载目前依赖浏览器直连 OSS 的预签名 URL，URL 的 host 来自 `OSS_ENDPOINT`（开发环境为 `http://localhost:9000`）。一旦浏览器与 API/OSS 不在同一台机器（局域网联调、未来部署 OSS 不对公网暴露的拓扑），预签名 URL 对浏览器不可达，预览全部失败。把产物字节经 API 反向代理后，浏览器只需要能访问 API 同源地址，OSS 永远只被 API 与 Worker 在服务端访问。

这是对既有决策（`add-artifacts-api` design D1/D2：「产物字节不经过 API 进程」）的**显式偏离**：当时优化的是 API 进程带宽，现在 MVP 实际暴露的问题是网络拓扑耦合 —— 浏览器可达性不应是 OSS 部署的约束。MVP 产物体量小（文本/HTML/图片为主），代理带宽成本可接受。

## What Changes

- **新增** `GET /api/v1/artifacts/{artifact_id}/download?token=...`：API 校验短时签名 token 后从 OSS 拉取对象并流式回写浏览器（带 `Content-Type` / `Content-Length` / 响应级 `Content-Security-Policy: sandbox allow-scripts` / `X-Content-Type-Options: nosniff`）。
- **修改** `GET /api/v1/artifacts/{artifact_id}/presign`：响应结构 `{url, expires_at, bytes, mime, sha256}` 不变，但 `url` 从「OSS 绝对地址的预签名 URL」改为「API 相对路径的签名下载 URL」（`/api/v1/artifacts/{id}/download?token=...`）。**BREAKING**（对直接消费 `url` 指向 OSS 这一语义的调用方；前端将 `url` 视为不透明字符串，不受影响）。
- token 由 API 用现有 `AUTH_JWT_SECRET` 签发（`aud="artifact-download"`、`sub=artifact_id`），TTL 复用 `OSS_PRESIGN_TTL`；`<img>` / `<iframe>` / 文本 `fetch` 均无法携带 Authorization 头，token-in-query 是必需形态。access token 校验路径同步**显式拒绝带非空 `aud` 的 token**（现状拒绝下载 token 仅靠缺 `tid` 的副作用，必须改为设计防护）。
- 前端 CSP 全面收紧：`img-src` / `frame-src` 不再放行 OSS 来源（删掉 `https:` 与 `http://localhost:9000`），回到 `'self'`；`connect-src` 删 `http: https:` 通配（其放行理由——OSS 跨源文本 fetch——随本变更消失）。spec 层删除文本预览的 CORS 降级要求（代码本无专用分支，仅注释过时）。
- 同步更新 `docs/ARCHITECTURE.md`（路由表、预览小节、设计系统小节的 CSP/CORS 表述、D1/D2 偏离说明）与 `docs/DEVELOPMENT.md`（`OSS_ENDPOINT` 无需浏览器可达；前置反向代理的 token-in-query 日志注意事项）。
- 不改 Worker；不改 `web-artifacts-views` 数据访问层代码与 requirement（`url` 不透明），但其 Purpose 段的 "OSS URL" 措辞与 `artifacts-api` Purpose 的 "never proxies object bytes" 须在归档时同步改写。
- **部署绑定**：API 与 web 改动同一次发布（现行 `frame-src` 不含 `'self'`，分步发布会在 http 环境产生 HTML 预览破损窗口，见 design Migration Plan）。

## Capabilities

### New Capabilities

（无 —— 下载代理归入既有 `artifacts-api` 能力面）

### Modified Capabilities

- `artifacts-api`：presign 端点的 `url` 语义改为 API 相对签名下载 URL；新增 download 反向代理路由的需求（token 校验、流式转发、响应头、错误码、日志/指标约束）。
- `web-artifact-preview`：预览加载源从「跨源 OSS（CSP 放行 + CORS 门槛）」改为「同源 API 路由」；CSP 收紧为 `'self'`，文本预览的 CORS 降级要求删除。

## Impact

- `api/`：新增 download 路由（handler 薄、取流逻辑下沉 domain/application）；`internal/auth` 增加下载 token Issue/Verify 并给 access Verify 加 aud 拒绝；`oss.Client` 字段从 PresignClient 换为 `*s3.Client` 并新增 `GetObject` 流式读取；domain `ArtifactPresigner` 接口换形（`artifact_id` 入参，本地签 token）；`MapError` 增加 403/502 哨兵直映射；metrics 增加下载计数/字节数；既有 MinIO 集成测试改写为「presign → GET download 路由」回环。
- `web/`：`index.html` CSP 收紧（img-src / frame-src / connect-src）；`ArtifactPreviewPanel` 更新过时 CORS 注释（无专用分支可删）；MSW mock handlers 同步相对 URL + download 路由。
- `docs/`：ARCHITECTURE.md / DEVELOPMENT.md 同步。
- 安全面：HTML 产物改为同源伺服，必须靠响应级 `CSP: sandbox` 阻断「直接在顶层标签页打开下载 URL 导致存储型 XSS 跑在 API 源上」的新风险（design 详述）。
