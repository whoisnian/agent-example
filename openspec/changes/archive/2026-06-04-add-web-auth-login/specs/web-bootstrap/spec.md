## MODIFIED Requirements

### Requirement: Route Skeleton

The router SHALL declare the following routes (placeholder rows display the route name and a "not implemented" notice; real rows render their implemented component):

| Path | Component | Notes |
|---|---|---|
| `/` | redirect → `/tasks` | |
| `/tasks` | `TaskList` | |
| `/tasks/new` | `TaskCreate` | |
| `/tasks/:id` | `TaskDetail` | reads `:id` from params |
| `/cost` | `CostDashboard` | |
| `/settings` | `SettingsPlaceholder` | |
| `/login` | `LoginPage` | rendered outside `RootLayout`; real email/password login (see `web-auth`) |
| `*` | `NotFoundPlaceholder` | |

Unauthenticated access to any route except `/login` MUST redirect to `/login`. Authentication state is read from the auth store (token presence in `localStorage`). Token validity is established by `LoginPage` against the API and re-checked lazily: an invalid or expired token surfaces as a `401` on the first authenticated request, which clears the session and redirects to `/login` (see `web-data-access` 401 Handling).

#### Scenario: Unauthenticated redirect
- **WHEN** the user navigates to `/tasks` without a token in `localStorage`
- **THEN** the router MUST redirect to `/login`

#### Scenario: Route parameter parsed
- **WHEN** the user navigates to `/tasks/abc-123`
- **THEN** `TaskDetail` MUST render and read `abc-123` as the task id from the route params
