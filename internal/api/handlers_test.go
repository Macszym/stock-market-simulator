package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Macszym/stock-market-simulator/internal/domain"
)

// handleHealthz does not touch the service, so a Server with only the mux
// configured is enough to drive the request through ServeHTTP and routes().
func newServerForHealthzTests() *Server {
	s := &Server{mux: http.NewServeMux()}
	s.routes()
	return s
}

// fakeService is an in-memory ServiceAPI for handler tests. It backs the
// happy paths from real maps and exposes per-method error fields so a test
// can drive a single handler into its 5xx branch without breaking the rest.
type fakeService struct {
	bank     map[string]int64
	wallets  map[string]map[string]int64
	auditLog []domain.AuditEntry

	bankErr        error
	setBankErr     error
	walletErr      error
	walletStockErr error
	auditLogErr    error
	buyErr         error
	sellErr        error
}

var _ ServiceAPI = (*fakeService)(nil)

func newFakeService() *fakeService {
	return &fakeService{
		bank:    map[string]int64{},
		wallets: map[string]map[string]int64{},
	}
}

func (f *fakeService) GetBank(_ context.Context) ([]domain.Stock, error) {
	if f.bankErr != nil {
		return nil, f.bankErr
	}
	out := make([]domain.Stock, 0, len(f.bank))
	for name, qty := range f.bank {
		out = append(out, domain.Stock{Name: name, Quantity: qty})
	}
	return out, nil
}

func (f *fakeService) SetBank(_ context.Context, stocks []domain.Stock) error {
	if f.setBankErr != nil {
		return f.setBankErr
	}
	seen := map[string]bool{}
	for _, st := range stocks {
		if st.Name == "" {
			return domain.ErrEmptyStockName
		}
		if st.Quantity < 0 {
			return domain.ErrInvalidQuantity
		}
		if seen[st.Name] {
			return domain.ErrDuplicateStockName
		}
		seen[st.Name] = true
	}
	f.bank = map[string]int64{}
	for _, st := range stocks {
		f.bank[st.Name] = st.Quantity
	}
	return nil
}

func (f *fakeService) GetWallet(_ context.Context, id string) (domain.Wallet, error) {
	if f.walletErr != nil {
		return domain.Wallet{}, f.walletErr
	}
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

func (f *fakeService) GetWalletStockQuantity(_ context.Context, walletID, stockName string) (int64, error) {
	if f.walletStockErr != nil {
		return 0, f.walletStockErr
	}
	holdings, ok := f.wallets[walletID]
	if !ok {
		return 0, domain.ErrWalletNotFound
	}
	return holdings[stockName], nil
}

func (f *fakeService) GetAuditLog(_ context.Context) ([]domain.AuditEntry, error) {
	if f.auditLogErr != nil {
		return nil, f.auditLogErr
	}
	return f.auditLog, nil
}

func (f *fakeService) BuyStock(_ context.Context, walletID, stockName string) error {
	if f.buyErr != nil {
		return f.buyErr
	}
	qty, ok := f.bank[stockName]
	if !ok {
		return domain.ErrStockNotFound
	}
	if qty <= 0 {
		return domain.ErrInsufficientBankStock
	}
	f.bank[stockName] = qty - 1
	if _, ok := f.wallets[walletID]; !ok {
		f.wallets[walletID] = map[string]int64{}
	}
	f.wallets[walletID][stockName]++
	f.auditLog = append(f.auditLog, domain.AuditEntry{
		Operation: domain.OperationBuy, WalletID: walletID, StockName: stockName,
	})
	return nil
}

func (f *fakeService) SellStock(_ context.Context, walletID, stockName string) error {
	if f.sellErr != nil {
		return f.sellErr
	}
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
		Operation: domain.OperationSell, WalletID: walletID, StockName: stockName,
	})
	return nil
}

func newServerWithFake(svc ServiceAPI, chaos func()) *Server {
	if chaos == nil {
		chaos = func() {}
	}
	return NewServer(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), chaos)
}

func TestServeHTTP_HealthzReturnsOK(t *testing.T) {
	s := newServerForHealthzTests()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var got map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, map[string]string{"status": "ok"}, got)
}

