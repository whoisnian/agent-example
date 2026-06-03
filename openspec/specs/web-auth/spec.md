# web-auth Specification

## Purpose

The `features/auth/` slice of the web client owns the user-facing authentication flow: a `LoginPage` at `/login` that exchanges email/password for a session via `POST /api/v1/auth/login`, inline credential/input error surfacing that mirrors the API's indistinguishability, a guarded post-login redirect back to the originally requested route, a logout control that clears the session, and the authenticated identity display in the application shell. It builds on the session-aware auth store and 401-handling opt-out defined in `web-data-access` and the `/login` route gating defined in `web-bootstrap`. This capability was established by archiving the `add-web-auth-login` change.

## Requirements

### Requirement: Login Page

The web client SHALL provide a `LoginPage` at route `/login` (rendered outside `RootLayout`) with an email field, a password field, and a submit control. Submitting SHALL call `POST /api/v1/auth/login` with `{email, password}`. While the request is in flight the submit control MUST be disabled to prevent duplicate submissions. On success the page MUST store the returned session (token + `user`) via the auth store and leave `/login`. The login request MUST suppress both the global error toast and the React Query cache toast so the page is the sole error surface.

#### Scenario: Successful login stores the session and enters the app
- **WHEN** the user submits valid credentials
- **THEN** `POST /api/v1/auth/login` is called, the returned `token` and `user` are stored in the auth store, and the app navigates away from `/login`

#### Scenario: Submit is disabled while the request is pending
- **WHEN** the login request is in flight
- **THEN** the submit control MUST be disabled until the request settles

#### Scenario: Login errors never raise a global toast
- **WHEN** the login request fails with any error code
- **THEN** no global error toast is emitted; the error is shown only on the page

### Requirement: Credential and Input Error Surface

The login page SHALL display credential and validation errors inline without clearing the session or navigating. A `401 invalid_credentials` MUST render a single fixed message that does NOT reveal whether the email or the password was wrong (mirroring the API's indistinguishability). A `400 invalid_input` MUST render a generic input-error message. Any other failure (network, timeout, 5xx) MUST render its error message inline. A failed login MUST NOT trigger the transport's 401 clear-token-and-redirect behavior.

#### Scenario: Wrong credentials show one indistinct message
- **WHEN** the login request returns `401` with `code:"invalid_credentials"`
- **THEN** the page MUST show a single message that does not indicate which field was wrong, MUST NOT clear any existing token, and MUST NOT redirect

#### Scenario: Malformed input shows a generic message
- **WHEN** the login request returns `400` with `code:"invalid_input"`
- **THEN** the page MUST show a generic input-error message inline and remain on `/login`

#### Scenario: Transport/server failure shows inline
- **WHEN** the login request fails with a network error, timeout, or 5xx
- **THEN** the page MUST show the error inline and remain on `/login`

### Requirement: Post-Login Redirect

On successful login the client SHALL navigate to the route the user originally requested, read from the router state `from` that `RequireAuth` stashed at redirect time. The target MUST be used only when it is a safe internal path: it MUST begin with `/` AND its second character MUST be neither `/` nor `\` (rejecting protocol-relative targets such as `//evil.example` and `/\evil.example`). Otherwise the client MUST fall back to `/tasks`.

#### Scenario: Redirect back to the intended route
- **WHEN** an unauthenticated user is redirected from `/tasks/abc-123` to `/login` and then logs in successfully
- **THEN** the client MUST navigate to `/tasks/abc-123`

#### Scenario: Fallback and open-redirect guard
- **WHEN** there is no `from` state, or `from` is not a safe internal path (e.g. `//evil.example`, `/\evil.example`, or an absolute URL)
- **THEN** the client MUST navigate to `/tasks`

### Requirement: Logout

The application shell SHALL provide a logout control. Activating it MUST clear the session (token and `user`) from the auth store and navigate to `/login`. After logout, the authenticated tree MUST be inaccessible without logging in again.

#### Scenario: Logout clears the session and returns to login
- **WHEN** the user activates the logout control
- **THEN** the auth store's token and `user` MUST be cleared and the app MUST navigate to `/login`

#### Scenario: Authenticated routes are gated after logout
- **WHEN** the user has logged out and navigates to an authenticated route such as `/tasks`
- **THEN** `RequireAuth` MUST redirect to `/login`

### Requirement: Authenticated Identity Display

While authenticated, the application shell SHALL display the logged-in user's email (from the stored `user`) so the active identity is visible. The display MUST tolerate a null `user` (rendering no identity text rather than dereferencing it), so a render that occurs before/without a populated `user` — e.g. a half-rehydrated legacy session or the moment after logout — never throws.

#### Scenario: Shell shows the logged-in email
- **WHEN** a session with `user.email = "dev@example.com"` is stored and an authenticated route renders
- **THEN** the shell MUST display `dev@example.com`

#### Scenario: Null user renders no identity text without error
- **WHEN** an authenticated route renders while `user` is `null`
- **THEN** the shell MUST render without error and MUST NOT show an identity email
