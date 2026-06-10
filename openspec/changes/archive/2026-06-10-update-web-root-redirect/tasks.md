## 1. 实现

- [x] 1.1 `web/src/router.tsx`：index 路由重定向 `/tasks` → `/tasks/new`
- [x] 1.2 `web/src/routes/router.test.tsx`：同步根路径断言（落在 TaskCreate）

## 2. 验收

- [x] 2.1 `npm run typecheck && npm run lint && npm run test && npm run build` 全绿
- [x] 2.2 `openspec validate update-web-root-redirect` 通过
