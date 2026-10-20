package account

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Store opens missing accounts with the starting credits.
type Store interface {
	Account(ctx context.Context, userID string) (Account, error)
	LedgerEntries(ctx context.Context, userID string, limit, offset int64) ([]LedgerEntry, int64, error)
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

// GetAccount opens the account on the user's first call.
func (s *Service) GetAccount(ctx context.Context, userID string) (Account, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return Account{}, ErrInvalidArgument
	}
	return s.store.Account(ctx, id.String())
}

// ListLedgerEntries returns the user's entries, newest first.
func (s *Service) ListLedgerEntries(ctx context.Context, userID string, p Page) (LedgerPage, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return LedgerPage{}, ErrInvalidArgument
	}
	if p.Number < 0 || p.Size < 0 || p.Size > MaxPageSize {
		return LedgerPage{}, ErrInvalidArgument
	}
	if p.Number == 0 {
		p.Number = 1
	}
	if p.Size == 0 {
		p.Size = DefaultPageSize
	}
	if _, err := s.store.Account(ctx, id.String()); err != nil {
		return LedgerPage{}, err
	}
	size := int64(p.Size)
	entries, total, err := s.store.LedgerEntries(ctx, id.String(), size, int64(p.Number-1)*size)
	if err != nil {
		return LedgerPage{}, fmt.Errorf("list ledger entries: %w", err)
	}
	return LedgerPage{Entries: entries, PageInfo: PageInfo{
		Page:       p.Number,
		PageSize:   p.Size,
		TotalItems: total,
		TotalPages: int32((total + size - 1) / size),
	}}, nil
}
