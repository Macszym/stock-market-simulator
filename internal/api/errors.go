package api

import (
	"errors"
	"net/http"

	"github.com/Macszym/stock-market-simulator/internal/domain"
)

// mapError translates a service-layer error into an HTTP status, a stable
// machine-readable code, and a human-readable message. Unknown errors are
// reported as 500 INTERNAL_ERROR; the caller should log the original error
// before responding.
func mapError(err error) (status int, code, msg string) {
	switch {
	case errors.Is(err, domain.ErrWalletNotFound):
		return http.StatusNotFound, "WALLET_NOT_FOUND", err.Error()
	case errors.Is(err, domain.ErrStockNotFound):
		return http.StatusNotFound, "STOCK_NOT_FOUND", err.Error()
	case errors.Is(err, domain.ErrEmptyStockName):
		return http.StatusBadRequest, "EMPTY_STOCK_NAME", err.Error()
	case errors.Is(err, domain.ErrInvalidQuantity):
		return http.StatusBadRequest, "INVALID_QUANTITY", err.Error()
	case errors.Is(err, domain.ErrDuplicateStockName):
		return http.StatusBadRequest, "DUPLICATE_STOCK_NAME", err.Error()
	case errors.Is(err, domain.ErrInvalidOperation):
		return http.StatusBadRequest, "INVALID_OPERATION", err.Error()
	case errors.Is(err, domain.ErrInsufficientBankStock):
		return http.StatusBadRequest, "INSUFFICIENT_BANK_STOCK", err.Error()
	case errors.Is(err, domain.ErrInsufficientWalletStock):
		return http.StatusBadRequest, "INSUFFICIENT_WALLET_STOCK", err.Error()
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error"
	}
}
