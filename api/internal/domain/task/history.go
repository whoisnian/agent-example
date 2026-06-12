// Conversation-history assembly (refactor-task-conversation-continuity).
//
// History = the base version's parent chain rendered oldest→newest, one turn
// per version. The chain — not a separate conversation store — is the single
// source of truth, so rollback-branch automatically yields the correct
// branch-local history. Assembly happens inside the iterate / rollback-branch
// transaction and the result is frozen into the execute outbox payload; any
// republish reuses the stored payload (spec: task-conversation-history).
package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// History assembly bounds (design D3). 20 caps the DB chain walk; 16 KiB is
// the authoritative serialized cap (maximally-sized turns retain ~7); per-turn
// fields are rune-truncated to 1024 bytes each.
const (
	maxHistoryTurns      = 20
	maxHistoryFieldBytes = 1024
	maxHistoryBytes      = 16 * 1024
)

// HistoryTurn is one conversation turn in the execute payload's `history`
// array (docs/ARCHITECTURE.md §5.3). Summary is null for versions without a
// worker summary (failed runs, pre-migration rows, event not yet consumed).
type HistoryTurn struct {
	VersionNo int32   `json:"version_no"`
	Prompt    string  `json:"prompt"`
	Summary   *string `json:"summary"`
	Status    string  `json:"status"`
}

// historyStats reports what assembly did, for the handler's structured log.
type historyStats struct {
	Turns        int // turns kept in the payload
	DroppedDepth int // versions beyond the chain-walk depth bound
	DroppedSize  int // whole turns dropped to fit the serialized cap
}

// assembleHistory walks the parent chain from baseID upward (bounded
// point-reads on the tx-bound queries) and returns the bounded, oldest→newest
// turn list. A missing chain entry is an integrity error and propagates.
func assembleHistory(
	ctx context.Context,
	q *sqlc.Queries,
	baseID uuid.UUID,
) ([]HistoryTurn, historyStats, error) {
	var stats historyStats
	// Walk newest→oldest, then reverse. The loop is bounded by
	// maxHistoryTurns; parent_id chains cannot cycle (parent precedes child).
	newestFirst := make([]HistoryTurn, 0, maxHistoryTurns)
	cur := baseID
	for range maxHistoryTurns {
		row, err := q.GetVersionChainEntry(ctx, toPgUUID(cur))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, stats, fmt.Errorf("history chain broken at version %s: %w", cur, err)
			}
			return nil, stats, fmt.Errorf("walk history chain: %w", err)
		}
		newestFirst = append(newestFirst, HistoryTurn{
			VersionNo: row.VersionNo,
			Prompt:    truncateHistoryField(row.Prompt),
			Summary:   truncateHistorySummary(row.Summary),
			Status:    row.Status,
		})
		if !row.ParentID.Valid {
			break
		}
		cur = fromPgUUID(row.ParentID)
	}
	return boundHistoryTurns(newestFirst)
}

// boundHistoryTurns reverses the walk order (newest-first → oldest-first) and
// enforces the serialized cap by dropping whole turns from the oldest end.
// Pure function — the DB walk above feeds it; tests drive it directly.
func boundHistoryTurns(newestFirst []HistoryTurn) ([]HistoryTurn, historyStats, error) {
	var stats historyStats
	// The walk stopped at the depth bound while a parent remained: count it as
	// depth-dropped for observability (we don't know how many more there are
	// without walking; 1 marks "chain was cut", which is what the log needs).
	if len(newestFirst) == maxHistoryTurns {
		stats.DroppedDepth = 1
	}

	turns := make([]HistoryTurn, len(newestFirst))
	for i, t := range newestFirst {
		turns[len(turns)-1-i] = t
	}

	// Serialized cap: drop whole turns from the oldest end until it fits.
	for len(turns) > 0 {
		b, err := json.Marshal(turns)
		if err != nil {
			return nil, stats, fmt.Errorf("marshal history: %w", err)
		}
		if len(b) <= maxHistoryBytes {
			break
		}
		turns = turns[1:]
		stats.DroppedSize++
	}
	stats.Turns = len(turns)
	return turns, stats, nil
}

// truncateHistoryField rune-truncates a turn field to maxHistoryFieldBytes,
// appending an ellipsis when a cut happened (counted inside the limit).
func truncateHistoryField(s string) string {
	if len(s) <= maxHistoryFieldBytes {
		return s
	}
	const ellipsis = "…"
	maxBytes := maxHistoryFieldBytes - len(ellipsis)
	cut := len(s)
	for i, r := range s {
		if i+utf8.RuneLen(r) > maxBytes {
			cut = i
			break
		}
	}
	return strings.TrimRightFunc(s[:cut], unicode.IsSpace) + ellipsis
}

// truncateHistorySummary keeps NULL as nil and truncates non-null values.
func truncateHistorySummary(s *string) *string {
	if s == nil {
		return nil
	}
	v := truncateHistoryField(*s)
	return &v
}
