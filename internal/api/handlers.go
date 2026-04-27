package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Macszym/stock-market-simulator/internal/domain"
)

// chaosShutdownDelay gives the response time to leave the socket before the
// shutdown signal cancels in-flight connections. Without it the client sees
// EOF or, behind a load balancer, a 502.
const chaosShutdownDelay = 100 * time.Millisecond

type stocksResponse struct {
	Stocks []domain.Stock `json:"stocks"`
}

type buySellRequest struct {
	Type string `json:"type"`
}

type setStocksRequest struct {
	Stocks []domain.Stock `json:"stocks"`
}

type auditEntryDTO struct {
	Type      string `json:"type"`
	WalletID  string `json:"wallet_id"`
	StockName string `json:"stock_name"`
}

type logResponse struct {
	Log []auditEntryDTO `json:"log"`
}

func (s *Server) handleGetStocks(w http.ResponseWriter, r *http.Request) {
	stocks, err := s.svc.GetBank(r.Context())
	if err != nil {
		status, code, msg := mapError(err)
		if status >= 500 {
			s.logger.Error("get bank failed", "err", err)
		}
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, stocksResponse{Stocks: stocks})
}

func (s *Server) handleSetStocks(w http.ResponseWriter, r *http.Request) {
	var req setStocksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body")
		return
	}
	if err := s.svc.SetBank(r.Context(), req.Stocks); err != nil {
		status, code, msg := mapError(err)
		if status >= 500 {
			s.logger.Error("set bank failed", "err", err)
		}
		writeError(w, status, code, msg)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGetWallet(w http.ResponseWriter, r *http.Request) {
	walletID := r.PathValue("wallet_id")
	wallet, err := s.svc.GetWallet(r.Context(), walletID)
	if err != nil {
		status, code, msg := mapError(err)
		if status >= 500 {
			s.logger.Error("get wallet failed", "err", err, "wallet_id", walletID)
		}
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, wallet)
}

func (s *Server) handleGetWalletStockQuantity(w http.ResponseWriter, r *http.Request) {
	walletID := r.PathValue("wallet_id")
	stockName := r.PathValue("stock_name")
	qty, err := s.svc.GetWalletStockQuantity(r.Context(), walletID, stockName)
	if err != nil {
		status, code, msg := mapError(err)
		if status >= 500 {
			s.logger.Error("get wallet stock quantity failed", "err", err,
				"wallet_id", walletID, "stock_name", stockName)
		}
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, qty)
}

func (s *Server) handleBuySell(w http.ResponseWriter, r *http.Request) {
	var req buySellRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body")
		return
	}

	walletID := r.PathValue("wallet_id")
	stockName := r.PathValue("stock_name")

	var err error
	switch req.Type {
	case "buy":
		err = s.svc.BuyStock(r.Context(), walletID, stockName)
	case "sell":
		err = s.svc.SellStock(r.Context(), walletID, stockName)
	default:
		writeError(w, http.StatusBadRequest, "INVALID_OPERATION", "type must be 'buy' or 'sell'")
		return
	}

	if err != nil {
		status, code, msg := mapError(err)
		if status >= 500 {
			s.logger.Error("buy/sell failed", "err", err, "type", req.Type,
				"wallet_id", walletID, "stock_name", stockName)
		}
		writeError(w, status, code, msg)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGetLog(w http.ResponseWriter, r *http.Request) {
	entries, err := s.svc.GetAuditLog(r.Context())
	if err != nil {
		status, code, msg := mapError(err)
		if status >= 500 {
			s.logger.Error("get audit log failed", "err", err)
		}
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, toLogResponse(entries))
}

// handleChaos triggers the same graceful shutdown path as SIGTERM. The shutdown
// runs in a goroutine after a short delay so the response leaves the socket
// before the listener is torn down.
func (s *Server) handleChaos(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "shutting down"})
	go func() {
		time.Sleep(chaosShutdownDelay)
		s.chaos()
	}()
}

func toLogResponse(entries []domain.AuditEntry) logResponse {
	log := make([]auditEntryDTO, 0, len(entries))
	for _, e := range entries {
		log = append(log, auditEntryDTO{
			Type:      strings.ToLower(string(e.Operation)),
			WalletID:  e.WalletID,
			StockName: e.StockName,
		})
	}
	return logResponse{Log: log}
}
