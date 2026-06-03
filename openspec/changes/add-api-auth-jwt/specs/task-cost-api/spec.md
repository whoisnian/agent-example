## MODIFIED Requirements

### Requirement: Owner-Scoped Reads Hide Unowned Resources

Cost-detail endpoints MUST treat "unknown id" and "unowned id" identically — both return HTTP `404` with the unified envelope (`code = "task_not_found"` for `/tasks/{id}/cost`, `code = "version_not_found"` for `/versions/{id}/cost`). No `403 forbidden`, no list-leaking via differential response, no different error code. This MUST mirror `task-read-api` §"Owner-Scoped Reads Hide Unowned Resources".

The owner identity is the authenticated `Principal{tenant_id, user_id}` resolved from the request context, which the Bearer-token middleware populates from the validated JWT claims (see `api-auth` §"Bearer Token Authentication" / §"Authenticated Principal Drives Ownership"). A row counts as "owned" iff `tasks.tenant_id = $tenant AND tasks.user_id = $user`; for versions, ownership is resolved through `task_versions.task_id → tasks`. The scoping behavior is unchanged — only the source of `(tenant_id, user_id)` moves from the dev-mode env constant to the per-request token principal.

#### Scenario: Same tenant, different user, returns 404
- **GIVEN** a task owned by `(tenant_id=T, user_id=U1)`
- **WHEN** caller `(T, U2)` `GET /api/v1/tasks/{id}/cost`
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"` (NOT `403`, even though the tenant matches)

#### Scenario: Different tenant returns 404
- **GIVEN** a task owned by `(tenant_id=T1, user_id=U)`
- **WHEN** caller `(T2, U)` `GET /api/v1/versions/{id}/cost` for one of that task's versions
- **THEN** the response MUST be HTTP `404` with `code = "version_not_found"`
