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
	mux    *http.ServeMux
}

func NewServer(svc *service.Service, logger *slog.Logger) *Server {
	s := &Server{
		svc:    svc,
		logger: logger,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
