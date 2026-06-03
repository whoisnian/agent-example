## Context

The `artifacts-api` capability is archived and green: `GET /versions/{version_id}/artifacts` returns owner-scoped per-version metadata (`{id, kind, mime, bytes, sha256, created_at}`, ordered `created_at ASC, id ASC`, never leaking `oss_key`), and `GET /artifacts/{artifact_id}/presign` mints a short-lived single-object S3 presigned GET URL plus `{url, expires_at, bytes, mime, sha256}`. The web client today consumes neither — `VersionTree` is a pure presentational component rendering status/cost per node, with no artifact affordance.

This change mirrors the just-shipped `add-web-cost-views` structure: a small `features/<x>/` data-access slice + a thin UI surface, with MSW fixtures and full web gates. The product placement decision (per-version expandable rows in `VersionTree`, vs. a single current-version panel) was put to the user, who chose **per-version expandable** — the faithful match to the strictly per-version API.

Constraints carried in from prior rounds: React Query for server state (`web-data-access`); the two-layer toast model (`http.ts` transport `toastOnError` + `query-client.ts` cache `meta.silent`); owner-scoped 404 = not-found, never 403; decimal-string discipline for money only; restricted Tailwind spacing scale; format files you touch but never bulk-reformat.

## Goals / Non-Goals

**Goals:**
- A reusable `features/artifacts/` slice: typed list read (lazy, per `version_id`) + an on-demand presign action that never caches its result.
- `VersionTree` rows gain a lazy per-version disclosure listing artifacts with a direct-to-OSS Download.
- Distinct loading / empty (`[]`) / error states per expanded version; presign failures surface, never swallowed.
- MSW fixtures + unit/integration tests; all web gates green; touched files prettier-clean.

**Non-Goals:**
- File **upload** (`/uploads/sts` is `[Deferred]`; ARCHITECTURE §A, `add-artifacts-api` D5).
- In-app preview/rendering/hashing of artifact contents — we list metadata and hand off the URL.
- A task-level artifact aggregate or a standalone `/artifacts` route — there is no such endpoint, and the surface is intentionally embedded in TaskDetail.
- Charting or any new dependency.
- Reacting to `expires_at` (no countdown / pre-expiry refresh): a click always re-mints, so a stale URL never matters in practice.

## Decisions

### D1 — Slice shape: list as a React Query read, presign as a non-cached action
`features/artifacts/{types,api,queries,format}.ts`, mirroring `features/costs/`.
- `api.ts`: `getVersionArtifacts(versionId, signal)` → `/versions/{id}/artifacts` (default transport toast — the list owns no in-page error region, so a transport toast is the fallback like other reads); `getArtifactPresign(artifactId, signal)` → `/artifacts/{id}/presign` with **`toastOnError:false`** (the calling component owns the error UX — see D4).
- `queries.ts`: `useVersionArtifactsQuery(versionId)` with `enabled: !!versionId`, `retry: (n,e) => !(e instanceof ApiError && e.status===404) && n<2`, `meta:{silent:true}` — a verbatim copy of the `useTaskCostQuery` posture (404 is a render state, not a retry/toast). A non-404 error (incl. the practically-unreachable `400` from a malformed id — `versionId` is always a server-sourced `VersionNode.id`) follows the same posture as `useTaskCostQuery`: it may retry up to 2× and renders the component's error state; no separate 400 handling is specified (consistent with the just-archived `web-cost-views`, which likewise does not special-case the `/tasks/{id}/cost` 400).
- Presign is modeled as `useArtifactPresignMutation()` (React Query `useMutation`) with **`meta:{silent:true}`**, exactly mirroring the three existing mutations (`features/tasks/mutations.ts`: create/iterate/control are all `meta:{silent:true}` paired with a `toastOnError:false` transport call). **Why a mutation for a GET:** the result is a short-lived side-effecting credential, not cacheable server state; `useMutation` gives per-click `isPending`/`onError` without polluting the query cache, and naturally re-mints every call. **Why silent + the component owns the error:** the global `mutationCache.onError` (`query-client.ts`) toasts every non-silent mutation; combining a transport toast (`toastOnError:true`) with a non-silent mutation double-toasts the same failure. So presign is silent at BOTH layers (`toastOnError:false` + `meta:{silent:true}`) and the Download `onError` pushes the single user-facing toast (and/or a per-row hint) — see D4. Alternative — a plain imperative `await getArtifactPresign()` in the click handler — also works but reinvents pending/error tracking; the mutation is the smaller, more idiomatic surface.

**Alternative considered:** caching presign by artifact id with a short `staleTime`. Rejected — `expires_at` is advisory and the TTL is server-controlled; a cached-but-expired URL yields an OSS 403 the user can't see coming. Re-minting on click is strictly simpler and always correct.

