## Why

`/tasks/new` 已是聊天式创建入口且为认证后默认落点，但右侧 Artifact Preview 列仍占据约一半宽度，只显示 "Select a version to preview" 占位——创建页没有任何可预览对象，空列分散焦点、压缩 composer。

## What Changes

- Task Create 路由（`/tasks/new`）**不渲染**右侧预览列（含折叠态的 re-open 按钮）。
- 抑制为路由驱动（`RootLayout` 判断路由），**不改写** store 的 `previewCollapsed` 用户偏好；离开创建页即按原折叠标志恢复。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `web-bootstrap`: "Application Shell" 增加 Task Create 路由的预览列抑制例外与对应 scenario。

## Impact

- `web/src/routes/root-layout.tsx`（路由判断）；`router.test.tsx` 断言补充。`PreviewColumn` / `ArtifactPreviewPanel` / store 不变。
