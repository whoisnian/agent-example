## MODIFIED Requirements

### Requirement: Conversation-Style Event Rendering

The event log SHALL render the turn's events as **distinct blocks rather than one mixed bubble**, so plan / step / summary content is not crammed together. It MUST render at most three blocks, in this order, each its own card and each omitted when it has no content:

- **Plan card** — the single `plan` event whose `payload.steps` is a non-empty array, rendered as an ordered step list.
- **Process card** — every remaining recognized/unknown event (step / status / log / error / a malformed plan / unknown kinds), in sequence order, as de-emphasized rows: `step` as a progress row (verdict glyph for pass/finish vs retry + `title` + de-emphasized `summary`); `status` as a human-readable transition line; `log` as small monospace; `error` with destructive styling naming the code/message; anything else as a bounded compact payload preview (the only place a JSON-ish rendering may appear).
- **Summary card** — the last non-empty `summary` event's text, rendered as paragraph prose and visually distinct from the muted plan/process cards (it is the turn's "answer"). An empty summary renders nothing.

`artifact` AND `artifact_deleted` events MUST NOT be rendered in the log at all — file lifecycle (a produced file and its later removal) surfaces only via the per-turn aggregate products card (and only once the version is terminal, where the card reflects the current persisted artifact set with deleted files already absent). `title` and any other non-conversational kind MUST also render nothing (the task title lives in the header; cost flows on a separate exchange and never reaches this event stream). **Never raw JSON for a recognized kind.** A recognized kind whose payload is missing expected fields MUST degrade to the compact fallback row, never throw.

#### Scenario: Plan, process, and summary render as separate blocks

- **WHEN** a turn's events include a `plan` (with steps), `step` events, and a `summary`
- **THEN** the plan MUST render in its own ordered-list card, the steps in a separate process card, and the summary in its own answer card — the summary MUST NOT be inside the process card

#### Scenario: Summary renders as the assistant answer card

- **WHEN** the events include a `summary` event with text
- **THEN** its text MUST render as paragraph prose in a dedicated, visually distinct card (no kind label, no JSON)

#### Scenario: Steps render structured inside the process card

- **WHEN** the events include `step` events with verdicts
- **THEN** each MUST render as a verdict-glyph progress row (title + de-emphasized summary) inside the process card — no raw JSON

#### Scenario: Artifact events are not rendered in the log

- **WHEN** the events include an `artifact` event with `path = "index.html"`
- **THEN** the log MUST NOT render any row or file line for it (products appear only in the aggregate card)

#### Scenario: Artifact-deleted events are not rendered in the log

- **WHEN** the events include an `artifact_deleted` event with `payload = {path: "styles.css", version_id}`
- **THEN** the log MUST NOT render any row, file line, or compact JSON-fallback preview for it (it is hidden exactly like `artifact`); the deleted file's absence surfaces only through the terminal products card's refetched artifact set

#### Scenario: Non-conversational kinds are hidden

- **WHEN** the events include a `title` event
- **THEN** the log MUST NOT render a row for it

#### Scenario: Unknown or malformed payloads degrade safely

- **WHEN** an event has an unrecognized `kind`, or a `plan` event lacks `payload.steps`
- **THEN** the offending event MUST render as the bounded compact payload preview (a process-card fallback row, no dedicated plan card) without throwing
