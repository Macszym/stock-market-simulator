// Package service contains the application use cases. It validates input
// against the domain rules and delegates persistence to a Repository.
package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Macszym/stock-market-simulator/internal/domain"
)

// Repository is the persistence contract that Service depends on. It is
// defined here (the consumer) rather than in storage so that tests can supply
// a fake without importing the storage package.
type Repository interface {
	GetBankStocks(ctx context.Context) ([]domain.Stock, error)
	SetBankStocks(ctx context.Context, stocks []domain.Stock) error
	GetWallet(ctx context.Context, id string) (domain.Wallet, error)
	GetWalletStockQuantity(ctx context.Context, walletID, stockName string) (int64, error)
	GetAuditLog(ctx context.Context) ([]domain.AuditEntry, error)
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func NewService(repo Repository, logger *slog.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

func (s *Service) GetBank(ctx context.Context) ([]domain.Stock, error) {
	return s.repo.GetBankStocks(ctx)
}

func (s *Service) SetBank(ctx context.Context, stocks []domain.Stock) error {
	seen := map[string]bool{}
	for _, st := range stocks {
		if st.Name == "" {
			return fmt.Errorf("%w", domain.ErrEmptyStockName)
		}
		if st.Quantity < 0 {
			return fmt.Errorf("%w: %q has %d", domain.ErrInvalidQuantity, st.Name, st.Quantity)
		}
		if seen[st.Name] {
			return fmt.Errorf("%w: %q", domain.ErrDuplicateStockName, st.Name)
		}
		seen[st.Name] = true
	}

	if err := s.repo.SetBankStocks(ctx, stocks); err != nil {
		return err
	}
	s.logger.Info("bank updated", "stock_count", len(stocks))
	return nil
}

func (s *Service) GetWallet(ctx context.Context, id string) (domain.Wallet, error) {
	return s.repo.GetWallet(ctx, id)
}

func (s *Service) GetWalletStockQuantity(ctx context.Context, walletID, stockName string) (int64, error) {
	return s.repo.GetWalletStockQuantity(ctx, walletID, stockName)
}

func (s *Service) GetAuditLog(ctx context.Context) ([]domain.AuditEntry, error) {
	return s.repo.GetAuditLog(ctx)
}
