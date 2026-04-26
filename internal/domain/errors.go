package domain

import "errors"

var (
	ErrWalletNotFound          = errors.New("wallet not found")
	ErrStockNotFound           = errors.New("stock not found")
	ErrInsufficientBankStock   = errors.New("insufficient bank stock")
	ErrInsufficientWalletStock = errors.New("insufficient wallet stock")
	ErrInvalidOperation        = errors.New("invalid operation")
)

type OperationType string

const (
	OperationBuy  OperationType = "BUY"
	OperationSell OperationType = "SELL"
)

func (o OperationType) Validate() error {
	switch o {
	case OperationBuy, OperationSell:
		return nil
	default:
		return ErrInvalidOperation
	}
}
