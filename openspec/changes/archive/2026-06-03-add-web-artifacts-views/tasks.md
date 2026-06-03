## 1. Artifacts data-access slice (`features/artifacts/`)

- [x] 1.1 `types.ts`: `ArtifactMeta` (`{id, kind, mime: string|null, bytes: number|null, sha256: string|null, created_at}`), `VersionArtifacts` (`{version_id, artifacts: ArtifactMeta[]}`), `PresignResult` (`{url, expires_at, bytes: number|null, mime: string|null, sha256: string|null}`) — nullable fields present-and-nullable, never optional.
- [x] 1.2 `format.ts`: `formatBytes(n: number): string` (decimal KB/MB/GB) with a unit test; document that `bytes` is an integer size, NOT the decimal-string money rule.
- [x] 1.3 `api.ts`: `getVersionArtifacts(versionId, signal?)` → `/api/v1/versions/{id}/artifacts` with **`toastOnError:false`** (`ArtifactList` renders an inline error/empty state — single surface, mirrors `getMyCost`); `getArtifactPresign(artifactId, signal?)` → `/api/v1/artifacts/{id}/presign` with **`toastOnError:false`** (the component owns the error UX).
- [x] 1.4 `queries.ts`: `artifactKeys` + `useVersionArtifactsQuery(versionId)` (`enabled:!!versionId`, skip-retry-on-404, `meta:{silent:true}` — mirror `useTaskCostQuery`); `useArtifactPresignMutation()` (`useMutation`, **`meta:{silent:true}`** to avoid the `mutationCache.onError` double-toast, never cached, re-mints each call).

## 2. UI surface (TaskDetail / VersionTree)

- [x] 2.1 `components/tasks/ArtifactList.tsx`: takes `versionId`, lazy-fetches via `useVersionArtifactsQuery`; renders distinct loading / empty (`[]` → "no artifacts") / error states; one row per artifact with `kind`, `mime` (or `—`), `formatBytes(bytes)` (or `—`), and a Download button. Stable testids: `artifact-list`, `artifact-list-empty`, `artifact-row`, `artifact-download`.
- [x] 2.2 Download handler: `useArtifactPresignMutation`; `onSuccess` navigates the browser to `data.url` via `window.location.assign` (direct OSS, no proxy, no `target=_blank`); `onError` pushes exactly one toast (and/or a per-row hint) — the mutation/transport are both silent so this is the sole error surface; a second click re-mints.
- [x] 2.3 `VersionTree.tsx`: add a per-row expand/collapse toggle (testid `version-expand-toggle`, `aria-expanded`, local `Set<string>` of expanded ids); when open, render `<ArtifactList versionId={node.id} />` indented under the row; preserve existing flatten/badges/current-marker behavior and the `version-tree`/`version-node`/`current-marker` testids; use the restricted Tailwind spacing scale only.

## 3. Test fixtures (MSW)

- [x] 3.1 `test/mocks/handlers.ts`: `artifactFixture(id, overrides?)` helper + default `GET /versions/:id/artifacts` (two artifacts, one with null `mime`/`bytes`/`sha256`) and `GET /artifacts/:id/presign` (fake `url` + `expires_at` + echoed fields). Tests `server.use()` these variants: list empty (`[]`), list `version_not_found` (404), presign `artifact_not_found` (404), presign `internal_error` (500).

## 4. Tests

- [x] 4.1 `features/artifacts/api.test.ts` + `format.test.ts`: list parses in server order with nulls preserved; presign parses; `formatBytes` boundaries — `0`, just-under-1KB, exactly-on-the-boundary (`1000`, `1_000_000`), MB, GB — pinning the unit label + decimal places (e.g. `"1.0 KB"`), plus the `null`-placeholder caller path (`—`).
- [x] 4.2 `features/artifacts/queries.test.tsx` (or fold into component test): 404 on list skips retry + stays silent (no toast); presign mutation re-mints (two `mutate` calls → two requests) and writes nothing to the query cache; a presign error does NOT double-toast (transport silent + `meta.silent`).
- [x] 4.3 `components/tasks/ArtifactList.test.tsx`: loading→list render; empty-list empty state; list 404 → no crash; Download success asserts navigation by spying `window.location.assign` with the returned `url` (single fixed mechanism — no anchor-click variant); presign 404 AND 500 each surface exactly one error and do NOT navigate; null `mime`/`bytes` render `—` placeholders.
- [x] 4.4 `VersionTree` test update: collapsed rows issue NO artifact request; expanding fires exactly one list request for that `version_id`; sibling rows unaffected; existing badges/current-marker assertions still pass.

## 5. Gates & wrap-up

- [x] 5.1 From `web/`: `npm run typecheck` ✓, `npm run lint` ✓ (0 warnings), `npm run test` ✓ (all pass incl. new).
- [x] 5.2 `npx prettier --write` only the files touched this change; confirm `prettier --check` clean on them (do NOT bulk-reformat the repo).
- [x] 5.3 `openspec validate add-web-artifacts-views --strict` (from repo root) ✓.
