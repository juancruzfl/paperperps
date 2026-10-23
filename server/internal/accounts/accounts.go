// Package accounts creates anonymous accounts, authenticates them by secret, and reads
// trading state and history. Only the engine writes trading state.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrNotFound     = errors.New("not found")
)

// Querier is satisfied by *pgxpool.Pool, *pgx.Conn and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Store struct {
	pool            *pgxpool.Pool
	startingBalance decimal.Decimal
}

func NewStore(pool *pgxpool.Pool, startingBalance decimal.Decimal) *Store {
	return &Store{pool: pool, startingBalance: startingBalance}
}

// Created is returned once, when an account is made. The secret is never stored or shown
// again; only its hash is kept.
type Created struct {
	ID     uuid.UUID `json:"accountId"`
	Secret string    `json:"secret"`
	Handle string    `json:"handle"`
}

// Create makes an anonymous account funded with the starting balance.
func (s *Store) Create(ctx context.Context) (Created, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return Created{}, err
	}
	secret := base64.RawURLEncoding.EncodeToString(buf)

	for attempt := 0; ; attempt++ {
		c := Created{ID: mustV7(), Secret: secret, Handle: randomHandle()}
		err := s.insertAccount(ctx, c)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && attempt < 5 {
			continue // handle taken; draw another
		}
		return c, err
	}
}

func (s *Store) insertAccount(ctx context.Context, c Created) error {
	runID := mustV7()
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// accounts.current_run_id is checked at commit, so the run can be inserted second.
		if _, err := tx.Exec(ctx,
			`INSERT INTO accounts (id, secret_hash, handle, current_run_id) VALUES ($1, $2, $3, $4)`,
			c.ID, HashSecret(c.Secret), c.Handle, runID); err != nil {
			return fmt.Errorf("insert account: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO runs (id, account_id, starting_balance, balance) VALUES ($1, $2, $3, $3)`,
			runID, c.ID, s.startingBalance); err != nil {
			return fmt.Errorf("insert run: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ledger_entries (id, account_id, run_id, kind, amount, ref_id) VALUES ($1, $2, $3, 'deposit', $4, $3)`,
			mustV7(), c.ID, runID, s.startingBalance); err != nil {
			return fmt.Errorf("insert deposit: %w", err)
		}
		return nil
	})
}

