// Package domain holds the core types of the stock market simulator.
// Types here are pure data: no IO, no database access, no HTTP knowledge.
package domain

import "time"

type Stock struct {
	Name     string
	Quantity int64
}

type Wallet struct {
	ID     string
	Stocks []Stock
}

type AuditEntry struct {
	ID        int64
	Operation OperationType
	WalletID  string
	StockName string
	CreatedAt time.Time
}