func TestToLogResponse(t *testing.T) {
	tests := []struct {
		name string
		in   []domain.AuditEntry
		want logResponse
	}{
		{
			name: "empty input yields empty (non-nil) slice",
			in:   nil,
			want: logResponse{Log: []auditEntryDTO{}},
		},
		{
			name: "buy entry is lowercased",
			in: []domain.AuditEntry{
				{Operation: domain.OperationBuy, WalletID: "w1", StockName: "AAPL"},
			},
			want: logResponse{Log: []auditEntryDTO{
				{Type: "buy", WalletID: "w1", StockName: "AAPL"},
			}},
		},
		{
			name: "mixed buy and sell preserve order and case",
			in: []domain.AuditEntry{
				{Operation: domain.OperationBuy, WalletID: "w1", StockName: "AAPL"},
				{Operation: domain.OperationSell, WalletID: "w2", StockName: "GOOG"},
			},
			want: logResponse{Log: []auditEntryDTO{
				{Type: "buy", WalletID: "w1", StockName: "AAPL"},
				{Type: "sell", WalletID: "w2", StockName: "GOOG"},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toLogResponse(tt.in)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestHandleGetStocks_EmptyBank(t *testing.T) {
	s := newServerWithFake(newFakeService(), nil)

	req := httptest.NewRequest(http.MethodGet, "/stocks", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got stocksResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Empty(t, got.Stocks)
}

func TestHandleGetStocks_PopulatedBank(t *testing.T) {
	fake := newFakeService()
	fake.bank["AAPL"] = 100
	s := newServerWithFake(fake, nil)

	req := httptest.NewRequest(http.MethodGet, "/stocks", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got stocksResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, []domain.Stock{{Name: "AAPL", Quantity: 100}}, got.Stocks)
}

func TestHandleGetStocks_RepoErrorMapsTo500(t *testing.T) {
	fake := newFakeService()
	fake.bankErr = errors.New("connection refused")
	s := newServerWithFake(fake, nil)

	req := httptest.NewRequest(http.MethodGet, "/stocks", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	var got errorResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "INTERNAL_ERROR", got.Code)
}

func TestHandleSetStocks_HappyPath(t *testing.T) {
	fake := newFakeService()
	s := newServerWithFake(fake, nil)

	body := bytes.NewBufferString(`{"stocks":[{"name":"AAPL","quantity":100},{"name":"MSFT","quantity":50}]}`)
	req := httptest.NewRequest(http.MethodPost, "/stocks", body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, int64(100), fake.bank["AAPL"])
	require.Equal(t, int64(50), fake.bank["MSFT"])
}

func TestHandleSetStocks_MalformedJSON(t *testing.T) {
	s := newServerWithFake(newFakeService(), nil)

	req := httptest.NewRequest(http.MethodPost, "/stocks", bytes.NewBufferString(`{"stocks":`))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	var got errorResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "INVALID_REQUEST", got.Code)
}

func TestHandleSetStocks_ValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"empty name", `{"stocks":[{"name":"","quantity":1}]}`, "EMPTY_STOCK_NAME"},
		{"negative quantity", `{"stocks":[{"name":"AAPL","quantity":-1}]}`, "INVALID_QUANTITY"},
		{"duplicate name", `{"stocks":[{"name":"AAPL","quantity":1},{"name":"AAPL","quantity":2}]}`, "DUPLICATE_STOCK_NAME"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServerWithFake(newFakeService(), nil)

			req := httptest.NewRequest(http.MethodPost, "/stocks", bytes.NewBufferString(tt.body))
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)

			require.Equal(t, http.StatusBadRequest, rr.Code)
			var got errorResponse
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
			require.Equal(t, tt.wantCode, got.Code)
		})
	}
}

func TestHandleGetWallet_HappyPath(t *testing.T) {
	fake := newFakeService()
	fake.wallets["w1"] = map[string]int64{"AAPL": 5}
	s := newServerWithFake(fake, nil)

	req := httptest.NewRequest(http.MethodGet, "/wallets/w1", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got domain.Wallet
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "w1", got.ID)
	require.Equal(t, []domain.Stock{{Name: "AAPL", Quantity: 5}}, got.Stocks)
}

func TestHandleGetWallet_NotFound(t *testing.T) {
	s := newServerWithFake(newFakeService(), nil)

	req := httptest.NewRequest(http.MethodGet, "/wallets/missing", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
	var got errorResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "WALLET_NOT_FOUND", got.Code)
}

// GET /wallets/{id}/stocks/{name} returns a bare JSON integer per spec.
func TestHandleGetWalletStockQuantity_BareIntegerBody(t *testing.T) {
	fake := newFakeService()
	fake.wallets["w1"] = map[string]int64{"AAPL": 7}
	s := newServerWithFake(fake, nil)

	req := httptest.NewRequest(http.MethodGet, "/wallets/w1/stocks/AAPL", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "7\n", rr.Body.String())
}

func TestHandleGetWalletStockQuantity_ZeroForMissingHolding(t *testing.T) {
	fake := newFakeService()
	fake.wallets["w1"] = map[string]int64{}
	s := newServerWithFake(fake, nil)

	req := httptest.NewRequest(http.MethodGet, "/wallets/w1/stocks/AAPL", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "0\n", rr.Body.String())
}

func TestHandleGetWalletStockQuantity_MissingWallet(t *testing.T) {
	s := newServerWithFake(newFakeService(), nil)

	req := httptest.NewRequest(http.MethodGet, "/wallets/missing/stocks/AAPL", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
	var got errorResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "WALLET_NOT_FOUND", got.Code)
}

