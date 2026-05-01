package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