### D2 — `bytes` is a number, not a decimal string
The decimal-string rule exists to avoid float rounding of **money** (`amount_usd`, NUMERIC(18,8)). `bytes` is an integer file size (int8); file sizes stay well within `Number.MAX_SAFE_INTEGER`. So `format.ts` gets `formatBytes(n: number): string` (B/KB/MB/GB, binary or decimal — pick decimal `1000` for simplicity, label `KB`/`MB`), and `bytes: number | null` is converted directly. This is explicitly called out in the spec so a reviewer doesn't flag it as a decimal-discipline violation. `formatBytes(null)` is not defined — callers branch on `null` and render a neutral placeholder (e.g. `—`).

### D3 — `VersionTree` disclosure without disturbing tree logic
`VersionTree` keeps its parent-linked `flatten()` and row rendering. Each row gains a toggle button (`aria-expanded`) tracking open state in a local `Set<string>` of expanded `version_id`s (component-local `useState`, not Zustand — ephemeral UI state, and `web-data-access` forbids server entities in Zustand; this is neither). When a row is open, render `<ArtifactList versionId={node.id} />` (new `components/tasks/ArtifactList.tsx`) beneath it. `ArtifactList` owns the lazy query + the three states + the Download buttons. This keeps `VersionTree` a thin layout owner and isolates artifact concerns in one testable component. Indentation reuses the existing `depth * 16px` padding convention.

**Alternative considered:** lifting expansion state to TaskDetail or a store. Rejected — expansion is local view state with no cross-component consumer; keeping it in `VersionTree` avoids prop-drilling and a needless store.

Note: this surface does **not** reuse `VersionNode.artifact_root` (`features/tasks/types.ts`) — that field is the OSS prefix and is unrelated to the artifact list. Artifact metadata comes solely from the new `GET /versions/{id}/artifacts` read; `oss_key`/prefix never reach the client.

### D4 — Download = mint then navigate, directly to OSS
On Download click: `presign.mutate(artifactId)`; `onSuccess(({url}) => { window.location.assign(url) })`. The cross-origin `download` attribute is ignored by browsers, so we rely on OSS `Content-Disposition` for the filename — acceptable for MVP (the API/worker controls the object's disposition; out of scope here). We do **not** `target=_blank`-spawn a tab for every file (popup-blocker risk); a same-tab navigation to a presigned GET triggers the download/preview and returns the user via back. Error UX: presign is silent at both toast layers (`toastOnError:false` + `meta:{silent:true}`, per D1) and the Download `onError` is the single error surface — it pushes one error toast and may set a per-row hint. This keeps the established "mutation is silent, the component owns UX" convention (`features/tasks/mutations.ts`) rather than relying on the transport or `mutationCache` toast, avoiding the double-fire those would cause.

### D5 — MSW fixtures
Add to `handlers.ts`: `GET /versions/:id/artifacts` (default: two artifacts via a new `artifactFixture(id, overrides)` helper, one with null `mime`/`bytes`/`sha256` to exercise the placeholder path) and `GET /artifacts/:id/presign` (default `{url:"https://oss.test/...", expires_at, bytes, mime, sha256}`). Tests `server.use()` the empty-list, `version_not_found` 404, and presign-`artifact_not_found`/500 variants. Navigation in jsdom: stub/assert `window.location.assign` (or the created anchor's `click`) rather than perform a real navigation.

## Risks / Trade-offs

- **[Cross-origin `download` filename]** The browser ignores the `download` attr on a cross-origin URL, so the saved filename comes from OSS `Content-Disposition`, not the app → Mitigation: out of scope here; the object's disposition is set upstream. We still label each row with `kind`/`mime`/size so the user knows what they're fetching.
- **[Presigned URL leakage in logs/history]** The URL is a bearer credential to one object for a few minutes; same-tab `window.location.assign` also writes it into browser history/address bar → Mitigation: never logged by the app; never persisted/cached; single-object scoped + short TTL (the backend's existing guarantee) bounds the exposure of a history entry. No `target=_blank` (also avoids popup blocking).
- **[Toast double-fire on presign error]** A mutation that both lets the transport toast (`toastOnError:true`) and stays non-silent gets toasted twice (transport + `mutationCache.onError` in `query-client.ts`) → Mitigation (D1/D4): presign is silent at BOTH layers (`toastOnError:false` + `meta:{silent:true}`) and the Download `onError` is the single error surface, matching the three existing mutations. This is the most likely implementation trap, so it is pinned in the spec ("standard error path") and tasks.
- **[Expanding many versions = many requests]** Each expansion fires one list query → Mitigation: lazy (only on expand) + per-version cache keying (each version cached independently). A re-expand within `staleTime` (30s) hits cache; a re-expand after `staleTime` refetches per React Query defaults — i.e. de-duped only within the stale window, not "fetched once forever". Acceptable for MVP tree sizes.
- **[jsdom navigation]** A real `window.location.assign` would error/navigate the test runner → Mitigation: assert the call (spy) instead of executing it; this is the unit-level contract anyway ("navigate to the returned url").
- **[`bytes` as number]** Theoretical precision loss above 2^53 bytes (~9 PB) → Mitigation: not a real artifact size in MVP; money stays string, file size does not.

## Open Questions

- None blocking. Filename/disposition polish and any artifact preview are explicitly deferred (Non-Goals).
