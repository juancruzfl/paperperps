package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is satisfied by *pgxpool.Pool and pgx.Tx.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// AdminAction is one row of the audit log: every change made through the admin API.
type AdminAction struct {
	ID        int64          `json:"id"`
	At        time.Time      `json:"at"`
	Action    string         `json:"action"`
	AccountID *uuid.UUID     `json:"accountId"`
	Handle    *string        `json:"handle"` // at the time of the action
	Reason    string         `json:"reason"`
	Detail    map[string]any `json:"detail"`
}

// RecordAction appends to the audit log. Pass the transaction that makes the change, so
// the change and its record commit together.
func RecordAction(ctx context.Context, q Execer, a AdminAction) error {
	if a.Detail == nil {
		a.Detail = map[string]any{}
	}
	_, err := q.Exec(ctx, `INSERT INTO admin_actions (action, account_id, handle, reason, detail) VALUES ($1, $2, $3, $4, $5)`,
		a.Action, a.AccountID, a.Handle, a.Reason, a.Detail)
	if err != nil {
		return fmt.Errorf("record admin action: %w", err)
	}
	return nil
}

// Actions returns the latest audit log entries, newest first, optionally for one account.
func (s *Store) Actions(ctx context.Context, accountID *uuid.UUID, limit int) ([]AdminAction, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, at, action, account_id, handle, reason, detail FROM admin_actions
		WHERE $1::uuid IS NULL OR account_id = $1
		ORDER BY id DESC LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("admin actions: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminAction, error) {
		var a AdminAction
		err := r.Scan(&a.ID, &a.At, &a.Action, &a.AccountID, &a.Handle, &a.Reason, &a.Detail)
		return a, err
	})
}

// AccountSummary is an account as the admin search lists it.
type AccountSummary struct {
	ID                uuid.UUID  `json:"accountId"`
	Handle            string     `json:"handle"`
	CreatedAt         time.Time  `json:"createdAt"`
	Trades            int        `json:"trades"`
	LeaderboardOptIn  bool       `json:"leaderboardOptIn"`
	LeaderboardHidden bool       `json:"leaderboardHidden"`
	SuspendedAt       *time.Time `json:"suspendedAt"`
}

// Search finds accounts by exact ID or by a substring of the handle.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]AccountSummary, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	id, idErr := uuid.Parse(query)
	var byID *uuid.UUID
	if idErr == nil {
		byID = &id
	}
	// Escape LIKE wildcards so the query is matched literally.
	pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query) + "%"
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.handle, a.created_at,
			(SELECT count(*) FROM fills f WHERE f.account_id = a.id),
			a.leaderboard_opt_in, a.leaderboard_banned, a.suspended_at
		FROM accounts a
		WHERE a.id = $1 OR a.handle LIKE $2
		ORDER BY a.handle LIMIT $3`, byID, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("search accounts: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AccountSummary, error) {
		var a AccountSummary
		err := r.Scan(&a.ID, &a.Handle, &a.CreatedAt, &a.Trades, &a.LeaderboardOptIn, &a.LeaderboardHidden, &a.SuspendedAt)
		return a, err
	})
}

// handleOf reads an account's handle inside tx, locking the row.
func handleOf(ctx context.Context, tx pgx.Tx, id uuid.UUID) (string, error) {
	var h string
	err := tx.QueryRow(ctx, `SELECT handle FROM accounts WHERE id = $1 FOR UPDATE`, id).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return h, err
}

// AdminRename gives the account a new handle: newHandle, or a random one when empty.
// It returns the handle it set.
func (s *Store) AdminRename(ctx context.Context, id uuid.UUID, newHandle, reason string) (string, error) {
	random := newHandle == ""
	if !random {
		newHandle = strings.ToLower(strings.TrimSpace(newHandle))
		if err := ValidateHandle(newHandle); err != nil {
			return "", err
		}
	}
	for attempt := 0; ; attempt++ {
		h := newHandle
		if random {
			h = randomHandle()
		}
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			old, err := handleOf(ctx, tx, id)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE accounts SET handle = $2 WHERE id = $1`, id, h); err != nil {
				return err
			}
			return RecordAction(ctx, tx, AdminAction{Action: "rename", AccountID: &id, Handle: &old, Reason: reason,
				Detail: map[string]any{"from": old, "to": h}})
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if random && attempt < 5 {
				continue
			}
			return "", ErrHandleTaken
		}
		if err != nil {
			return "", err
		}
		return h, nil
	}
}

// SetLeaderboardHidden takes the account off the leaderboard, or puts it back. Unlike the
// account's own opt-out, the account can't undo it.
func (s *Store) SetLeaderboardHidden(ctx context.Context, id uuid.UUID, hidden bool, reason string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		h, err := handleOf(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE accounts SET leaderboard_banned = $2 WHERE id = $1`, id, hidden); err != nil {
			return err
		}
		action := "hide"
		if !hidden {
			action = "unhide"
		}
		return RecordAction(ctx, tx, AdminAction{Action: action, AccountID: &id, Handle: &h, Reason: reason})
	})
}

// UseNonce records a signed admin request's nonce until it expires. It returns false if the
// nonce was already used: the request is a replay. Expired nonces are cleared as it goes.
func (s *Store) UseNonce(ctx context.Context, nonce string, expires time.Time) (bool, error) {
	if _, err := s.pool.Exec(ctx, `DELETE FROM admin_nonces WHERE expires_at < now()`); err != nil {
		return false, fmt.Errorf("clear admin nonces: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO admin_nonces (nonce, expires_at) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		nonce, expires)
	if err != nil {
		return false, fmt.Errorf("record admin nonce: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// FlaggedHandles lists accounts whose handle fails today's filter: handles chosen before
// a term was added to it. It reads every handle, which is fine at this site's size.
func (s *Store) FlaggedHandles(ctx context.Context) ([]AccountSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.handle, a.created_at,
			(SELECT count(*) FROM fills f WHERE f.account_id = a.id),
			a.leaderboard_opt_in, a.leaderboard_banned, a.suspended_at
		FROM accounts a ORDER BY a.created_at`)
	if err != nil {
		return nil, fmt.Errorf("flagged handles: %w", err)
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AccountSummary, error) {
		var a AccountSummary
		err := r.Scan(&a.ID, &a.Handle, &a.CreatedAt, &a.Trades, &a.LeaderboardOptIn, &a.LeaderboardHidden, &a.SuspendedAt)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("flagged handles: %w", err)
	}
	flagged := []AccountSummary{}
	for _, a := range all {
		if Offensive(a.Handle) {
			flagged = append(flagged, a)
		}
	}
	return flagged, nil
}