func TestHandleBuySell_BuyHappyPath(t *testing.T) {
	fake := newFakeService()
	fake.bank["AAPL"] = 5
	s := newServerWithFake(fake, nil)

	req := httptest.NewRequest(http.MethodPost, "/wallets/w1/stocks/AAPL", bytes.NewBufferString(`{"type":"buy"}`))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, int64(4), fake.bank["AAPL"])
	require.Equal(t, int64(1), fake.wallets["w1"]["AAPL"])
}

func TestHandleBuySell_SellHappyPath(t *testing.T) {
	fake := newFakeService()
	fake.bank["AAPL"] = 5
	fake.wallets["w1"] = map[string]int64{"AAPL": 3}
	s := newServerWithFake(fake, nil)

	req := httptest.NewRequest(http.MethodPost, "/wallets/w1/stocks/AAPL", bytes.NewBufferString(`{"type":"sell"}`))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, int64(6), fake.bank["AAPL"])
	require.Equal(t, int64(2), fake.wallets["w1"]["AAPL"])
}

func TestHandleBuySell_MalformedJSON(t *testing.T) {
	s := newServerWithFake(newFakeService(), nil)

	req := httptest.NewRequest(http.MethodPost, "/wallets/w1/stocks/AAPL", bytes.NewBufferString(`{`))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	var got errorResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "INVALID_REQUEST", got.Code)
}

// Type is strict-lowercase per the spec example {"type":"sell|buy"}.
func TestHandleBuySell_InvalidType(t *testing.T) {
	tests := []struct{ name, body string }{
		{"empty type", `{"type":""}`},
		{"uppercase BUY rejected", `{"type":"BUY"}`},
		{"unknown verb", `{"type":"trade"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServerWithFake(newFakeService(), nil)

			req := httptest.NewRequest(http.MethodPost, "/wallets/w1/stocks/AAPL", bytes.NewBufferString(tt.body))
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)

			require.Equal(t, http.StatusBadRequest, rr.Code)
			var got errorResponse
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
			require.Equal(t, "INVALID_OPERATION", got.Code)
		})
	}
}

func TestHandleBuySell_BuyStockNotFound(t *testing.T) {
	s := newServerWithFake(newFakeService(), nil)

	req := httptest.NewRequest(http.MethodPost, "/wallets/w1/stocks/UNKNOWN", bytes.NewBufferString(`{"type":"buy"}`))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
	var got errorResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "STOCK_NOT_FOUND", got.Code)
}

func TestHandleBuySell_BuyInsufficientBank(t *testing.T) {
	fake := newFakeService()
	fake.bank["AAPL"] = 0
	s := newServerWithFake(fake, nil)

	req := httptest.NewRequest(http.MethodPost, "/wallets/w1/stocks/AAPL", bytes.NewBufferString(`{"type":"buy"}`))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	var got errorResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "INSUFFICIENT_BANK_STOCK", got.Code)
}

func TestHandleBuySell_SellInsufficientWallet(t *testing.T) {
	fake := newFakeService()
	fake.bank["AAPL"] = 5
	s := newServerWithFake(fake, nil)

	req := httptest.NewRequest(http.MethodPost, "/wallets/w1/stocks/AAPL", bytes.NewBufferString(`{"type":"sell"}`))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	var got errorResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "INSUFFICIENT_WALLET_STOCK", got.Code)
}

func TestHandleGetLog_Empty(t *testing.T) {
	s := newServerWithFake(newFakeService(), nil)

	req := httptest.NewRequest(http.MethodGet, "/log", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got logResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Empty(t, got.Log)
}

func TestHandleGetLog_PopulatedPreservesOrderAndCase(t *testing.T) {
	fake := newFakeService()
	fake.auditLog = []domain.AuditEntry{
		{Operation: domain.OperationBuy, WalletID: "w1", StockName: "AAPL"},
		{Operation: domain.OperationSell, WalletID: "w2", StockName: "GOOG"},
	}
	s := newServerWithFake(fake, nil)

	req := httptest.NewRequest(http.MethodGet, "/log", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got logResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, []auditEntryDTO{
		{Type: "buy", WalletID: "w1", StockName: "AAPL"},
		{Type: "sell", WalletID: "w2", StockName: "GOOG"},
	}, got.Log)
}

func TestHandleChaos_TriggersChaosFunc(t *testing.T) {
	var called atomic.Int32
	chaos := func() { called.Add(1) }
	s := newServerWithFake(newFakeService(), chaos)

	req := httptest.NewRequest(http.MethodPost, "/chaos", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusAccepted, rr.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, "shutting down", body["message"])

	// chaosShutdownDelay is 100ms; allow a comfortable margin against scheduling jitter.
	require.Eventually(t, func() bool { return called.Load() == 1 }, 500*time.Millisecond, 10*time.Millisecond)
}
