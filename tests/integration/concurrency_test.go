//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Macszym/stock-market-simulator/internal/domain"
	"github.com/Macszym/stock-market-simulator/internal/storage"
)

// TestConcurrent_BuyStock_RaceForLimitedBank fires 100 concurrent BuyStock
// calls against a bank that holds 50 units. Postgres must serialize the
// per-row UPDATE so that exactly 50 succeed, the bank ends at 0, the wallets
// collectively own 50 units and the audit log carries exactly 50 entries.
// Any other outcome means the atomicity guarantees broke under contention.
func TestConcurrent_BuyStock_RaceForLimitedBank(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)
	ctx := context.Background()

	const initialQty = 50
	const goroutines = 100
	require.NoError(t, repo.SetBankStocks(ctx, []domain.Stock{{Name: "AAPL", Quantity: initialQty}}))

	errCh := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			errCh <- repo.BuyStock(ctx, fmt.Sprintf("wallet-%d", id), "AAPL")
		}(i)
	}
	wg.Wait()
	close(errCh)

	var ok, depleted int
	var firstUnexpected error
	for err := range errCh {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrInsufficientBankStock):
			depleted++
		default:
			if firstUnexpected == nil {
				firstUnexpected = err
			}
		}
	}
	require.NoError(t, firstUnexpected, "unexpected error during concurrent buys")
	require.Equal(t, initialQty, ok, "exactly %d buys must succeed", initialQty)
	require.Equal(t, goroutines-initialQty, depleted, "the rest must fail with insufficient bank stock")

	var bankQty int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT quantity FROM bank_stocks WHERE name = 'AAPL'`).Scan(&bankQty))
	require.Equal(t, int64(0), bankQty, "bank must be exhausted")

	var walletSum int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity), 0) FROM wallet_stocks WHERE stock_name = 'AAPL'`).Scan(&walletSum))
	require.Equal(t, int64(initialQty), walletSum, "wallets together must hold every unit released by the bank")

	var auditCount int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE operation = 'BUY' AND stock_name = 'AAPL'`).Scan(&auditCount))
	require.Equal(t, int64(initialQty), auditCount, "audit log must record every successful buy and only those")
}
