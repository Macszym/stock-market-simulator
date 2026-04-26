// Package storage provides Postgres-backed persistence for the domain types.
package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Macszym/stock-market-simulator/internal/domain"
)

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func (p *Postgres) GetBankStocks(ctx context.Context) ([]domain.Stock, error) {
	rows, err := p.pool.Query(ctx, `SELECT name, quantity FROM bank_stocks ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query bank_stocks: %w", err)
	}
	defer rows.Close()

	stocks := []domain.Stock{}
	for rows.Next() {
		var s domain.Stock
		if err := rows.Scan(&s.Name, &s.Quantity); err != nil {
			return nil, fmt.Errorf("scan bank_stocks: %w", err)
		}
		stocks = append(stocks, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter bank_stocks: %w", err)
	}
	return stocks, nil
}

// SetBankStocks replaces the entire bank inventory in a single transaction.
// TRUNCATE is preferred over DELETE: it skips per-row triggers, writes less
// WAL, does not require a follow-up VACUUM and naturally matches "overwrite"
// semantics. The transaction guarantees that a crash between TRUNCATE and the
// inserts leaves the bank in its previous state.
func (p *Postgres) SetBankStocks(ctx context.Context, stocks []domain.Stock) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `TRUNCATE bank_stocks`); err != nil {
		return fmt.Errorf("truncate bank_stocks: %w", err)
	}

	for _, s := range stocks {
		if _, err := tx.Exec(ctx,
			`INSERT INTO bank_stocks (name, quantity) VALUES ($1, $2)`,
			s.Name, s.Quantity,
		); err != nil {
			return fmt.Errorf("insert bank_stock %q: %w", s.Name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (p *Postgres) GetWallet(ctx context.Context, id string) (domain.Wallet, error) {
	exists, err := p.walletExists(ctx, id)
	if err != nil {
		return domain.Wallet{}, err
	}
	if !exists {
		return domain.Wallet{}, domain.ErrWalletNotFound
	}

	rows, err := p.pool.Query(ctx,
		`SELECT stock_name, quantity FROM wallet_stocks WHERE wallet_id = $1 ORDER BY stock_name`,
		id,
	)
	if err != nil {
		return domain.Wallet{}, fmt.Errorf("query wallet_stocks %q: %w", id, err)
	}
	defer rows.Close()

	stocks := []domain.Stock{}
	for rows.Next() {
		var s domain.Stock
		if err := rows.Scan(&s.Name, &s.Quantity); err != nil {
			return domain.Wallet{}, fmt.Errorf("scan wallet_stocks: %w", err)
		}
		stocks = append(stocks, s)
	}
	if err := rows.Err(); err != nil {
		return domain.Wallet{}, fmt.Errorf("iter wallet_stocks: %w", err)
	}

	return domain.Wallet{ID: id, Stocks: stocks}, nil
}

func (p *Postgres) GetWalletStockQuantity(ctx context.Context, walletID, stockName string) (int64, error) {
	exists, err := p.walletExists(ctx, walletID)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, domain.ErrWalletNotFound
	}

	var qty int64
	err = p.pool.QueryRow(ctx,
		`SELECT quantity FROM wallet_stocks WHERE wallet_id = $1 AND stock_name = $2`,
		walletID, stockName,
	).Scan(&qty)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("query wallet_stocks %q/%q: %w", walletID, stockName, err)
	}
	return qty, nil
}

func (p *Postgres) GetAuditLog(ctx context.Context) ([]domain.AuditEntry, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT id, operation, wallet_id, stock_name, created_at FROM audit_log ORDER BY id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query audit_log: %w", err)
	}
	defer rows.Close()

	entries := []domain.AuditEntry{}
	for rows.Next() {
		var e domain.AuditEntry
		if err := rows.Scan(&e.ID, &e.Operation, &e.WalletID, &e.StockName, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit_log: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter audit_log: %w", err)
	}
	return entries, nil
}

func (p *Postgres) walletExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := p.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM wallets WHERE id = $1)`,
		id,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query wallet %q: %w", id, err)
	}
	return exists, nil
}
