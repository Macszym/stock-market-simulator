// Package api wires HTTP routes to service operations.
package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/Macszym/stock-market-simulator/internal/domain"
)

// ServiceAPI is the set of service operations the HTTP layer depends on.
// Declared here (the consumer) so handler tests can drop in a fake without
// importing the service package, mirroring the Repository pattern in service.
type ServiceAPI interface {
	GetBank(ctx context.Context) ([]domain.Stock, error)
	SetBank(ctx context.Context, stocks []domain.Stock) error
	GetWallet(ctx context.Context, id string) (domain.Wallet, error)
	GetWalletStockQuantity(ctx context.Context, walletID, stockName string) (int64, error)
	GetAuditLog(ctx context.Context) ([]domain.AuditEntry, error)
	BuyStock(ctx context.Context, walletID, stockName string) error
	SellStock(ctx context.Context, walletID, stockName string) error
}

type Server struct {
	svc    ServiceAPI
	logger *slog.Logger
	chaos  func()
	mux    *http.ServeMux
}

// NewServer wires HTTP routes to service operations. The chaos function is
// invoked by POST /chaos to trigger the instance shutdown; in production it is
// the cancel returned by signal.NotifyContext so the kill path is identical to
// receiving SIGTERM.
func NewServer(svc ServiceAPI, logger *slog.Logger, chaos func()) *Server {
	s := &Server{
		svc:    svc,
		logger: logger,
		chaos:  chaos,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /stocks", s.handleGetStocks)
	s.mux.HandleFunc("POST /stocks", s.handleSetStocks)
	s.mux.HandleFunc("GET /wallets/{wallet_id}", s.handleGetWallet)
	s.mux.HandleFunc("GET /wallets/{wallet_id}/stocks/{stock_name}", s.handleGetWalletStockQuantity)
	s.mux.HandleFunc("POST /wallets/{wallet_id}/stocks/{stock_name}", s.handleBuySell)
	s.mux.HandleFunc("GET /log", s.handleGetLog)
	s.mux.HandleFunc("POST /chaos", s.handleChaos)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
