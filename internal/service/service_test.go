package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Macszym/stock-market-simulator/internal/domain"
)

// fakeRepo is an in-memory Repository for service tests.
type fakeRepo struct {
	bank     map[string]int64
	wallets  map[string]map[string]int64
	auditLog []domain.AuditEntry
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		bank:    map[string]int64{},
		wallets: map[string]map[string]int64{},
	}
}

// Compile-time guard: fakeRepo must satisfy Repository.
var _ Repository = (*fakeRepo)(nil)

func (f *fakeRepo) GetBankStocks(_ context.Context) ([]domain.Stock, error) {
	out := make([]domain.Stock, 0, len(f.bank))
	for name, qty := range f.bank {
		out = append(out, domain.Stock{Name: name, Quantity: qty})
	}
	return out, nil
}

func (f *fakeRepo) SetBankStocks(_ context.Context, stocks []domain.Stock) error {
	f.bank = map[string]int64{}
	for _, st := range stocks {
		f.bank[st.Name] = st.Quantity
	}
	return nil
}

func (f *fakeRepo) GetWallet(_ context.Context, id string) (domain.Wallet, error) {
	holdings, ok := f.wallets[id]
	if !ok {
		return domain.Wallet{}, domain.ErrWalletNotFound
	}
	stocks := make([]domain.Stock, 0, len(holdings))
	for name, qty := range holdings {
		stocks = append(stocks, domain.Stock{Name: name, Quantity: qty})
	}
	return domain.Wallet{ID: id, Stocks: stocks}, nil
}

func (f *fakeRepo) GetWalletStockQuantity(_ context.Context, walletID, stockName string) (int64, error) {
	holdings, ok := f.wallets[walletID]
	if !ok {
		return 0, domain.ErrWalletNotFound
	}
	// Missing stock for an existing wallet returns 0; matches Postgres semantics.
	return holdings[stockName], nil
}

func (f *fakeRepo) GetAuditLog(_ context.Context) ([]domain.AuditEntry, error) {
	return f.auditLog, nil
}

func (f *fakeRepo) BuyStock(_ context.Context, walletID, stockName string) error {
	if _, ok := f.wallets[walletID]; !ok {
		f.wallets[walletID] = map[string]int64{}
	}
	qty, ok := f.bank[stockName]
	if !ok {
		return domain.ErrStockNotFound
	}
	if qty <= 0 {
		return domain.ErrInsufficientBankStock
	}
	f.bank[stockName] = qty - 1
	f.wallets[walletID][stockName]++
	f.auditLog = append(f.auditLog, domain.AuditEntry{
		Operation: domain.OperationBuy,
		WalletID:  walletID,
		StockName: stockName,
	})
	return nil
}

func (f *fakeRepo) SellStock(_ context.Context, walletID, stockName string) error {
	if _, ok := f.wallets[walletID]; !ok {
		f.wallets[walletID] = map[string]int64{}
	}
	if _, ok := f.bank[stockName]; !ok {
		return domain.ErrStockNotFound
	}
	holdings := f.wallets[walletID]
	qty, ok := holdings[stockName]
	if !ok || qty <= 0 {
		return domain.ErrInsufficientWalletStock
	}
	holdings[stockName] = qty - 1
	f.bank[stockName]++
	f.auditLog = append(f.auditLog, domain.AuditEntry{
		Operation: domain.OperationSell,
		WalletID:  walletID,
		StockName: stockName,
	})
	return nil
}

