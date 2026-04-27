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

// TestConcurrent_BuyAndSell_PreservesInvariant runs 100 buys (each into a
// distinct wallet) interleaved with 100 sells (all from w1) against a single
// stock. The total amount of that stock in the system must stay constant -
// every successful operation moves one unit between two rows but never
// creates or destroys any. The audit log must mirror exactly the set of
// successful operations.
func TestConcurrent_BuyAndSell_PreservesInvariant(t *testing.T) {
	cleanDB(t)
	repo := storage.NewPostgres(pool)
	ctx := context.Background()

	const buys, sells = 100, 100
	const initialBank, initialWallet = int64(100), int64(100)
	const expectedTotal = initialBank + initialWallet

	require.NoError(t, repo.SetBankStocks(ctx, []domain.Stock{{Name: "GOOG", Quantity: initialBank}}))
	_, err := pool.Exec(ctx, `INSERT INTO wallets (id) VALUES ('w1')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO wallet_stocks (wallet_id, stock_name, quantity) VALUES ('w1', 'GOOG', $1)`,
		initialWallet)
	require.NoError(t, err)

	type result struct {
		op  string
		err error
	}
	resultsCh := make(chan result, buys+sells)
	var wg sync.WaitGroup

	for i := 0; i < buys; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := repo.BuyStock(ctx, fmt.Sprintf("buyer-%d", id), "GOOG")
			resultsCh <- result{op: "buy", err: err}
		}(i)
	}
	for i := 0; i < sells; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := repo.SellStock(ctx, "w1", "GOOG")
			resultsCh <- result{op: "sell", err: err}
		}()
	}
	wg.Wait()
	close(resultsCh)

	var buyOK, sellOK int
	var firstUnexpected error
	for r := range resultsCh {
		if r.err == nil {
			if r.op == "buy" {
				buyOK++
			} else {
				sellOK++
			}
			continue
		}
		if firstUnexpected == nil {
			firstUnexpected = fmt.Errorf("unexpected %s error: %w", r.op, r.err)
		}
	}
	require.NoError(t, firstUnexpected)
	require.Equal(t, buys, buyOK, "every buy should succeed - sells keep replenishing the bank")
	require.Equal(t, sells, sellOK, "every sell should succeed - w1 starts with enough holdings")

	var bankQty, walletSum int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT quantity FROM bank_stocks WHERE name = 'GOOG'`).Scan(&bankQty))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity), 0) FROM wallet_stocks WHERE stock_name = 'GOOG'`).Scan(&walletSum))
	require.Equal(t, expectedTotal, bankQty+walletSum,
		"total stock in the system must remain %d (bank=%d, wallets=%d)", expectedTotal, bankQty, walletSum)

	var totalAudit, buyAudit, sellAudit int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE stock_name = 'GOOG'`).Scan(&totalAudit))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE operation = 'BUY' AND stock_name = 'GOOG'`).Scan(&buyAudit))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE operation = 'SELL' AND stock_name = 'GOOG'`).Scan(&sellAudit))
	require.Equal(t, int64(buyOK+sellOK), totalAudit, "audit count must equal successful op count")
	require.Equal(t, int64(buyOK), buyAudit)
	require.Equal(t, int64(sellOK), sellAudit)
}
