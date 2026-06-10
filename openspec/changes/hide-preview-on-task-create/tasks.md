## 1. 实现

- [x] 1.1 `web/src/routes/root-layout.tsx`：`useLocation` 判断 `/tasks/new` 时不渲染 `<PreviewColumn>`（含 children）
- [x] 1.2 `router.test.tsx`：断言 `/tasks/new` 无 `preview-column` 且无 `preview-open`，`/tasks` 仍有

## 2. 验收

- [x] 2.1 `npm run typecheck && npm run lint && npm run test && npm run build` 全绿
- [x] 2.2 `openspec validate hide-preview-on-task-create` 通过