func newTestService(repo Repository) *Service {
	return NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestService_GetBank_Empty(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	stocks, err := svc.GetBank(context.Background())
	require.NoError(t, err)
	require.Empty(t, stocks)
}

func TestService_GetBank_WithStocks(t *testing.T) {
	repo := newFakeRepo()
	repo.bank = map[string]int64{"AAPL": 100}
	svc := newTestService(repo)

	stocks, err := svc.GetBank(context.Background())
	require.NoError(t, err)
	require.Equal(t, []domain.Stock{{Name: "AAPL", Quantity: 100}}, stocks)
}

func TestService_SetBank(t *testing.T) {
	tests := []struct {
		name      string
		input     []domain.Stock
		wantErr   error
		wantState map[string]int64
	}{
		{
			name:      "happy path overwrites bank",
			input:     []domain.Stock{{Name: "AAPL", Quantity: 100}, {Name: "MSFT", Quantity: 50}},
			wantState: map[string]int64{"AAPL": 100, "MSFT": 50},
		},
		{
			name:      "empty input clears bank",
			input:     []domain.Stock{},
			wantState: map[string]int64{},
		},
		{
			name:    "empty name rejected",
			input:   []domain.Stock{{Name: "", Quantity: 1}},
			wantErr: domain.ErrEmptyStockName,
		},
		{
			name:    "negative quantity rejected",
			input:   []domain.Stock{{Name: "AAPL", Quantity: -1}},
			wantErr: domain.ErrInvalidQuantity,
		},
		{
			name:    "duplicate name rejected",
			input:   []domain.Stock{{Name: "AAPL", Quantity: 1}, {Name: "AAPL", Quantity: 2}},
			wantErr: domain.ErrDuplicateStockName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo()
			repo.bank = map[string]int64{"OLD": 1} // pre-existing state to verify it does not survive a happy path
			svc := newTestService(repo)

			err := svc.SetBank(context.Background(), tt.input)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Equal(t, map[string]int64{"OLD": 1}, repo.bank, "validation failure must not touch the repository")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantState, repo.bank)
		})
	}
}

func TestService_GetWallet_NotFound(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	_, err := svc.GetWallet(context.Background(), "missing")
	require.ErrorIs(t, err, domain.ErrWalletNotFound)
}

func TestService_GetWallet_HappyPath(t *testing.T) {
	repo := newFakeRepo()
	repo.wallets["w1"] = map[string]int64{"AAPL": 5}
	svc := newTestService(repo)

	w, err := svc.GetWallet(context.Background(), "w1")
	require.NoError(t, err)
	require.Equal(t, "w1", w.ID)
	require.Equal(t, []domain.Stock{{Name: "AAPL", Quantity: 5}}, w.Stocks)
}

func TestService_GetWalletStockQuantity_NotFound(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	_, err := svc.GetWalletStockQuantity(context.Background(), "missing", "AAPL")
	require.ErrorIs(t, err, domain.ErrWalletNotFound)
}

func TestService_GetWalletStockQuantity_ExistingWalletNoHolding(t *testing.T) {
	repo := newFakeRepo()
	repo.wallets["w1"] = map[string]int64{}
	svc := newTestService(repo)

	qty, err := svc.GetWalletStockQuantity(context.Background(), "w1", "AAPL")
	require.NoError(t, err)
	require.Equal(t, int64(0), qty)
}

func TestService_GetWalletStockQuantity_WithHolding(t *testing.T) {
	repo := newFakeRepo()
	repo.wallets["w1"] = map[string]int64{"AAPL": 7}
	svc := newTestService(repo)

	qty, err := svc.GetWalletStockQuantity(context.Background(), "w1", "AAPL")
	require.NoError(t, err)
	require.Equal(t, int64(7), qty)
}

