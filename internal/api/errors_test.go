package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Macszym/stock-market-simulator/internal/domain"
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "wallet not found maps to 404",
			err:        domain.ErrWalletNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   "WALLET_NOT_FOUND",
		},
		{
			name:       "stock not found maps to 404",
			err:        domain.ErrStockNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   "STOCK_NOT_FOUND",
		},
		{
			name:       "empty stock name maps to 400",
			err:        domain.ErrEmptyStockName,
			wantStatus: http.StatusBadRequest,
			wantCode:   "EMPTY_STOCK_NAME",
		},
		{
			name:       "invalid quantity maps to 400",
			err:        domain.ErrInvalidQuantity,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_QUANTITY",
		},
		{
			name:       "duplicate stock name maps to 400",
			err:        domain.ErrDuplicateStockName,
			wantStatus: http.StatusBadRequest,
			wantCode:   "DUPLICATE_STOCK_NAME",
		},
		{
			name:       "invalid operation maps to 400",
			err:        domain.ErrInvalidOperation,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_OPERATION",
		},
		{
			name:       "insufficient bank stock maps to 400",
			err:        domain.ErrInsufficientBankStock,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INSUFFICIENT_BANK_STOCK",
		},
		{
			name:       "insufficient wallet stock maps to 400",
			err:        domain.ErrInsufficientWalletStock,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INSUFFICIENT_WALLET_STOCK",
		},
		{
			name:       "unknown error falls through to 500",
			err:        errors.New("connection reset"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code, msg := mapError(tt.err)
			require.Equal(t, tt.wantStatus, status)
			require.Equal(t, tt.wantCode, code)
			require.NotEmpty(t, msg)
		})
	}
}

// Storage wraps DB errors with fmt.Errorf("...: %w", err) for unexpected
// failures. Sentinels themselves are returned directly today, but pinning
// the contract on errors.Is keeps the layer safe if a wrapping callsite
// ever appears between storage and the handler.
func TestMapError_WrappedSentinelStillMatches(t *testing.T) {
	wrapped := fmt.Errorf("storage layer: %w", domain.ErrStockNotFound)

	status, code, _ := mapError(wrapped)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "STOCK_NOT_FOUND", code)
}

// Internal errors must not leak the original error string to the client;
// it can carry DB connection details, file paths, or stack-like context.
func TestMapError_InternalErrorScrubsMessage(t *testing.T) {
	_, _, msg := mapError(errors.New("dial tcp 10.0.0.1:5432: connection refused"))
	require.Equal(t, "internal server error", msg)
}
