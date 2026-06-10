## Context

外壳规格要求三栏在每个认证路由持久渲染；创建页无预览对象。两种实现：路由驱动抑制 vs 进入页面时写 `setPreviewCollapsed(true)`。

## Goals / Non-Goals

**Goals:** `/tasks/new` 全宽 composer，无预览列与 re-open 按钮。

**Non-Goals:** 不改其它路由的三栏行为、不动折叠偏好语义。

## Decisions

- **路由驱动**（`RootLayout` 内 `useLocation`，`/tasks/new` 时不渲染 `<PreviewColumn>`），而非 effect 改写 `previewCollapsed`：后者会污染用户偏好（离开创建页后列保持被动折叠），且 mount/unmount 时序易抖动。路由判断用精确路径匹配（`/tasks/new` 无子路由）。
- 列不渲染时 `ArtifactPreviewPanel` 一并卸载，无副作用（其数据读取按 store 选中态懒发起）。

## Risks / Trade-offs

- [外壳不再严格"每路由三栏"] → 规格同步声明例外；其余路由不变。
