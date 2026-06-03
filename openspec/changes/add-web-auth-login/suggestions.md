# Review: `add-web-auth-login`

Summary: The proposal's intent (real login, transport 401 opt-out, store `{token,user}`, redirect-back guard, logout) is sound and well-aligned with existing feature-slice conventions. But several claims about the existing code are wrong or incomplete, and the task list omits edits that the change provably forces (a second `setToken` caller in the WS client, two test files that import the removed symbol/component). As written, the gates in task 6.2 (`typecheck`, `test`) would fail. Findings below.

## Blockers

### B1 — `setToken` has a SECOND non-test caller (`ws.ts`); design D2 and tasks miss it
- Evidence: `web/src/services/ws.ts:294` calls `useAuthStore.getState().setToken(null)` in the `4001` (auth-expired) close path. `design.md:33` claims "its sole non-test caller was `apiFetch`'s 401 handler"; `grep` confirms two production callers: `http.ts:101` and `ws.ts:294`.
- Why it matters: Tasks 1.3 only repoints `handleUnauthorized` (`http.ts`). Removing `setToken` from the store (task 2.1) leaves `ws.ts:294` referencing a non-existent action → `npm run typecheck` (task 6.2) fails, and the realtime auth-expiry logout silently breaks.
- Recommended edit: Add a task under §1 (or §2) to update `ws.ts:294` to call `logout()` instead of `setToken(null)`; correct `design.md` D2's "sole non-test caller" claim to name both `http.ts` and `ws.ts`.

### B2 — Removing `setToken`/`LoginPlaceholder` breaks `router.test.tsx` and `http.test.ts`; tasks only update `store.test.ts`
- Evidence: `web/src/routes/router.test.tsx:15` imports `LoginPlaceholder`, `:65` asserts `getByTestId("placeholder-login")`, and `:69-95` seed auth via `useAuthStore.setState({ token: "test" })` (that still works), but the file imports the deleted component. `web/src/services/http.test.ts:45,73` call `useAuthStore.getState().setToken(...)` which is removed by task 2.1. Tasks list only updates `store.test.ts` (task 2.2).
- Why it matters: `npm run typecheck`/`npm run test` (task 6.2) fail — `router.test.tsx` won't compile (missing import) and `http.test.ts` calls a removed method. The change is not green as specified.
- Recommended edit: Add explicit tasks to (a) update `router.test.tsx` to import/use `LoginPage` and the new testid, and (b) update `http.test.ts:45,73` to use `setSession(...)`/`setState` instead of `setToken`. Note `router.test.tsx:69` etc. use `setState({ token: "test" })` directly — that still compiles, but the `LoginPlaceholder` import + `placeholder-login` testid must change.

### B3 — Task 3.2 passes a raw object as `body`; `apiFetch` never serializes it
- Evidence: Task 3.2 specifies `apiFetch<LoginResponse>("/api/v1/auth/login", { method:"POST", body, ... })` with `body` being the request object. `apiFetch` (`http.ts:120-154`) passes `rest.body` straight into `fetch` and only sets `Content-Type` — it does NOT `JSON.stringify`. Every existing writer stringifies at the call site: `createTask`/`iterateTask`/`controlTask` use `body: JSON.stringify(body)` (`features/tasks/api.ts:59,71,80`).
- Why it matters: As written the request body serializes to `"[object Object]"`; the API returns `400 invalid_input` for every login. Functional bug.
- Recommended edit: Task 3.2 must read `body: JSON.stringify(body)` (or have `login()` stringify), matching the established convention.

## Should-fix

### S1 — React Query retry will retry `invalid_credentials` 401 unless the mutation disables retry
- Evidence: The opted-out 401 rejects with `code:"invalid_credentials"` (delta `specs/web-data-access/spec.md:14-15`). The query retry rule only suppresses `code === "unauthenticated"` and `status === 409` (`query-client.ts:28-34`). Mutations, however, default to `retry:false` (`query-client.ts:37`), and `useLoginMutation` is a mutation — so in practice it is not retried.
- Why it matters: The proposal/design never state *why* a credential 401 is safe from the retry rule; it is only safe because it's a mutation. If the login were ever moved to a query, the rule would retry a credential failure (the `error.code !== "unauthenticated"` branch). This is a latent footgun worth pinning.
- Recommended edit: In `design.md` (D3 or D4) note explicitly that login is a mutation (`retry:false`), so the `unauthenticated`-only retry rule does not apply; optionally add `retry:false` on the mutation defensively. No spec change required.

### S2 — `ApiErrorCode` union does NOT need `invalid_credentials`/`invalid_input` added; review item 6 in the brief is moot
- Evidence: `types/envelope.ts:43-48` defines `ApiErrorCode = "timeout" | "network_error" | "unauthenticated" | "internal_error" | (string & {})` — open-ended; the frozen `web-data-access` "Typed Error Envelope" requirement (`spec.md:78`) mandates this open shape. Server codes are already arbitrary strings.
- Why it matters: No task adds these codes and none is needed; good. But the proposal/design lean on comparing `error.code === "invalid_credentials"` (D5) — that works against the open union with no type change. Flagging so a reviewer/implementer does not "helpfully" narrow the union (which would violate the open-ended requirement).
- Recommended edit: None required; optionally add a one-line note to D5 that no `ApiErrorCode` change is needed (open union).