// HashSecret is how secrets are stored. Secrets are 256 random bits, so a plain SHA-256 is
// enough; there's nothing to brute-force.
func HashSecret(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// Authenticate returns the account a secret belongs to.
func (s *Store) Authenticate(ctx context.Context, secret string) (uuid.UUID, error) {
	if secret == "" || len(secret) > 128 {
		return uuid.Nil, ErrUnauthorized
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM accounts WHERE secret_hash = $1`, HashSecret(secret)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrUnauthorized
	}
	return id, err
}

// --- read model ---

// Position is an account's position and leverage setting on one coin. A zero size means
// only the leverage setting exists.
type Position struct {
	Coin           string          `json:"coin"`
	Size           decimal.Decimal `json:"size"`
	EntryPx        decimal.Decimal `json:"entryPx"`
	Leverage       int             `json:"leverage"`
	MarginMode     string          `json:"marginMode"`
	IsolatedMargin decimal.Decimal `json:"isolatedMargin"`
}

type Order struct {
	ID         uuid.UUID           `json:"id"`
	Coin       string              `json:"coin"`
	Side       string              `json:"side"`
	Type       string              `json:"type"`
	Px         decimal.NullDecimal `json:"px"`
	TriggerPx  decimal.NullDecimal `json:"triggerPx"`
	Sz         decimal.Decimal     `json:"sz"`
	FilledSz   decimal.Decimal     `json:"filledSz"`
	TIF        *string             `json:"tif"`
	ReduceOnly bool                `json:"reduceOnly"`
	// ParentOrderID links a TP/SL to the entry order it was attached to; it only activates
	// once the parent has filled. Orders sharing an OcoGroup cancel each other.
	ParentOrderID *uuid.UUID `json:"parentOrderId"`
	OcoGroup      *uuid.UUID `json:"ocoGroup"`
	Status        string     `json:"status"`
	StatusReason  *string    `json:"statusReason"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

type Fill struct {
	ID            uuid.UUID       `json:"id"`
	OrderID       *uuid.UUID      `json:"orderId"`
	Coin          string          `json:"coin"`
	Side          string          `json:"side"`
	Px            decimal.Decimal `json:"px"`
	Sz            decimal.Decimal `json:"sz"`
	Fee           decimal.Decimal `json:"fee"`
	Liquidity     string          `json:"liquidity"`
	ClosedPnl     decimal.Decimal `json:"closedPnl"`
	IsLiquidation bool            `json:"isLiquidation"`
	CreatedAt     time.Time       `json:"createdAt"`
	Note          string          `json:"note"` // the trader's private journal note; "" if none
}

type FundingPayment struct {
	Coin        string          `json:"coin"`
	FundingHour time.Time       `json:"fundingHour"`
	Size        decimal.Decimal `json:"size"`
	Rate        decimal.Decimal `json:"rate"`
	OraclePx    decimal.Decimal `json:"oraclePx"`
	Amount      decimal.Decimal `json:"amount"`
}

type Suspension struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// Account is an account's current trading state.
type Account struct {
	ID               uuid.UUID  `json:"accountId"`
	Handle           string     `json:"handle"`
	CreatedAt        time.Time  `json:"createdAt"`
	LeaderboardOptIn bool       `json:"leaderboardOptIn"`
	LastResetAt      *time.Time `json:"lastResetAt"`
	Resets           int        `json:"resets"`
	// LeaderboardHidden means a moderator took the account off the leaderboard; opting in
	// doesn't bring it back.
	LeaderboardHidden bool `json:"leaderboardHidden"`
	// Suspension is set while a moderator has suspended the account: it can close
	// positions but not open new ones.
	Suspension *Suspension `json:"suspension"`
	// DegenMode allows leverage above Hyperliquid's cap, up to engine.MaxDegenLeverage.
	DegenMode bool `json:"degenMode"`
	// DegenAt is when a fill first grew a position above the cap; nil if never.
	DegenAt *time.Time `json:"degenAt"`

	RunID           uuid.UUID       `json:"runId"`
	StartingBalance decimal.Decimal `json:"startingBalance"` // of the current run
	Balance         decimal.Decimal `json:"balance"`
	// PriorPnl is the PnL of earlier runs (before resets), which still counts toward the
	// leaderboard: each ended run's final balance plus the PnL locked at its reset, minus
	// what it started with.
	PriorPnl decimal.Decimal `json:"priorPnl"`

	Positions  []Position `json:"positions"`
	OpenOrders []Order    `json:"openOrders"`
}

const orderColumns = `id, coin, side, type, px, trigger_px, sz, filled_sz, tif, reduce_only, parent_order_id, oco_group,
	status, status_reason, created_at, updated_at`

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.Coin, &o.Side, &o.Type, &o.Px, &o.TriggerPx, &o.Sz, &o.FilledSz, &o.TIF,
		&o.ReduceOnly, &o.ParentOrderID, &o.OcoGroup, &o.Status, &o.StatusReason, &o.CreatedAt, &o.UpdatedAt)
	return o, err
}

// Load reads an account's current state. q may be a transaction.
func Load(ctx context.Context, q Querier, id uuid.UUID) (Account, error) {
	a := Account{ID: id, Positions: []Position{}, OpenOrders: []Order{}}
	var suspendedAt *time.Time
	var suspendReason string
	err := q.QueryRow(ctx, `
		SELECT a.handle, a.created_at, a.leaderboard_opt_in, a.last_reset_at,
			a.leaderboard_banned, a.suspended_at, coalesce(a.suspend_reason, ''),
			a.degen_mode, a.degen_at,
			r.id, r.starting_balance, r.balance,
			coalesce((SELECT sum(p.balance + p.locked_upnl - p.starting_balance) FROM runs p
				WHERE p.account_id = a.id AND p.id <> r.id), 0),
			(SELECT count(*) - 1 FROM runs p WHERE p.account_id = a.id)
		FROM accounts a JOIN runs r ON r.id = a.current_run_id
		WHERE a.id = $1`, id).Scan(&a.Handle, &a.CreatedAt, &a.LeaderboardOptIn, &a.LastResetAt,
		&a.LeaderboardHidden, &suspendedAt, &suspendReason,
		&a.DegenMode, &a.DegenAt,
		&a.RunID, &a.StartingBalance, &a.Balance, &a.PriorPnl, &a.Resets)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("load account: %w", err)
	}
	if suspendedAt != nil {
		a.Suspension = &Suspension{At: *suspendedAt, Reason: suspendReason}
	}

	rows, err := q.Query(ctx, `
		SELECT coin, size, coalesce(entry_px, 0), leverage, margin_mode, isolated_margin
		FROM positions WHERE account_id = $1 ORDER BY coin`, id)
	if err != nil {
		return Account{}, fmt.Errorf("load positions: %w", err)
	}
	a.Positions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Position, error) {
		var p Position
		err := r.Scan(&p.Coin, &p.Size, &p.EntryPx, &p.Leverage, &p.MarginMode, &p.IsolatedMargin)
		return p, err
	})
	if err != nil {
		return Account{}, fmt.Errorf("load positions: %w", err)
	}

	rows, err = q.Query(ctx, `SELECT `+orderColumns+` FROM orders
		WHERE account_id = $1 AND status IN ('open', 'triggered') ORDER BY created_at`, id)
	if err != nil {
		return Account{}, fmt.Errorf("load orders: %w", err)
	}
	a.OpenOrders, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Order, error) { return scanOrder(r) })
	if err != nil {
		return Account{}, fmt.Errorf("load orders: %w", err)
	}
	return a, nil
}

