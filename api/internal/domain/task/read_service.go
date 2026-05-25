package task

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// Pagination / cursor upper bounds enforced by the read service (design D3 /
// D7). The *defaults* applied to absent query params live in the HTTP layer.
const (
	maxPageSize   = 100
	maxEventLimit = 1000
)

// Owner is the single caller-identity value type threaded through every read
// method (design D1 / S8). Ownership holds only when BOTH tenant and user
// match, so a same-tenant / different-user row is hidden.
type Owner struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
}

// owns reports whether the given (pgtype) owning columns belong to this Owner.
// It converts pgtype.UUID → uuid.UUID and requires Valid (the columns are
// NOT NULL, but the explicit check stops an all-zero identity from silently
// matching an all-zero column).
func (o Owner) owns(tenantID, userID pgtype.UUID) bool {
	return tenantID.Valid && userID.Valid &&
		uuid.UUID(tenantID.Bytes) == o.TenantID &&
		uuid.UUID(userID.Bytes) == o.UserID
}

// ReadService is the queries-only read side of the Task aggregate. Unlike the
// write Service it holds no pool / clock / id-generator: every method is a
// read with ownership enforcement and DTO assembly.
type ReadService struct {
	Queries *sqlc.Queries
}

// NewReadService constructs the read service.
func NewReadService(q *sqlc.Queries) *ReadService {
	return &ReadService{Queries: q}
}

// ListTasks returns the caller's tasks (newest first) with offset pagination
// and an optional status filter, each row carrying a batched cost summary.
// page is clamped to a minimum of 1 and pageSize to [1, maxPageSize]; the
// returned page/pageSize echo the effective values.
func (s *ReadService) ListTasks(ctx context.Context, owner Owner, page, pageSize int, status *string) (TaskListPage, error) {
	page = clampPage(page)
	pageSize = clampPageSize(pageSize)
	offset := (page - 1) * pageSize

	rows, err := s.Queries.ListTasks(ctx, sqlc.ListTasksParams{
		TenantID: toPgUUID(owner.TenantID),
		UserID:   toPgUUID(owner.UserID),
		Limit:    int32(pageSize),
		Offset:   int32(offset),
		Status:   status,
	})
	if err != nil {
		return TaskListPage{}, err
	}

	total, err := s.Queries.CountTasks(ctx, sqlc.CountTasksParams{
		TenantID: toPgUUID(owner.TenantID),
		UserID:   toPgUUID(owner.UserID),
		Status:   status,
	})
	if err != nil {
		return TaskListPage{}, err
	}

	costByTask, err := s.taskCosts(ctx, rows)
	if err != nil {
		return TaskListPage{}, err
	}

	items := make([]TaskSummary, len(rows))
	for i := range rows {
		r := &rows[i]
		id := fromPgUUID(r.ID)
		cost, ok := costByTask[id]
		if !ok {
			cost = zeroCost()
		}
		items[i] = TaskSummary{
			ID:             id,
			Title:          r.Title,
			TaskType:       r.TaskType,
			Status:         r.Status,
			CurrentVersion: pgUUIDToPtr(r.CurrentVersion),
			CreatedAt:      r.CreatedAt.Time,
			UpdatedAt:      r.UpdatedAt.Time,
			Cost:           cost,
		}
	}
	return TaskListPage{Items: items, Page: page, PageSize: pageSize, Total: total}, nil
}

