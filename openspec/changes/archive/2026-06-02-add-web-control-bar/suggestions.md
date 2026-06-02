# Review findings — `add-web-control-bar`

Reviewed the four proposal artifacts against the backend contract (`openspec/specs/task-control-api/spec.md`), the existing web code, the `web-tasks-pages` capability spec, and AGENTS.md §4.3 / §5. Findings are grouped by artifact, each tagged blocker / should-fix / nice-to-have, with file + location and a concrete fix.

Overall the proposal is well-scoped, contract-accurate on the big shapes (202 `{accepted, action, task_id, effective}`, `effective ∈ {queued, best_effort}`, 409 `invalid_state` with status-naming message, 404 not-special-cased), and faithfully mirrors the iterate idiom. The findings below are mostly precision gaps, a couple of real consistency defects, and missing test/scenario coverage — no architecture-level blockers.

---

## proposal.md

1. **should-fix — `meta.silent` vs `toastOnError:false` are conflated.**
   Line 12 says the mutation is "silent" and lines 10–11 say it "opts out of the global error toast." Those are two *different* mechanisms in this codebase and the proposal blurs them: `toastOnError:false` (an `apiFetch` option, set in `api.ts`) suppresses the transport-layer toast; `meta:{silent:true}` (read by `query-client.ts` `MutationCache.onError`, lines 5–18) suppresses the React-Query-layer toast. The existing iterate path sets *both* (`api.ts:68-70` + `mutations.ts:42`), and the control path must too or a fallback error toast can fire from the mutation cache. The proposal should state both explicitly so the implementer doesn't drop one.
   Fix: in "Data layer" (line 12) say "`controlTask` sets `toastOnError:false` AND `useControlTaskMutation` sets `meta:{silent:true}` — both, matching iterate."

2. **nice-to-have — "info note" toast level for best_effort is asserted but `info` is only weakly justified.**
   Line 10 / line 38 (design) call for an `info` toast. `ToastLevel` (store.ts:3) does include `"info"`, so this is fine — just confirm the level is `info` (not `warning`) consistently across proposal/design/tasks; currently consistent. No change required; flagging only because the spec scenario (line 36–39) does not name a level (see spec finding 3).

3. **nice-to-have — Impact section omits the pure helper from "New code."**
   Line 29 lists the component + `{api,mutations,types}.ts` additions but tasks.md 1.4 adds a `controlAvailability` helper to `types.ts`; the proposal's New-code list should mention it (and its unit test) so reviewers see the full surface. Minor.

---

## design.md

4. **should-fix — D2's cancel precondition will *enable* Cancel for a `cancelling` task, which contradicts the documented task-status reachability.**
   D2 (line 29) encodes `cancel ∉ {cancelled, succeeded, failed}`. That is verbatim-correct against the API precondition (`task-control-api` spec lines 46). BUT `types.ts:32-46` documents that `cancelling` is *version-only and never appears on `task.status`* (event-ingest skips it). So for a task, `cancel`-enabled = `status ∈ {pending, running, paused}`. The helper as written (`∉ terminal`) is harmless for reachable statuses, but if a `cancelling` value ever leaks onto `task.status` the bar would offer Cancel — and the API would still accept it (not terminal), producing a duplicate cancel. Recommend the helper be defined positively over the *reachable* task set and the design note this, to match the `isActiveStatus`/`TASK_STATUSES` reasoning already in the codebase. At minimum, D2 should cite `types.ts`'s "version-only statuses" note so the implementer doesn't widen the union.

5. **should-fix — D2 says "unit-tested against every `TaskStatus`" but `cancelling`/`queued` aren't in `TaskStatus`.**
   D2 (line 29) and tasks 4.2 say the helper is tested "across … each terminal status" / "every `TaskStatus`." `TaskStatus` (types.ts:30) is exactly the six `TASK_STATUSES`; `queued`/`cancelling` are *not* members. The enablement matrix test should iterate `TASK_STATUSES` (the six) — good — but the design's phrase "every `TaskStatus`" should be tied to that constant so the test author doesn't also hand-roll `queued`/`cancelling` cases that can't occur on a task. Tighten the wording.

6. **should-fix — D4 leaves 404 handling under-specified and slightly inconsistent with the existing page.**
   D4 (line 37) says 404 is "already handled by the page's existing not-found render once the task query refetches." That is only partially true: the not-found render (`TaskDetail.tsx:47-54`) is driven by **`taskQuery.error`**, not by the control mutation. A 404 from the *control* POST does not populate `taskQuery.error`; it lands in the mutation's `onError`. The `onSettled` invalidation will refetch the task query, and *that* refetch returning 404 is what flips the page — so the chain works, but only because the task still 404s on read. The design should state that the control mutation's own 404 is otherwise swallowed/generically toasted, and that the not-found render depends on the subsequent task refetch also 404ing. As written a reader could think the mutation directly triggers the not-found view.