func (s *Store) Load(ctx context.Context, id uuid.UUID) (Account, error) {
	return Load(ctx, s.pool, id)
}

// OrderHistory returns the account's most recent closed (filled or cancelled) orders.
func (s *Store) OrderHistory(ctx context.Context, id uuid.UUID, limit int) ([]Order, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+orderColumns+` FROM orders
		WHERE account_id = $1 AND status IN ('filled', 'cancelled') ORDER BY updated_at DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Order, error) { return scanOrder(r) })
}

// Fills returns the account's most recent fills.
func (s *Store) Fills(ctx context.Context, id uuid.UUID, limit int) ([]Fill, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.id, f.order_id, f.coin, f.side, f.px, f.sz, f.fee, f.liquidity, f.closed_pnl, f.is_liquidation,
			f.created_at, coalesce(n.note, '')
		FROM fills f LEFT JOIN fill_notes n ON n.fill_id = f.id
		WHERE f.account_id = $1 ORDER BY f.created_at DESC, f.id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Fill, error) {
		var f Fill
		err := r.Scan(&f.ID, &f.OrderID, &f.Coin, &f.Side, &f.Px, &f.Sz, &f.Fee, &f.Liquidity,
			&f.ClosedPnl, &f.IsLiquidation, &f.CreatedAt, &f.Note)
		return f, err
	})
}

// Funding returns the account's most recent funding payments.
func (s *Store) Funding(ctx context.Context, id uuid.UUID, limit int) ([]FundingPayment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT coin, funding_hour, size, rate, oracle_px, amount
		FROM funding_payments WHERE account_id = $1 ORDER BY funding_hour DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FundingPayment, error) {
		var f FundingPayment
		err := r.Scan(&f.Coin, &f.FundingHour, &f.Size, &f.Rate, &f.OraclePx, &f.Amount)
		return f, err
	})
}

// mustV7 returns a time-ordered UUID. NewV7 only fails if the system's random source does.
func mustV7() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

// NewID returns a new time-ordered ID for any row.
func NewID() uuid.UUID { return mustV7() }

// --- profile ---

var (
	ErrHandleTaken   = errors.New("handle taken")
	ErrHandleInvalid = errors.New("invalid handle")
	// ErrHandleOffensive: the handle contains a slur or obscenity (profanity.go).
	ErrHandleOffensive = errors.New("offensive handle")
	// ErrSuspended: suspended accounts can't change their handle, so they can't undo a
	// moderator's rename.
	ErrSuspended    = errors.New("account suspended")
	handlePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,23}$`)
	reservedHandles = []string{"admin", "administrator", "support", "moderator", "staff", "official",
		"paperperps", "hyperliquid", "system", "root"}
)