### S3 — Route Skeleton MODIFIED table still lists stale `*Placeholder` names for already-real routes
- Evidence: The delta table (`specs/web-bootstrap/spec.md:10-13`) keeps `TaskListPlaceholder`, `TaskDetailPlaceholder`, `CostDashboardPlaceholder`. The real router (`web/src/router.tsx:4-7,23-26`) renders `TaskList`, `TaskDetail`, `CostDashboard` (only `/settings`, `/login` pre-change, and `*` are placeholders). The delta's new preamble even says "real rows render their implemented component," yet the table contradicts it.
- Why it matters: This change touches `/login` only, but it is rewriting the entire Route Skeleton requirement (MODIFIED restates the whole block). Carrying forward names that already drifted from reality bakes in a documentation lie under the banner of an accuracy fix. The `tasks`/`costs` archives that shipped those real components apparently never updated this table.
- Recommended edit: While rewriting the table, correct the `/tasks`, `/tasks/:id`, `/cost` rows to `TaskList` / `TaskDetail` / `CostDashboard` (and consider adding the `/tasks/new` → `TaskCreate` row that exists at `router.tsx:24` but is absent from the spec table). If the author wants to keep this change narrowly scoped to `/login`, call out the pre-existing drift explicitly rather than silently re-freezing it.

### S4 — Persisted-store migration hazard for the existing `{token}`-only blob is unaddressed
- Evidence: Old store persists `partialize: (state) => ({ token })` under `auth.token` (`store.ts:25`). The new store persists `{token, user}` under the same key (delta `specs/web-data-access/spec.md:24`). A returning user has `localStorage["auth.token"] = {state:{token:"..."},version:0}` with no `user`.
- Why it matters: After upgrade, `user` rehydrates as `undefined` (not `null`) while `token` is still present → `RequireAuth` admits them (token truthy) but the shell's identity display (task 5.1, requirement "Authenticated Identity Display") reads `user.email` on an absent `user`. Without a guard this throws on first authenticated render for every pre-existing session. The design's "stale token self-heals via 401" only fires on the first network call, after the shell has already tried to render `user.email`.
- Why it matters (cont.): zustand `persist` has no `version`/`migrate` here, so the merge is a shallow spread; `user` stays `undefined`.
- Recommended edit: Either (a) add a `migrate`/`version` bump that maps a legacy `{token}` blob to `{token, user:null}`, or (b) require the shell identity render to tolerate `user == null` (render nothing / a fallback) and add a scenario for "token present but user null". Document the chosen path in design and add a task. At minimum, the "Authenticated Identity Display" requirement should specify behavior when `user` is null.