7. **nice-to-have — D5 mentions a `danger` variant; confirmed available.**
   Looked at `Button.tsx:4-10`: `danger` variant exists. No issue.

8. **nice-to-have — Open Question about toast copy is fine to defer** (line 61); non-contractual. No issue.

---

## specs/web-tasks-pages/spec.md (the ADDED delta)

9. **should-fix — Missing scenario: 400 invalid_input / generic error path.**
   The requirement body and scenarios cover 202/409/best_effort/in-flight, but design D4 (line 39) explicitly defines a `400 invalid_input` → generic error-toast fallthrough, and the proposal Impact (line 30) lists no 400 fixture. The backend contract has a whole `Request Validation` requirement (400). Even though the client "only ever sends a valid action," a scenario asserting "an unexpected `ApiError` (non-409) surfaces a generic error toast and is not retried" makes the inline-error contract testable and matches D4. Add it.

10. **should-fix — No scenario asserts the disabled-button *reason text* / `title`, though the requirement MUSTs it.**
    The requirement (line 11) says a disabled button "MUST carry a human-readable reason (e.g. via `title`)." No scenario verifies it. Scenario "Action enablement follows task status" (line 17-19) only says "disabled with a reason" in prose. Add an explicit assertion (e.g. "AND a disabled `Resume` exposes an accessible reason via `title`") so the MUST is testable — mirrors how the Iterate requirement is exercised.

