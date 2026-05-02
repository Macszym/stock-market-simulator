//go:build integration

// Package integration_test exercises the storage layer against a real
// Postgres instance launched via testcontainers-go. A single container is
// shared across the test binary; each test calls cleanDB() to reset state.
package integration_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Macszym/stock-market-simulator/internal/domain"
	"github.com/Macszym/stock-market-simulator/internal/storage"
	"github.com/Macszym/stock-market-simulator/migrations"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:18.3-alpine",
		postgres.WithDatabase("stocksim"),
		postgres.WithUsername("stocksim"),
		postgres.WithPassword("stocksim"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}

	exitCode := func() int {
		defer func() {
			if err := container.Terminate(ctx); err != nil {
				log.Printf("terminate container: %v", err)
			}
		}()

		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			log.Printf("connection string: %v", err)
			return 1
		}

		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			log.Printf("init pool: %v", err)
			return 1
		}
		defer pool.Close()

		if err := pool.Ping(ctx); err != nil {
			log.Printf("ping: %v", err)
			return 1
		}

		if err := runMigrations(pool); err != nil {
			log.Printf("migrations: %v", err)
			return 1
		}

		return m.Run()
	}()

	os.Exit(exitCode)
}

func runMigrations(pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// cleanDB resets every table and restarts identity sequences so each test
// starts from a known empty state. wallet_stocks is listed explicitly because
// of its FK to wallets; CASCADE keeps audit_log self-consistent.
func cleanDB(t *testing.T) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`TRUNCATE bank_stocks, wallets, wallet_stocks, audit_log RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
}

func TestPostgres_GetBankStocks_Empty(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)

	stocks, err := repo.GetBankStocks(context.Background())
	require.NoError(t, err)
	require.Empty(t, stocks)
}

func TestPostgres_SetBankStocks_RoundTrip(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)
	ctx := context.Background()

	input := []domain.Stock{
		{Name: "AAPL", Quantity: 100},
		{Name: "MSFT", Quantity: 50},
	}
	require.NoError(t, repo.SetBankStocks(ctx, input))

	out, err := repo.GetBankStocks(ctx)
	require.NoError(t, err)
	// GetBankStocks orders by name, so AAPL comes before MSFT deterministically.
	require.Equal(t, []domain.Stock{
		{Name: "AAPL", Quantity: 100},
		{Name: "MSFT", Quantity: 50},
	}, out)
}

func TestPostgres_SetBankStocks_OverwritesPrevious(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)
	ctx := context.Background()

	require.NoError(t, repo.SetBankStocks(ctx, []domain.Stock{{Name: "OLD", Quantity: 1}}))
	require.NoError(t, repo.SetBankStocks(ctx, []domain.Stock{{Name: "NEW", Quantity: 2}}))

	out, err := repo.GetBankStocks(ctx)
	require.NoError(t, err)
	require.Equal(t, []domain.Stock{{Name: "NEW", Quantity: 2}}, out)
}

func TestPostgres_GetWallet_NotFound(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)

	_, err := repo.GetWallet(context.Background(), "missing")
	require.ErrorIs(t, err, domain.ErrWalletNotFound)
}

func TestPostgres_GetWallet_WithHoldings(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `INSERT INTO wallets (id) VALUES ('w1')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO wallet_stocks (wallet_id, stock_name, quantity) VALUES ('w1', 'AAPL', 5), ('w1', 'MSFT', 3)`)
	require.NoError(t, err)

	wallet, err := repo.GetWallet(ctx, "w1")
	require.NoError(t, err)
	require.Equal(t, "w1", wallet.ID)
	require.Equal(t, []domain.Stock{
		{Name: "AAPL", Quantity: 5},
		{Name: "MSFT", Quantity: 3},
	}, wallet.Stocks)
}

func TestPostgres_GetWallet_ExistsWithoutHoldings(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `INSERT INTO wallets (id) VALUES ('w1')`)
	require.NoError(t, err)

	wallet, err := repo.GetWallet(ctx, "w1")
	require.NoError(t, err)
	require.Equal(t, "w1", wallet.ID)
	require.Empty(t, wallet.Stocks)
}

func TestPostgres_GetWalletStockQuantity_WalletMissing(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)

	_, err := repo.GetWalletStockQuantity(context.Background(), "missing", "AAPL")
	require.ErrorIs(t, err, domain.ErrWalletNotFound)
}

func TestPostgres_GetWalletStockQuantity_HoldingMissing(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `INSERT INTO wallets (id) VALUES ('w1')`)
	require.NoError(t, err)

	qty, err := repo.GetWalletStockQuantity(ctx, "w1", "AAPL")
	require.NoError(t, err)
	require.Equal(t, int64(0), qty)
}

func TestPostgres_GetWalletStockQuantity_HoldingExists(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `INSERT INTO wallets (id) VALUES ('w1')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO wallet_stocks (wallet_id, stock_name, quantity) VALUES ('w1', 'AAPL', 7)`)
	require.NoError(t, err)

	qty, err := repo.GetWalletStockQuantity(ctx, "w1", "AAPL")
	require.NoError(t, err)
	require.Equal(t, int64(7), qty)
}

func TestPostgres_GetAuditLog_Empty(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)

	entries, err := repo.GetAuditLog(context.Background())
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestPostgres_GetAuditLog_OrderedByID(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`INSERT INTO audit_log (operation, wallet_id, stock_name) VALUES ('BUY', 'w1', 'AAPL'), ('SELL', 'w2', 'MSFT')`)
	require.NoError(t, err)

	entries, err := repo.GetAuditLog(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, domain.OperationBuy, entries[0].Operation)
	require.Equal(t, "w1", entries[0].WalletID)
	require.Equal(t, "AAPL", entries[0].StockName)
	require.Equal(t, domain.OperationSell, entries[1].Operation)
	require.Less(t, entries[0].ID, entries[1].ID, "audit log must be ordered by id ascending")
	require.False(t, entries[0].CreatedAt.IsZero(), "created_at must be populated by DB default")
}

func TestPostgres_SellStock_AutoCreatesWalletOnMissing(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)
	ctx := context.Background()

	require.NoError(t, repo.SetBankStocks(ctx, []domain.Stock{{Name: "AAPL", Quantity: 5}}))

	err := repo.SellStock(ctx, "newwallet", "AAPL")
	require.ErrorIs(t, err, domain.ErrInsufficientWalletStock)

	var exists bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM wallets WHERE id = $1)`, "newwallet").Scan(&exists))
	require.True(t, exists, "missing wallet must be auto-created on sell")

	var bankQty int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT quantity FROM bank_stocks WHERE name = 'AAPL'`).Scan(&bankQty))
	require.Equal(t, int64(5), bankQty, "bank quantity must be unchanged on failed sell")

	var auditCount int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log`).Scan(&auditCount))
	require.Equal(t, int64(0), auditCount, "failed sell must not write an audit row")
}
