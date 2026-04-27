// Package domain holds the core types of the stock market simulator.
// Types here are pure data: no IO, no database access, no HTTP knowledge.
package domain

import "time"

type Stock struct {
	Name     string `json:"name"`
	Quantity int64  `json:"quantity"`
}

type Wallet struct {
	ID     string  `json:"id"`
	Stocks []Stock `json:"stocks"`
}

// AuditEntry has no JSON tags by design: the GET /log response shape exposes
// only {type, wallet_id, stock_name} with a lowercase operation, while this
// type keeps ID/CreatedAt internally for ordering and debugging. The HTTP
// layer converts this to its own DTO.
type AuditEntry struct {
	ID        int64
	Operation OperationType
	WalletID  string
	StockName string
	CreatedAt time.Time
}