func TestService_GetAuditLog_Empty(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	entries, err := svc.GetAuditLog(context.Background())
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestService_GetAuditLog_WithEntries(t *testing.T) {
	repo := newFakeRepo()
	repo.auditLog = []domain.AuditEntry{
		{ID: 1, Operation: domain.OperationBuy, WalletID: "w1", StockName: "AAPL"},
	}
	svc := newTestService(repo)

	entries, err := svc.GetAuditLog(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, domain.OperationBuy, entries[0].Operation)
}

func TestService_BuyStock_AutoCreatesWallet(t *testing.T) {
	repo := newFakeRepo()
	repo.bank = map[string]int64{"AAPL": 5}
	svc := newTestService(repo)

	err := svc.BuyStock(context.Background(), "w1", "AAPL")
	require.NoError(t, err)
	require.Equal(t, int64(4), repo.bank["AAPL"])
	require.Equal(t, int64(1), repo.wallets["w1"]["AAPL"])
	require.Len(t, repo.auditLog, 1)
	require.Equal(t, domain.OperationBuy, repo.auditLog[0].Operation)
	require.Equal(t, "w1", repo.auditLog[0].WalletID)
	require.Equal(t, "AAPL", repo.auditLog[0].StockName)
}

func TestService_BuyStock_IncrementsExistingHolding(t *testing.T) {
	repo := newFakeRepo()
	repo.bank = map[string]int64{"AAPL": 5}
	repo.wallets["w1"] = map[string]int64{"AAPL": 2}
	svc := newTestService(repo)

	err := svc.BuyStock(context.Background(), "w1", "AAPL")
	require.NoError(t, err)
	require.Equal(t, int64(4), repo.bank["AAPL"])
	require.Equal(t, int64(3), repo.wallets["w1"]["AAPL"])
}

func TestService_BuyStock_StockNotFound(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	err := svc.BuyStock(context.Background(), "w1", "AAPL")
	require.ErrorIs(t, err, domain.ErrStockNotFound)
	require.Contains(t, repo.wallets, "w1", "wallet must be auto-created up-front, even on failure")
	require.Empty(t, repo.wallets["w1"], "wallet has no holdings on failed buy")
	require.Empty(t, repo.auditLog, "no audit entry on failure")
}

func TestService_BuyStock_InsufficientBankStock(t *testing.T) {
	repo := newFakeRepo()
	repo.bank = map[string]int64{"AAPL": 0}
	svc := newTestService(repo)

	err := svc.BuyStock(context.Background(), "w1", "AAPL")
	require.ErrorIs(t, err, domain.ErrInsufficientBankStock)
	require.Equal(t, int64(0), repo.bank["AAPL"])
	require.Contains(t, repo.wallets, "w1", "wallet must be auto-created up-front, even on failure")
	require.Empty(t, repo.wallets["w1"], "wallet has no holdings on failed buy")
	require.Empty(t, repo.auditLog)
}

func TestService_SellStock_HappyPath(t *testing.T) {
	repo := newFakeRepo()
	repo.bank = map[string]int64{"AAPL": 5}
	repo.wallets["w1"] = map[string]int64{"AAPL": 3}
	svc := newTestService(repo)

	err := svc.SellStock(context.Background(), "w1", "AAPL")
	require.NoError(t, err)
	require.Equal(t, int64(6), repo.bank["AAPL"])
	require.Equal(t, int64(2), repo.wallets["w1"]["AAPL"])
	require.Len(t, repo.auditLog, 1)
	require.Equal(t, domain.OperationSell, repo.auditLog[0].Operation)
}

// 404 (stock not in bank) wins over 400 (wallet would be sufficient).
func TestService_SellStock_StockNotFoundWinsOverWalletState(t *testing.T) {
	repo := newFakeRepo()
	repo.wallets["w1"] = map[string]int64{"AAPL": 3}
	svc := newTestService(repo)

	err := svc.SellStock(context.Background(), "w1", "AAPL")
	require.ErrorIs(t, err, domain.ErrStockNotFound)
	require.Equal(t, int64(3), repo.wallets["w1"]["AAPL"], "holding untouched")
	require.Empty(t, repo.auditLog)
}

func TestService_SellStock_MissingWallet(t *testing.T) {
	repo := newFakeRepo()
	repo.bank = map[string]int64{"AAPL": 5}
	svc := newTestService(repo)

	err := svc.SellStock(context.Background(), "missing", "AAPL")
	require.ErrorIs(t, err, domain.ErrInsufficientWalletStock)
	require.Equal(t, int64(5), repo.bank["AAPL"], "bank unchanged")
	require.Empty(t, repo.auditLog)
	require.Contains(t, repo.wallets, "missing", "missing wallet should be auto-created")
}

func TestService_SellStock_ExistingWalletNoHolding(t *testing.T) {
	repo := newFakeRepo()
	repo.bank = map[string]int64{"AAPL": 5}
	repo.wallets["w1"] = map[string]int64{}
	svc := newTestService(repo)

	err := svc.SellStock(context.Background(), "w1", "AAPL")
	require.ErrorIs(t, err, domain.ErrInsufficientWalletStock)
}

func TestService_SellStock_ZeroHolding(t *testing.T) {
	repo := newFakeRepo()
	repo.bank = map[string]int64{"AAPL": 5}
	repo.wallets["w1"] = map[string]int64{"AAPL": 0}
	svc := newTestService(repo)

	err := svc.SellStock(context.Background(), "w1", "AAPL")
	require.ErrorIs(t, err, domain.ErrInsufficientWalletStock)
}
