## ADDED Requirements

### Requirement: Worker Artifact Write Authority Includes Scoped Deletion

The Worker persistence layer is the only code path issuing PostgreSQL writes from the Worker, and the set of permitted write targets is hard-coded (`ALLOWED_WRITE_TABLES`). For the `artifacts` table the Worker SHALL be permitted to perform, in addition to INSERT/upsert on `(version_id, oss_key)`, a **scoped DELETE** of a single row identified by `(version_id, path)` where `version_id` is the **current run's** version. The deletion MUST be exposed as a dedicated persistence method (analogous to the existing artifact insert) and MUST NOT be issued as ad-hoc SQL from agent code. It MUST be idempotent (deleting a non-existent `(version_id, path)` row affects zero rows and MUST NOT raise) and MUST report whether a row was removed so the caller can decide whether to emit a retraction event.

The Worker MUST NOT delete rows of any version other than the run's own, and MUST NOT gain delete authority over any other table; `tasks` and `task_versions` remain owned by the API service and `cost_events`/`task_costs` by the Cost Service. Any further change to `ALLOWED_WRITE_TABLES` or to a table's permitted operations still requires a paired OpenSpec change to this capability.

#### Scenario: Worker deletes its own version's artifact row

- **WHEN** the Worker invokes the scoped artifact-delete method with the running version's `version_id` and a `path` that has a row
- **THEN** exactly that `(version_id, path)` row MUST be removed and the method MUST report that a row was deleted

#### Scenario: Scoped artifact delete is idempotent

- **WHEN** the Worker invokes the scoped artifact-delete method for a `(version_id, path)` with no matching row
- **THEN** zero rows MUST be affected, the method MUST NOT raise, and it MUST report that no row was deleted

#### Scenario: Worker cannot delete outside its allowlist

- **WHEN** any Worker code path attempts a DELETE against a table other than `artifacts`, or an `artifacts` DELETE not scoped to a single `(version_id, path)`
- **THEN** the persistence layer MUST reject it (the allowlist/method surface offers no such operation), preserving the API-owned ownership of `tasks`/`task_versions`