11. **nice-to-have — "transient confirmation" in the Pause scenario is hard to assert deterministically.**
    Scenario "Pause sends a control request and confirms" (line 21-24) requires "MUST show a transient confirmation." A unit test can assert a toast with the expected message was pushed (`useUiStore.getState().toasts`, as `TaskDetail.test.tsx:104-107` does), so this *is* observable — but the scenario should say "a confirmation toast" rather than "transient" (the auto-dismiss timing is not unit-observable and shouldn't be asserted). Reword to keep the scenario testable.

12. **nice-to-have — Accessibility beyond `title` is unspecified.**
    AGENTS.md doesn't mandate a11y, and the existing Iterate button only uses `title`, so matching that is acceptable. Optionally add that disabled buttons set `disabled` (native, so focus/AT semantics are correct) — but a `title` on a `disabled` button is not announced by all screen readers. Nice-to-have, not a blocker; consistent with existing pattern.

13. **Looked at the status sets in scenarios — correct.**
    Terminal set `{succeeded, failed, cancelled}` (line 19) and active gating match `task-control-api` preconditions and `TASK_STATUSES`. No `queued`/`cancelling` leak into the scenarios. Good (but see findings 4–5 for the helper definition itself).

---

## tasks.md

14. **should-fix — No task updates the existing `TaskDetail.test.tsx` / handlers for the new bar.**
    Task 4.1 adds a *new* control MSW handler, and the default `tasks/:id` fixture returns `succeeded` (handlers.ts:62-68) — a terminal status where all three control buttons are disabled. Existing TaskDetail tests (`TaskDetail.test.tsx`) don't reference the bar, so they won't break, but the new TaskDetail tests (4.3-4.5) need a non-terminal fixture via `server.use()` (the `running`/`paused` override pattern already exists at test lines 54-62). Add an explicit task: "new TaskDetail control tests override the detail fixture to `running`/`paused` via `server.use()` (the bar is all-disabled on the default `succeeded` fixture)." Otherwise 4.3 ("clicking Pause on a `running` task") silently assumes an override that isn't called out.

15. **should-fix — Task 1.2 says `toastOnError:false` but omits the `meta:{silent:true}` half (same as finding 1).**
    Task 1.2 (api wrapper) and 1.3 (mutation) are separate; 1.3 should explicitly state `meta:{silent:true}` (it does mention it) — good — but cross-link them: both are required to fully suppress toasts. Currently 1.3 names `meta:{silent:true}`, so this is already covered; just verify the implementer sets `toastOnError:false` in 1.2 (it's stated). Minor; downgrade to nice-to-have. No change strictly required.

16. **should-fix — Task 2.1 types `status` as `string`, but a discriminated `TaskStatus` would catch helper drift.**
    Task 2.1 declares `ControlBar` prop `status: string`. The page passes `loadedTask.status` which is typed `string` in `TaskInfo` (types.ts:77), so `string` is consistent with the codebase — fine. But the *helper* `controlAvailability(status)` (1.4) should accept `TaskStatus` or at least be exhaustively switch-tested, or a typo'd status silently yields all-false. Recommend the helper signature be `controlAvailability(status: string)` with an internal exhaustive check against `TASK_STATUSES`, and the unit test (4.2) assert the all-false default for an unknown status. Add that to 4.2.

17. **nice-to-have — Task 4.5 asserts live-pipeline status reflection but the mechanism it should drive is the *task refetch*, not a raw WS frame.**
    4.5 says "a `status` live frame / refetch … re-derives the bar." In a unit test, simulating a real WS frame is heavy; the deterministic path is to flip the `server.use()` detail fixture to terminal and trigger an invalidation (the mutation's `onSettled` already invalidates). Reword 4.5 to "after a control action settles, the `onSettled` invalidation refetches the (now-terminal) detail fixture and the bar re-derives to all-disabled" — that is what's actually unit-observable, matching D3's `onSettled` claim. As written it risks an un-runnable "live frame" assertion.

18. **nice-to-have — Gates 5.1-5.5 are good and match repo conventions.** `pnpm typecheck/lint/test/format:check` + `openspec validate --strict`. No issue.

---

## Cross-cutting

19. **should-fix — Mutex interaction with the existing Iterate button is never specified.**
    The Iterate button is disabled while `isActiveStatus(task.status)` (TaskDetail.tsx:32,119). The control bar is *enabled* in exactly those active states (pause/resume/cancel need an active task). So the two controls are complementary by status — but the proposal/spec never states that the control bar does **not** participate in the Iterate mutex and vice-versa (e.g. an in-flight control request disables only the three control buttons, not Iterate; an in-flight iterate doesn't disable the bar). Proposal line 24 asserts "the Iterate Action … unaffected and complementary" but no scenario or task pins it. Add a one-line note in design (or a scenario) that the in-flight guards are per-control-group, so a later change doesn't accidentally couple them.

20. **Looked at scope/size (AGENTS.md §7, <500 LOC) — appropriate.**
    One presentational component, ~3 small data-layer additions, a pure helper, one MSW handler, and focused tests. Web-only, additive, no API/worker/schema/MQ change. Well within the small-PR bound; nothing to split or drop. The Non-Goals (reason input, cancel confirm, rollback/branch) are correctly deferred and consistent with the rollback-postponed direction.

21. **Looked at the 202 DTO field names (`accepted`, `action`, `task_id`, `effective`) in tasks 1.1 / 4.1 vs the contract (task-control-api spec line 10) — exact match.** No issue. `effective` enum `{queued, best_effort}` correct.

---

## Summary

| Severity | Count |
|----------|-------|
| blocker | 0 |
| should-fix | 9 (items 1, 4, 5, 6, 9, 10, 14, 16, 19) |
| nice-to-have | 9 (items 2, 3, 7, 11, 12, 13, 15→downgraded, 17, 18, 20, 21 — informational/no-issue notes included) |

Most important: align the toast-suppression mechanism (both `toastOnError:false` and `meta:{silent:true}`, item 1/15); tie the `cancel` enablement helper to the *reachable* task-status set rather than the literal `∉ terminal` to avoid a `cancelling` leak (items 4/5); clarify that the control-POST 404 only surfaces the not-found render via the subsequent task refetch (item 6); and add the missing 400/generic-error and disabled-reason scenarios plus the `server.use()` non-terminal fixture and refetch-driven (not raw-WS) reflection test (items 9, 10, 14, 17).

---

## Resolution (verified + applied by maintainer)

All findings verified against the codebase before acting; `query-client.ts` was read to confirm item 1 (MutationCache.onError toasts unless `meta.silent`, independent of the apiFetch `toastOnError` toast → both layers genuinely required).

Applied:
- **1 / 15** — proposal Data-layer bullet now states both `toastOnError:false` AND `meta:{silent:true}` are set (design D4 already named both).
- **3** — proposal Impact lists the `controlAvailability` helper + its unit test.
- **4 / 5 / 16** — design D2 rewritten: `cancel` encoded **positively** over the reachable set `{pending,running,paused}`; helper takes `status: string`, returns all-false for unknown (incl. version-only `queued`/`cancelling`); "tested across all six `TASK_STATUSES` + unknown". tasks 1.4 and 4.2 updated to match.
- **6** — design D4 now states the control-POST 404 lands in the mutation `onError` and the not-found view appears only via the subsequent `onSettled` task refetch (the render is driven by `taskQuery.error`, not the mutation).
- **9** — spec gains a "Unexpected control error surfaces a generic toast, not a retry" scenario; tasks 4.5 covers it.
- **10** — spec gains a "Disabled action exposes a reason" scenario (`title`); tasks 4.3 asserts it.
- **11** — spec Pause scenario reworded "transient confirmation" → "a confirmation toast" (auto-dismiss timing is not unit-observable).
- **14** — tasks 4.1 notes the default `tasks/:id` fixture is `succeeded` (all-disabled) so control tests must `server.use()` a `running`/`paused` fixture.
- **17** — tasks renumbered; the reflection test (now 4.6) reworded to the deterministic `onSettled`-invalidation→refetch path rather than a raw WS frame.
- **19** — design D5 now states the in-flight guard is scoped to the control group (does not touch Iterate, and vice-versa).

Not changed (confirmed non-issues): 2, 7, 8, 12, 13, 18, 20, 21 — informational / already-correct. Item 12 (a11y beyond `title`) left to match the existing Iterate pattern (out of scope for this round).