### S5 — Redirect-back guard under-specifies real open-redirect vectors
- Evidence: `RequireAuth` sets `state:{ from: location.pathname }` (`require-auth.tsx:13`) — always a pathname, never a full URL, and React Router's `location.pathname` excludes scheme/host. The guard (D6 / requirement "Post-Login Redirect", `specs/web-auth/spec.md:37`) accepts "begins with a single `/` and not `//`".
- Why it matters: Two gaps. (1) Because `from` is only ever `location.pathname`, an attacker cannot inject `//evil.example` through the normal redirect path — but `from` comes from arbitrary router *state* and a hand-crafted `<Link state>` or `navigate(..., {state})` could set anything, so the guard is still worthwhile. (2) The guard as written misses backslash tricks (`/\evil.example`, which some browsers treat as protocol-relative) and `/%2F...`. The scenario only tests `//evil.example` and "an absolute URL" (which can't actually arrive via pathname).
- Recommended edit: Tighten the requirement to "begins with `/`, and the second char is neither `/` nor `\`," and add a scenario for the backslash vector. Optionally note in design that `from` is a pathname today, so the guard is defense-in-depth against crafted router state, not the normal flow.

### S6 — No MSW handler exists for `/auth/login`; fixtures pattern is non-trivial (per-case overrides)
- Evidence: `test/mocks/handlers.ts` has no `auth/login` route; tests override per-case via `server.use()` (comments at `handlers.ts:51,93`). Task 6.1 says add success/401/400 handlers but does not specify how the three outcomes are selected within one default handler.
- Why it matters: The default handler must pick an outcome (e.g. by request body email/password) or the per-case tests must `server.use()` overrides. Leaving this implicit risks a default handler that can't exercise the `invalid_credentials` and `invalid_input` scenarios that tasks 4.5 depend on.
- Recommended edit: In task 6.1, specify the default returns 200 for the configured dev credentials and that the 401/400 cases are installed via `server.use()` per test (matching the established override pattern), or that the handler branches on the posted body.

## Nice-to-have

### N1 — TopBar already has a `user-area` slot; logout/identity should reuse it, not invent shell markup
- Evidence: `web/src/components/layout/TopBar.tsx` renders `<div data-testid="user-area">user</div>` on the right — the `web-bootstrap` "Application Shell" requirement (`specs/web-bootstrap/spec.md:22`) calls this the "user-area placeholder on right." Tasks 5.1 say add the control to `root-layout.tsx`.
- Why it matters: `RootLayout` (`root-layout.tsx:7-19`) only composes `<TopBar/>`; it has no slot to inject identity/logout without prop-drilling. The natural home is `TopBar`'s existing `user-area`.
- Recommended edit: Point task 5.1 at `TopBar.tsx`'s `user-area` slot (replace the literal `"user"`) rather than `root-layout.tsx`, and reflect that the change touches `TopBar.tsx` in `proposal.md` Impact.

### N2 — Stale-token-on-login claim (D8) is correct but worth a transport test note
- Evidence: D8 (`design.md:53-54`) says a stale Bearer on `/auth/login` is harmless because the route is on the API allowlist. `api-auth` confirms `POST /api/v1/auth/login` is public (`api-auth/spec.md:38,49`). `apiFetch` will still attach `Authorization` if a token is in the store (`http.ts:126-129`).
- Why it matters: Correct, no action needed for correctness. But if a returning user has an expired token and submits login, the request carries a stale Bearer that the public route ignores — fine. No test asserts this; low value to add.
- Recommended edit: Optional — none required.

### N3 — Prettier gate is per-touched-file (`prettier --write`), but repo `format` script globs `src/**`
- Evidence: Task 6.2 says `npx prettier --write` only on touched files; `package.json` `format` is `prettier --write "src/**/*.{ts,tsx,css,md}"`. Lint is `eslint .` with the brief's "0 warnings" expectation.
- Why it matters: Running the repo `format` script could reformat untouched files and violate AGENTS §6 ("no unrelated changes"). The task's per-file approach is correct; just ensure the implementer does not run the repo `format` script.
- Recommended edit: None; task 6.2 already scopes prettier to touched files. Flag is informational.

## Verdict
- Blockers: 3 (B1 ws.ts setToken caller; B2 router/http tests not updated; B3 body not serialized)
- Should-fix: 6 (S1 retry rationale; S2 no union change needed; S3 stale placeholder names in route table; S4 persist migration / null-user render; S5 redirect guard backslash vector; S6 MSW login handler shape)
- Nice-to-have: 3 (N1 reuse TopBar user-area; N2 stale-Bearer note; N3 prettier scope)

The three blockers are concrete and will fail the change's own gates (typecheck/test) or break login at runtime; they must be fixed before `apply`. S3 and S4 are the most consequential of the should-fixes (a re-frozen documentation lie, and a crash for every pre-existing session).

---

## Independent verification (by Claude, against the cited lines)

Every finding was re-checked against the actual source before applying. Verdicts:

| ID | Verdict | Evidence confirmed |
|----|---------|--------------------|
| B1 | **VALID — apply** | `ws.ts:294` calls `setToken(null)` in the 4001 path; design D2's "sole non-test caller" is wrong. |
| B2 | **VALID — apply** | `http.test.ts:45,73` use `setToken`; `router.test.tsx:15,30` import/use `LoginPlaceholder`. |
| B3 | **VALID — apply** | `apiFetch` (http.ts:131-133) only sets Content-Type, never stringifies; `tasks/api.ts:60,72,81` stringify at call site. |
| S1 | **VALID — apply** | Mutations default `retry:false` (query-client); safe only because login is a mutation. Add note + defensive `retry:false`. |
| S2 | **VALID (no code change) — note added** | `ApiErrorCode` is open (`envelope.ts`); `code === "invalid_credentials"` compares fine. Added a one-line note so nobody narrows the union. |
| S3 | **VALID — apply** | `router.tsx` renders real `TaskList`/`TaskDetail`/`CostDashboard` + `/tasks/new`→`TaskCreate`; table corrected to reality. |
| S4 | **VALID — apply** | No `version`/`migrate` in `store.ts`; legacy `{token}` blob rehydrates `user:undefined`. Bump `version:1` + migrate dropping legacy session (old tokens are pre-JWT garbage anyway) AND tolerate `user==null` in the shell. |
| S5 | **VALID — apply** | `from` is `location.pathname` (defense-in-depth vs crafted state). Guard tightened to reject 2nd char `/` or `\`; backslash scenario added. |
| S6 | **VALID — apply** | No `/auth/login` handler in `handlers.ts`; repo uses per-case `server.use()`. Task 6.1 now specifies that. |
| N1 | **VALID — apply** | `TopBar.tsx` has the `user-area` slot (literal `"user"`); identity/logout retargeted there, not `root-layout.tsx`. |
| N2 | Correct, no action | Login route is public per `api-auth`; stale Bearer ignored. |
| N3 | Correct, no action | Task 6.2 already scopes prettier to touched files. |

No findings downgraded — all 10 substantive items applied. Artifacts updated: `proposal.md`, `design.md`, `tasks.md`, `specs/web-auth/spec.md`, `specs/web-data-access/spec.md`, `specs/web-bootstrap/spec.md`.
