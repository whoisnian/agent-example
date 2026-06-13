/**
 * TypeScript mirrors of the task-read-api / task-write-api DTOs
 * (`api/internal/domain/task/read_dtos.go`, `interfaces/http/tasks.go`).
 *
 * IMPORTANT: `amount_usd` is a decimal STRING (NUMERIC(18,8) rendered to 8 dp,
 * e.g. "0.62000000"). Never parse it to `number` — the API deliberately avoids
 * float rounding and so must the UI.
 */

// ---- shared ----

export interface CostSummary {
  amount_usd: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  tool_calls: number;
  wall_time_ms: number;
}

/** The six statuses a *task* may carry (tasks_status_check). */
export const TASK_STATUSES = [
  "pending",
  "running",
  "paused",
  "cancelled",
  "succeeded",
  "failed",
] as const;
export type TaskStatus = (typeof TASK_STATUSES)[number];

/**
 * Active-status set, mirroring the API's task-level `IsActive`
 * (`api/.../domain/task/status.go`). Note: `queued` / `cancelling` are
 * version-only and never appear on `task.status` (the event-ingest mapping
 * collapses `queued→pending` and skips `cancelling`), so for a *task* the
 * reachable active set is effectively {pending, running, paused}. The extra
 * two are kept only to match the constant verbatim.
 */
const ACTIVE_STATUSES = new Set<string>(["pending", "queued", "running", "paused", "cancelling"]);

export function isActiveStatus(status: string): boolean {
  return ACTIVE_STATUSES.has(status);
}

// ---- read DTOs ----

export interface TaskSummary {
  id: string;
  title: string;
  task_type: string;
  status: string;
  current_version: string | null;
  created_at: string;
  updated_at: string;
  cost: CostSummary;
}

export interface TaskListPage {
  items: TaskSummary[];
  page: number;
  page_size: number;
  total: number;
}

export interface TaskInfo {
  id: string;
  tenant_id: string;
  user_id: string;
  title: string;
  task_type: string;
  status: string;
  current_version: string | null;
  created_at: string;
  updated_at: string;
}

export interface VersionNode {
  id: string;
  parent_id: string | null;
  version_no: number;
  status: string;
  is_active: boolean;
  artifact_root: string | null;
  created_at: string;
  cost: CostSummary;
}

export interface TaskDetail {
  task: TaskInfo;
  current_version: VersionNode | null;
  cost: CostSummary;
}

export interface VersionTree {
  items: VersionNode[];
}

export interface VersionFull {
  id: string;
  task_id: string;
  parent_id: string | null;
  version_no: number;
  prompt: string;
  params: unknown;
  status: string;
  is_active: boolean;
  artifact_root: string | null;
  /** Run-result summary (worker kind=summary event); present-and-null until a
   *  run emits one. Labels a turn's collapsed execution section without
   *  eagerly fetching that version's events. */
  summary: string | null;
  created_at: string;
}

export interface RunSummary {
  id: string;
  attempt_no: number;
  status: string;
  started_at: string | null;
  ended_at: string | null;
  last_heartbeat: string | null;
  error: unknown;
}

export interface VersionDetail {
  version: VersionFull;
  runs: RunSummary[];
  cost: CostSummary;
}

export interface EventItem {
  id: number;
  version_id: string;
  run_id: string | null;
  seq: number;
  kind: string;
  payload: unknown;
  created_at: string;
}

export interface EventPage {
  items: EventItem[];
  /** Last returned event id, or the input after_id when the page is empty.
   *  "No new events" is `items.length === 0`, NOT a sentinel value. */
  next_after_id: number;
}

// ---- write DTOs ----

export interface CreateTaskRequest {
  /** Optional — when omitted the API derives the title from the prompt. */
  title?: string;
  task_type: string;
  prompt: string;
  params?: unknown;
  lane?: string;
}

export interface CreateTaskResponse {
  task_id: string;
  version_id: string;
  version_no: number;
  status: string;
}

export interface IterateTaskRequest {
  base_version_id?: string;
  prompt: string;
  params?: unknown;
  lane?: string;
}

export interface IterateTaskResponse {
  version_id: string;
  version_no: number;
  status: string;
}

/** Rollback mode (task-rollback-api §6.5): `branch` re-executes from the target
 *  version (201, new version); `switch` only repoints `current_version` (200,
 *  pointer move, no run). */
export type RollbackMode = "branch" | "switch";

export interface RollbackTaskRequest {
  target_version_id: string;
  mode: RollbackMode;
  /** branch only; an empty/omitted prompt is valid (the backend auto-fills
   *  "rollback to version N"). */
  prompt?: string;
  params?: unknown;
  lane?: string;
}

/** 201 `data` for mode=branch (a newly created version). */
export interface RollbackBranchResponse {
  version_id: string;
  version_no: number;
  status: string;
}

/** 200 `data` for mode=switch (a pointer move). NOTE the field name differs
 *  from branch (`current_version_id`, not `version_id`); the two share no
 *  discriminator, so callers key off the requested `mode`, not the response. */
export interface RollbackSwitchResponse {
  current_version_id: string;
  version_no: number;
  status: string;
}

/** `data` of a 409 `active_version_exists` error. */
export interface ActiveVersionConflict {
  active_version_id: string;
  active_version_status: string;
}

/** `data` of a 400 `invalid_input` error (single field; `field:"body"` for
 *  malformed JSON). */
export interface InvalidInputData {
  field: string;
  reason: string;
}

// ---- control DTOs (task-control-api) ----

export type ControlAction = "pause" | "resume" | "cancel";

export interface ControlRequest {
  action: ControlAction;
  /** Optional free-form note (≤200 chars). Not collected by the UI this round. */
  reason?: string;
}

/** `data` of the 202 control response. `effective` discriminates "an active run
 *  was resolved so the worker will receive this" (queued) from "no active run,
 *  the broker may drop the message" (best_effort, i.e. pre-claim cancel). */
export interface ControlResponse {
  accepted: boolean;
  action: ControlAction;
  task_id: string;
  effective: "queued" | "best_effort";
}

/** Which control actions are valid for a given task status. Encoded POSITIVELY
 *  over the reachable task statuses (mirroring the task-control-api advisory
 *  preconditions): pause∈{pending,running}, resume={paused}, cancel∈
 *  {pending,running,paused}. Any unknown status — including the version-only
 *  `queued`/`cancelling` that never reach `task.status` — yields all-false, so a
 *  typo can never enable an action. The API stays authoritative (a 409 is still
 *  handled by the caller). */
export interface ControlAvailability {
  canPause: boolean;
  canResume: boolean;
  canCancel: boolean;
}

export function controlAvailability(status: string): ControlAvailability {
  switch (status) {
    case "pending":
      return { canPause: true, canResume: false, canCancel: true };
    case "running":
      return { canPause: true, canResume: false, canCancel: true };
    case "paused":
      return { canPause: false, canResume: true, canCancel: true };
    default:
      // succeeded / failed / cancelled / unknown → no control possible.
      return { canPause: false, canResume: false, canCancel: false };
  }
}
