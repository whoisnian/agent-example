## 1. 数据模型：artifacts.path

- [ ] 1.1 迁移 0010：`ALTER TABLE artifacts ADD COLUMN path TEXT`，回填（按 `task_versions → tasks` join 剥 `{tenant_id}/{task_id}/{version_id}/` 前缀，不匹配或剥出空串均留 NULL），加部分唯一索引 `(version_id, path) WHERE path IS NOT NULL`，down 为 drop index + drop column；验证 up→down→up 干净往返、空串回填留 NULL、多 NULL 不冲突唯一索引
- [ ] 1.2 更新 sqlc：`ListArtifactsByVersion` 返回 `path`；新增 `GetArtifactByVersionAndPath(version_id, path)`（preview 路由用）；重新生成

## 2. Worker：step 级产物持久化 + artifact 事件

- [ ] 2.1 `persistence.insert_artifact` 增加 `path` 参数（upsert SET 列同步更新）；`ProducedArtifact` 已携带 path，贯通传递
- [ ] 2.2 `run_agent_loop` 注入 `persist_artifacts` 回调，step 收尾顺序固定为：**upsert 产物行 → 预留 step+artifact 全部 seq（`ctx.next_event_seq()`）→ 写 checkpoint → 发 step 事件 + 逐个 `kind="artifact"` 事件**（payload `{artifact_id, path, mime, bytes, sha256}`，insert-then-publish）。即产物落库与 seq 预留必须在 checkpoint 之前（崩溃窗口下重跑收敛、seq 被高水位覆盖）。移除 `AgentBase.run()` 末尾批量 insert；同步处理 `LoopResult.artifacts`（删除该字段或注明保留理由，避免死契约）
- [ ] 2.3 `inherit.py`：继承行写 `path`（剥版本前缀的相对 key），落库后逐个发 `kind="artifact"` 事件
- [ ] 2.4 单测：step 级落库时机（step 1 完成即可见）、**顺序断言（upsert+seq 预留先于 checkpoint，insert 先于 publish）**、crash-before-checkpoint resume 重跑不产生孤儿行/不复用已持久化 seq、失败仍 fail run、继承事件发射

## 3. API：DTO path + zip 归档 + 目录化预览

- [ ] 3.1 列表 DTO 增加 `path`（present-and-null）；契约测试覆盖 null 序列化
- [ ] 3.2 通用化下载 token：mint/verify 支持 `aud ∈ {artifact-download, artifact-archive, version-preview}`，`sub` 校验对应资源 id；access-token 双向隔离回归测试
- [ ] 3.3 `GET /versions/{id}/artifacts/archive/presign`（Bearer + owner 校验）+ `GET /versions/{id}/artifacts/archive?token=`（公开路由，`archive/zip` 流式打包，entry 名 = path / 回退 `artifact-<id>`，UTF-8 entry，attachment 头，空版本空 zip，OSS 失败 502/断流 abort+metric，token 不进日志）
- [ ] 3.4 `GET /versions/{id}/preview`（mint，返回 `{base_url, expires_at}`）+ `GET /versions/{id}/preview/{token}/{filepath...}`（公开路由：token 校验、filepath 解码+净化拒绝 `..`/`\`/绝对路径/空或 `.`、按 `(version_id, path)` 精确解析、下载代理同款安全头、404/403/502 口径；token 在路径段——必须以脱敏 route template 记日志，不复用默认 path 记录）
- [ ] 3.5 路由注册 + 归档/预览 metrics（outcome 标签 + bytes counter）+ HTTP 契约测试（zip 内容、相对引用解析、穿越/空路径拒绝、跨 version/aud token 403、**archive 与 preview 两路由访问日志均不含 token**）

## 3b. API：版本详情 summary（供历史回合折叠行）

- [ ] 3b.1 迁移已存在 `task_versions.summary`（0009）；`GetVersionDetail`/相关 sqlc 查询 SELECT `summary`；`VersionFull` 增 `Summary *string`（present-and-null JSON）；契约测试覆盖有值/NULL 两态
- [ ] 3b.2 web `VersionFull`/`types.ts` 增 `summary: string | null`；MSW handler 返回 summary；现有 version-detail 测试不回归

## 4. Web：数据访问与实时失效

- [ ] 4.1 `ArtifactMeta` 增加 `path: string | null`；`features/artifacts/` 新增 archive presign 与 preview mint 两个 mutation（双层静默、不缓存）；MSW handler + 单测
- [ ] 4.2 `use-task-live.ts`：`version:` 帧 `kind === "artifact" | "status"` 时失效该版本 artifacts 查询；单测覆盖两种 kind 与既有失效不回归

## 5. Web：回合布局 + 聚合卡片 + 对话连续性

- [ ] 5.1 `ConversationTurn` 重排为 prompt → result line → 执行区 → 产物 → 回滚 footer；产物改单张聚合卡（icon + "N file(s)" + 总大小 + path 摘要 + Download zip），激活写 `selectArtifact(versionId, firstId)`，Download zip 走 archive presign + navigate；0 产物省略、读失败静默、null path 回退 kind
- [ ] 5.2 历史回合执行区：折叠行（有 `summary`（来自版本详情 DTO，复用 `TurnPrompt` 已发起的 `useVersionQuery`）显示 summary，否则 "Execution log"）、展开才 `enabled` 事件查询、>1 页时显示首页 + 截断提示（不做 load-more）、当前回合保持展开+实时；`TaskDetail` 移除"仅 current 渲染 EventLog"特判
- [ ] 5.3 组件测试：迭代后 v1 执行区仍可展开、懒加载只在展开时发请求、聚合卡行为（激活/下载/错误单 toast）

## 6. Web：按 kind 的对话式事件渲染

- [ ] 6.1 `EventLog` 拆 per-kind 渲染器：`summary` 正文段落、`plan` 有序清单、`step` verdict 进度行、`artifact` 文件行（点击联动预览选中，按 `artifact_id` 去重）、`status` 弱化行、`log` 弱化等宽、`error` 保留、`title`/其它非对话 kind 不渲染、未知 kind 紧凑 JSON 兜底；payload 缺字段降级不抛错
- [ ] 6.2 组件测试：各 kind 渲染断言、隐藏 kind 不出行、同 `artifact_id` 重发只一行、malformed payload 走兜底

## 7. Web：目录化 HTML 预览 + path 显示

- [ ] 7.1 `ArtifactPreviewPanel`：HTML 渲染视图改用 preview mint（iframe src = `base_url + "/" + encodePath(path)`，path 为 null 回退单文件 URL）；Refresh 重 mint；mint 失败 inline error
- [ ] 7.2 面板行与 toolbar 标题改用 `path` 优先（null 回退 kind）；测试：目录预览 src 组装、null path 回退、path 标签显示
- [ ] 7.3 手动验证（/verify）：生成含相对 css/js 的 html 产物，确认 iframe 内样式脚本正常加载、zip 下载内容完整、运行中产物实时出现、迭代后历史回合可回看

## 8. 文档同步

- [ ] 8.1 `docs/ARCHITECTURE.md`：§4.2 worker 职责（step 级产物 + artifact 事件）、§5.2/§5.3 事件 kind 清单与 payload、API 面新增 archive/preview 端点