// taskCosts batch-fetches per-task cost summaries for the listed tasks, keyed
// by task id (absent tasks are simply missing → caller zero-fills).
func (s *ReadService) taskCosts(ctx context.Context, rows []sqlc.Task) (map[uuid.UUID]CostSummary, error) {
	out := make(map[uuid.UUID]CostSummary, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]pgtype.UUID, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	costRows, err := s.Queries.ListTaskCostsByTasks(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range costRows {
		cr := &costRows[i]
		out[fromPgUUID(cr.TaskID)] = costFromListRow(cr)
	}
	return out, nil
}

// GetTask returns a task the caller owns, its current-version summary (nil when
// current_version is NULL), and its task-level cost. Missing or unowned tasks
// map to ErrTaskNotFound (never 403).
func (s *ReadService) GetTask(ctx context.Context, owner Owner, taskID uuid.UUID) (TaskDetail, error) {
	t, err := s.Queries.GetTaskByID(ctx, toPgUUID(taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskDetail{}, ErrTaskNotFound
	}
	if err != nil {
		return TaskDetail{}, err
	}
	if !owner.owns(t.TenantID, t.UserID) {
		return TaskDetail{}, ErrTaskNotFound
	}

	detail := TaskDetail{
		Task: TaskInfo{
			ID:             fromPgUUID(t.ID),
			TenantID:       fromPgUUID(t.TenantID),
			UserID:         fromPgUUID(t.UserID),
			Title:          t.Title,
			TaskType:       t.TaskType,
			Status:         t.Status,
			CurrentVersion: pgUUIDToPtr(t.CurrentVersion),
			CreatedAt:      t.CreatedAt.Time,
			UpdatedAt:      t.UpdatedAt.Time,
		},
	}

	taskCost, err := s.Queries.GetTaskCost(ctx, toPgUUID(taskID))
	if err != nil {
		return TaskDetail{}, err
	}
	detail.Cost = costFromTaskAgg(&taskCost)

	if t.CurrentVersion.Valid {
		v, verr := s.Queries.GetTaskVersionByID(ctx, t.CurrentVersion)
		switch {
		case verr == nil:
			node := versionNodeFromRow(&v)
			node.Cost, err = s.versionCost(ctx, v.ID)
			if err != nil {
				return TaskDetail{}, err
			}
			detail.CurrentVersion = &node
		case errors.Is(verr, pgx.ErrNoRows):
			// dangling pointer (FK makes this impossible): leave null.
		default:
			return TaskDetail{}, verr
		}
	}
	return detail, nil
}

// ListVersions returns the flat, version_no-ordered tree for a task the caller
// owns, each node carrying a batched cost summary.
func (s *ReadService) ListVersions(ctx context.Context, owner Owner, taskID uuid.UUID) (VersionTree, error) {
	if _, err := s.ownedTask(ctx, owner, taskID); err != nil {
		return VersionTree{}, err
	}

	versions, err := s.Queries.ListVersionsByTask(ctx, toPgUUID(taskID))
	if err != nil {
		return VersionTree{}, err
	}

	costRows, err := s.Queries.ListVersionCostsByTask(ctx, toPgUUID(taskID))
	if err != nil {
		return VersionTree{}, err
	}
	costByVersion := make(map[uuid.UUID]CostSummary, len(costRows))
	for i := range costRows {
		c := &costRows[i]
		costByVersion[fromPgUUID(c.VersionID)] = costFromTaskCost(c)
	}

	items := make([]VersionNode, len(versions))
	for i := range versions {
		v := &versions[i]
		node := versionNodeFromRow(v)
		if c, ok := costByVersion[fromPgUUID(v.ID)]; ok {
			node.Cost = c
		} else {
			node.Cost = zeroCost()
		}
		items[i] = node
	}
	return VersionTree{Items: items}, nil
}

// GetVersion returns a version reachable through a task the caller owns, with
// its runs (oldest attempt first) and cost. A missing version, or one whose
// owning task is missing/unowned, maps to ErrVersionNotFound (never 500/403).
func (s *ReadService) GetVersion(ctx context.Context, owner Owner, versionID uuid.UUID) (VersionDetail, error) {
	v, err := s.ownedVersion(ctx, owner, versionID)
	if err != nil {
		return VersionDetail{}, err
	}

	runs, err := s.Queries.ListRunsByVersion(ctx, toPgUUID(versionID))
	if err != nil {
		return VersionDetail{}, err
	}
	runItems := make([]RunSummary, len(runs))
	for i := range runs {
		runItems[i] = runSummaryFromRow(&runs[i])
	}

	cost, err := s.versionCost(ctx, v.ID)
	if err != nil {
		return VersionDetail{}, err
	}

	return VersionDetail{
		Version: versionFullFromRow(&v),
		Runs:    runItems,
		Cost:    cost,
	}, nil
}

// ListVersionEvents returns the version's events with id > afterID, ascending,
// for a version the caller owns. limit is clamped to [1, maxEventLimit].
func (s *ReadService) ListVersionEvents(ctx context.Context, owner Owner, versionID uuid.UUID, afterID int64, limit int) (EventPage, error) {
	v, err := s.ownedVersion(ctx, owner, versionID)
	if err != nil {
		return EventPage{}, err
	}

	rows, err := s.Queries.ListVersionEventsAfter(ctx, sqlc.ListVersionEventsAfterParams{
		TaskID:    v.TaskID,
		VersionID: toPgUUID(versionID),
		ID:        afterID,
		Limit:     int32(clampEventLimit(limit)),
	})
	if err != nil {
		return EventPage{}, err
	}

	items := make([]EventItem, len(rows))
	next := afterID
	for i := range rows {
		items[i] = eventItemFromRow(&rows[i])
		next = rows[i].ID
	}
	return EventPage{Items: items, NextAfterID: next}, nil
}

// ownedTask loads a task and enforces ownership, mapping missing/unowned to
// ErrTaskNotFound.
func (s *ReadService) ownedTask(ctx context.Context, owner Owner, taskID uuid.UUID) (sqlc.Task, error) {
	t, err := s.Queries.GetTaskByID(ctx, toPgUUID(taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Task{}, ErrTaskNotFound
	}
	if err != nil {
		return sqlc.Task{}, err
	}
	if !owner.owns(t.TenantID, t.UserID) {
		return sqlc.Task{}, ErrTaskNotFound
	}
	return t, nil
}

// ownedVersion loads a version, resolves its owning task, and enforces
// ownership — mapping a missing version, a missing owning task (dangling, FK
// makes it impossible), or an unowned task all to ErrVersionNotFound. A genuine
// DB error on the task lookup propagates (→ 500), never masked.
func (s *ReadService) ownedVersion(ctx context.Context, owner Owner, versionID uuid.UUID) (sqlc.TaskVersion, error) {
	v, err := s.Queries.GetTaskVersionByID(ctx, toPgUUID(versionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.TaskVersion{}, ErrVersionNotFound
	}
	if err != nil {
		return sqlc.TaskVersion{}, err
	}

	t, terr := s.Queries.GetTaskByID(ctx, v.TaskID)
	if errors.Is(terr, pgx.ErrNoRows) {
		return sqlc.TaskVersion{}, ErrVersionNotFound
	}
	if terr != nil {
		return sqlc.TaskVersion{}, terr
	}
	if !owner.owns(t.TenantID, t.UserID) {
		return sqlc.TaskVersion{}, ErrVersionNotFound
	}
	return v, nil
}

// versionCost returns the version-level cost summary, zero-filled when no
// task_costs row exists yet.
func (s *ReadService) versionCost(ctx context.Context, versionID pgtype.UUID) (CostSummary, error) {
	c, err := s.Queries.GetVersionCost(ctx, versionID)
	switch {
	case err == nil:
		return costFromTaskCost(&c), nil
	case errors.Is(err, pgx.ErrNoRows):
		return zeroCost(), nil
	default:
		return CostSummary{}, err
	}
}

func clampPage(page int) int {
	if page < 1 {
		return 1
	}
	return page
}

func clampPageSize(pageSize int) int {
	switch {
	case pageSize < 1:
		return 1
	case pageSize > maxPageSize:
		return maxPageSize
	default:
		return pageSize
	}
}

func clampEventLimit(limit int) int {
	switch {
	case limit < 1:
		return 1
	case limit > maxEventLimit:
		return maxEventLimit
	default:
		return limit
	}
}
