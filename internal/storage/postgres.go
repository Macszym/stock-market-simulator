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

// BuyStock moves one unit of stockName from the bank into walletID's holdings
// and writes a BUY entry to the audit log, all in a single transaction.
func (p *Postgres) BuyStock(ctx context.Context, walletID, stockName string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the bank row and confirm the stock is a known symbol.
	var bankQty int64
	err = tx.QueryRow(ctx,
		`SELECT quantity FROM bank_stocks WHERE name = $1 FOR UPDATE`,
		stockName,
	).Scan(&bankQty)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrStockNotFound
	}
	if err != nil {
		return fmt.Errorf("lock bank_stocks %q: %w", stockName, err)
	}

	// Atomic decrement guarded by quantity > 0. Zero rows affected means the
	// row exists (we just locked it) but is depleted.
	ct, err := tx.Exec(ctx,
		`UPDATE bank_stocks SET quantity = quantity - 1 WHERE name = $1 AND quantity > 0`,
		stockName,
	)
	if err != nil {
		return fmt.Errorf("decrement bank_stocks %q: %w", stockName, err)
	}
	if ct.RowsAffected() == 0 {
		return domain.ErrInsufficientBankStock
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO wallets (id) VALUES ($1) ON CONFLICT (id) DO NOTHING`,
		walletID,
	); err != nil {
		return fmt.Errorf("upsert wallet %q: %w", walletID, err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO wallet_stocks (wallet_id, stock_name, quantity) VALUES ($1, $2, 1)
		 ON CONFLICT (wallet_id, stock_name) DO UPDATE SET quantity = wallet_stocks.quantity + 1`,
		walletID, stockName,
	); err != nil {
		return fmt.Errorf("upsert wallet_stocks %q/%q: %w", walletID, stockName, err)
	}

	if err := p.auditOp(ctx, tx, domain.OperationBuy, walletID, stockName); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// SellStock returns one unit of stockName from walletID's holdings back to the
// bank and writes a SELL entry to the audit log, all in a single transaction.
// The wallet is auto-created up-front and persists even if the sell itself
// fails, so the wallet creation is not undone by the transaction rollback.
func (p *Postgres) SellStock(ctx context.Context, walletID, stockName string) error {
	if _, err := p.pool.Exec(ctx,
		`INSERT INTO wallets (id) VALUES ($1) ON CONFLICT (id) DO NOTHING`,
		walletID,
	); err != nil {
		return fmt.Errorf("upsert wallet %q: %w", walletID, err)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Confirm the stock is a known symbol and lock its bank row for the
	// upcoming increment.
	var bankQty int64
	err = tx.QueryRow(ctx,
		`SELECT quantity FROM bank_stocks WHERE name = $1 FOR UPDATE`,
		stockName,
	).Scan(&bankQty)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrStockNotFound
	}
	if err != nil {
		return fmt.Errorf("lock bank_stocks %q: %w", stockName, err)
	}

	// Atomic decrement guarded by quantity > 0. Zero rows affected means the
	// wallet has no holding for stockName.
	ct, err := tx.Exec(ctx,
		`UPDATE wallet_stocks SET quantity = quantity - 1 WHERE wallet_id = $1 AND stock_name = $2 AND quantity > 0`,
		walletID, stockName,
	)
	if err != nil {
		return fmt.Errorf("decrement wallet_stocks %q/%q: %w", walletID, stockName, err)
	}
	if ct.RowsAffected() == 0 {
		return domain.ErrInsufficientWalletStock
	}

	if _, err := tx.Exec(ctx,
		`UPDATE bank_stocks SET quantity = quantity + 1 WHERE name = $1`,
		stockName,
	); err != nil {
		return fmt.Errorf("increment bank_stocks %q: %w", stockName, err)
	}

	if err := p.auditOp(ctx, tx, domain.OperationSell, walletID, stockName); err != nil {
		return err
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

// auditOp inserts a row into audit_log inside the caller's transaction. Both
// BuyStock and SellStock call it before Commit so the audit row and the
// state mutation either both persist or both roll back. ADR 0005 covers the
// rationale for keeping audit and state in one transaction.
func (p *Postgres) auditOp(ctx context.Context, tx pgx.Tx, op domain.OperationType, walletID, stockName string) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (operation, wallet_id, stock_name) VALUES ($1, $2, $3)`,
		op, walletID, stockName,
	); err != nil {
		return fmt.Errorf("insert audit_log: %w", err)
	}
	return nil
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
