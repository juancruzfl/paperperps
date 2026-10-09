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
	Coin       string          `json:"coin"`
	Size       decimal.Decimal `json:"size"`
	EntryPx    decimal.Decimal `json:"entryPx"`
	Leverage   int             `json:"leverage"`
	MarginMode string          `json:"marginMode"`
}

type Order struct {
	ID           uuid.UUID           `json:"id"`
	Coin         string              `json:"coin"`
	Side         string              `json:"side"`
	Type         string              `json:"type"`
	Px           decimal.NullDecimal `json:"px"`
	Sz           decimal.Decimal     `json:"sz"`
	FilledSz     decimal.Decimal     `json:"filledSz"`
	TIF          *string             `json:"tif"`
	ReduceOnly   bool                `json:"reduceOnly"`
	Status       string              `json:"status"`
	StatusReason *string             `json:"statusReason"`
	CreatedAt    time.Time           `json:"createdAt"`
	UpdatedAt    time.Time           `json:"updatedAt"`
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
}

type FundingPayment struct {
	Coin        string          `json:"coin"`
	FundingHour time.Time       `json:"fundingHour"`
	Size        decimal.Decimal `json:"size"`
	Rate        decimal.Decimal `json:"rate"`
	OraclePx    decimal.Decimal `json:"oraclePx"`
	Amount      decimal.Decimal `json:"amount"`
}

// Account is an account's current trading state.
type Account struct {
	ID         uuid.UUID       `json:"accountId"`
	Handle     string          `json:"handle"`
	CreatedAt  time.Time       `json:"createdAt"`
	RunID      uuid.UUID       `json:"runId"`
	Balance    decimal.Decimal `json:"balance"`
	Positions  []Position      `json:"positions"`
	OpenOrders []Order         `json:"openOrders"`
}

const orderColumns = `id, coin, side, type, px, sz, filled_sz, tif, reduce_only, status, status_reason, created_at, updated_at`

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.Coin, &o.Side, &o.Type, &o.Px, &o.Sz, &o.FilledSz, &o.TIF,
		&o.ReduceOnly, &o.Status, &o.StatusReason, &o.CreatedAt, &o.UpdatedAt)
	return o, err
}

// Load reads an account's current state. q may be a transaction.
func Load(ctx context.Context, q Querier, id uuid.UUID) (Account, error) {
	a := Account{ID: id, Positions: []Position{}, OpenOrders: []Order{}}
	err := q.QueryRow(ctx, `
		SELECT a.handle, a.created_at, r.id, r.balance
		FROM accounts a JOIN runs r ON r.id = a.current_run_id
		WHERE a.id = $1`, id).Scan(&a.Handle, &a.CreatedAt, &a.RunID, &a.Balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("load account: %w", err)
	}

	rows, err := q.Query(ctx, `
		SELECT coin, size, coalesce(entry_px, 0), leverage, margin_mode
		FROM positions WHERE account_id = $1 ORDER BY coin`, id)
	if err != nil {
		return Account{}, fmt.Errorf("load positions: %w", err)
	}
	a.Positions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Position, error) {
		var p Position
		err := r.Scan(&p.Coin, &p.Size, &p.EntryPx, &p.Leverage, &p.MarginMode)
		return p, err
	})
	if err != nil {
		return Account{}, fmt.Errorf("load positions: %w", err)
	}

	rows, err = q.Query(ctx, `SELECT `+orderColumns+` FROM orders
		WHERE account_id = $1 AND status = 'open' ORDER BY created_at`, id)
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
		WHERE account_id = $1 AND status <> 'open' ORDER BY updated_at DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Order, error) { return scanOrder(r) })
}

// Fills returns the account's most recent fills.
func (s *Store) Fills(ctx context.Context, id uuid.UUID, limit int) ([]Fill, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, order_id, coin, side, px, sz, fee, liquidity, closed_pnl, is_liquidation, created_at
		FROM fills WHERE account_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Fill, error) {
		var f Fill
		err := r.Scan(&f.ID, &f.OrderID, &f.Coin, &f.Side, &f.Px, &f.Sz, &f.Fee, &f.Liquidity,
			&f.ClosedPnl, &f.IsLiquidation, &f.CreatedAt)
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
