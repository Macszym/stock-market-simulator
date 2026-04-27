// Package api wires HTTP routes to service operations.
package api

import (
	"log/slog"
	"net/http"

	"github.com/Macszym/stock-market-simulator/internal/service"
)

type Server struct {
	svc    *service.Service
	logger *slog.Logger
	chaos  func()
	mux    *http.ServeMux
}

// NewServer wires HTTP routes to service operations. The chaos function is
// invoked by POST /chaos to trigger the instance shutdown; in production it is
// the cancel returned by signal.NotifyContext so the kill path is identical to
// receiving SIGTERM.
func NewServer(svc *service.Service, logger *slog.Logger, chaos func()) *Server {
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