// ValidateHandle checks a public display name: 3–24 lowercase letters, digits, - or _,
// starting with a letter or digit, and not impersonating staff or the exchange.
func ValidateHandle(h string) error {
	if !handlePattern.MatchString(h) {
		return ErrHandleInvalid
	}
	for _, r := range reservedHandles {
		if strings.Contains(h, r) {
			return ErrHandleInvalid
		}
	}
	if Offensive(h) {
		return ErrHandleOffensive
	}
	return nil
}

// Profile is the editable part of an account. Nil fields are left unchanged.
type Profile struct {
	Handle           *string `json:"handle"`
	LeaderboardOptIn *bool   `json:"leaderboardOptIn"`
}

func (s *Store) UpdateProfile(ctx context.Context, id uuid.UUID, p Profile) error {
	if p.Handle != nil {
		h := strings.ToLower(strings.TrimSpace(*p.Handle))
		if err := ValidateHandle(h); err != nil {
			return err
		}
		p.Handle = &h
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE accounts SET handle = coalesce($2, handle), leaderboard_opt_in = coalesce($3, leaderboard_opt_in)
		WHERE id = $1 AND ($2::text IS NULL OR suspended_at IS NULL)`, id, p.Handle, p.LeaderboardOptIn)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrHandleTaken
	}
	if err == nil && tag.RowsAffected() == 0 {
		return ErrSuspended
	}
	return err
}

// --- equity history ---

type EquityPoint struct {
	Time          time.Time       `json:"t"`
	Equity        decimal.Decimal `json:"equity"`
	CumulativePnl decimal.Decimal `json:"cumulativePnl"`
}

// Equity returns the account's equity snapshots, oldest first: every snapshot from the
// last 48 hours and one per hour before that, back 30 days.
func (s *Store) Equity(ctx context.Context, id uuid.UUID, now time.Time) ([]EquityPoint, error) {
	recent := now.Add(-48 * time.Hour)
	rows, err := s.pool.Query(ctx, `
		SELECT ts, equity, cumulative_pnl FROM (
			SELECT DISTINCT ON (date_trunc('hour', ts)) ts, equity, cumulative_pnl
			FROM equity_snapshots
			WHERE account_id = $1 AND ts >= $2 AND ts < $3
			ORDER BY date_trunc('hour', ts), ts DESC
		) hourly
		UNION ALL
		SELECT ts, equity, cumulative_pnl FROM equity_snapshots WHERE account_id = $1 AND ts >= $3
		ORDER BY ts`, id, now.Add(-30*24*time.Hour), recent)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (EquityPoint, error) {
		var p EquityPoint
		err := r.Scan(&p.Time, &p.Equity, &p.CumulativePnl)
		return p, err
	})
}

// OpenPositionCoins lists every coin anyone holds a position in.
func (s *Store) OpenPositionCoins(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT p.coin FROM positions p
		JOIN accounts a ON a.id = p.account_id AND a.current_run_id = p.run_id
		WHERE p.size <> 0 ORDER BY p.coin`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
