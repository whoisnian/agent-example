# web-artifacts-views Specification

## Purpose

The `features/artifacts/` slice of the web client provides typed, owner-scoped access to a version's produced artifacts. It exposes the per-version artifact list (`GET /api/v1/versions/{version_id}/artifacts`) through the shared `apiFetch` + React Query read pattern, and an on-demand single-artifact presign action (`GET /api/v1/artifacts/{artifact_id}/presign`) modeled as a non-cached mutation that re-mints a short-lived API-signed relative download URL (opaque to the client; served by the API download proxy since `add-artifact-download-proxy`) on each invocation. The slice keeps error surfacing single (no double toasts) and preserves the backend's serialization contract (server ordering, present-and-nullable metadata, opaque `kind`). This capability was established by archiving the `add-web-artifacts-views` change.

## Requirements

### Requirement: Version Artifact Data Access

The web client SHALL provide a `features/artifacts/` data-access slice exposing typed access to the `artifacts-api` endpoints through the existing `apiFetch` + React Query pattern.

The slice MUST expose a read for `GET /api/v1/versions/{version_id}/artifacts` parsed as `{ version_id: string; artifacts: ArtifactMeta[] }`, where each `ArtifactMeta` is `{ id: string; kind: string; mime: string | null; bytes: number | null; sha256: string | null; created_at: string }`. The slice MUST preserve the server-provided ordering (`created_at ASC, id ASC`) and MUST NOT re-sort client-side. The nullable fields (`mime`, `bytes`, `sha256`) MUST be modeled as present-and-nullable (`T | null`), never optional/omitted, mirroring the backend serialization. `kind` MUST be treated as opaque free text and MUST NOT be validated or enumerated by the client.

The read MUST be keyed by `version_id` and SHALL be lazily enabled (`enabled: !!versionId`) so it only fires when a consumer actually requests that version's artifacts. Because the consuming surface renders its own inline error/empty state, the list read MUST suppress BOTH toast layers — `toastOnError:false` on the transport call AND `meta:{silent:true}` on the query — so the inline state is the single error surface (mirroring `getMyCost`+`useMyCostQuery`), never a duplicate toast. A `404` (`version_not_found`) MUST additionally skip retry, surfacing as a no-op/empty render rather than a thrown unhandled error — mirroring `useTaskCostQuery`. An owned version with no artifacts yet returns `artifacts: []` (HTTP `200`, never `404`) and MUST therefore render as an empty state, not a not-found state. Non-`404` errors (including the practically-unreachable `400 invalid_input` from a malformed id — `version_id` is always a server-sourced `VersionNode.id`) MUST follow the same posture as `useTaskCostQuery` and render the consumer's error state; no distinct `400` handling is required (consistent with the archived `web-cost-views`).

The slice MUST expose a presign action for `GET /api/v1/artifacts/{artifact_id}/presign` parsed as `{ url: string; expires_at: string; bytes: number | null; mime: string | null; sha256: string | null }`. Because the URL is short-lived (the backend `OSS_PRESIGN_TTL`, default minutes) the presign result MUST NOT be stored in the React Query cache — it MUST be modeled as an on-demand action (a React Query mutation) that re-mints on each invocation. To avoid a double toast, the action MUST be silent at BOTH toast layers — the transport call MUST pass `toastOnError:false` AND the mutation MUST set `meta:{silent:true}` (mirroring the existing `features/tasks/mutations.ts` convention, since `query-client.ts`'s `mutationCache.onError` toasts every non-silent mutation) — and the calling component's `onError` MUST be the single error surface. A presign error (`artifact_not_found` `404`, or an `internal_error` `500`) MUST surface through that component error path (exactly one toast and/or a per-row error), not be swallowed and not be double-toasted.

`bytes` is a plain integer file size (NOT money) and MAY be converted to a `number` for human-readable formatting; this is distinct from the project's decimal-string rule, which applies ONLY to monetary `amount_usd` values and is unaffected by this slice.

#### Scenario: Version artifact list parses in server order with nullable fields preserved

- **WHEN** the slice reads `/api/v1/versions/{id}/artifacts` for a version with two artifacts, one of which has `mime`, `bytes`, and `sha256` all `null`
- **THEN** the parsed value MUST be `{ version_id, artifacts }` with the two entries in the server-provided `created_at ASC, id ASC` order (no client re-sort), and the null-metadata entry MUST carry `mime: null`, `bytes: null`, `sha256: null` (present-and-null, never omitted)

#### Scenario: Owned-but-empty version reads as an empty list, not 404

- **WHEN** the slice reads the artifacts of an owned version whose run produced no files
- **THEN** the response MUST parse to `{ version_id, artifacts: [] }` (HTTP `200`), and the consumer MUST be able to distinguish this empty state from a `version_not_found`

#### Scenario: Unowned/unknown version skips retry and stays silent

- **WHEN** the artifact-list read receives a `404` with `code = "version_not_found"`
- **THEN** the query MUST NOT retry the `404` and MUST suppress the cache toast (`meta.silent`), so the failure renders as a no-op/empty state rather than an unhandled rejection

#### Scenario: Presign mints a fresh URL on demand and is never cached

- **WHEN** the user invokes the download action for an artifact and the presign read returns `{ url, expires_at, bytes, mime, sha256 }`
- **THEN** the slice MUST return that exact `url`/`expires_at` to the caller without storing it in the React Query cache, and a subsequent invocation MUST issue a new presign request (re-mint) rather than serve a cached URL

#### Scenario: Presign failure surfaces exactly one error, never double-toasts

- **WHEN** the presign action receives a `404` (`artifact_not_found`) or a `500` (`internal_error`)
- **THEN** the failure MUST surface through the calling component's `onError` as a single user-facing error (one toast and/or a per-action indication), and MUST NOT be silently swallowed, AND MUST NOT fire a second toast from either the transport layer or `mutationCache.onError` (the action is `toastOnError:false` + `meta:{silent:true}`)
