package account

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Store reads accounts without side effects. Account returns
// ErrAccountNotFound for a user Credit has not opened an account for.
type Store interface {
	Account(ctx context.Context, userID string) (Account, error)
	LedgerEntries(ctx context.Context, userID string, limit, offset int64) ([]LedgerEntry, int64, error)
}

// Opener opens a user's account with the starting credits, doing nothing
// when it is already open.
type Opener interface {
	Open(ctx context.Context, userID string, at time.Time) error
}

// DeletionRecorder durably records the committed deletion of a FoC account.
type DeletionRecorder interface {
	RecordDeletion(ctx context.Context, userID, eventID uuid.UUID, deletedAt time.Time) error
}

type Service struct {
	store    Store
	opener   Opener
	recorder DeletionRecorder
	clock    func() time.Time
}

func NewService(store Store, opener Opener, recorder DeletionRecorder, clock func() time.Time) *Service {
	return &Service{store: store, opener: opener, recorder: recorder, clock: clock}
}

// Open opens the user's account with the starting credits when Credit learns
// of a new user. Repeating it grants nothing (C1.3.1). A user whose committed
// deletion Credit has recorded is never re-provisioned.
func (s *Service) Open(ctx context.Context, userID string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return ErrInvalidArgument
	}
	return s.opener.Open(ctx, id.String(), s.clock().UTC())
}

// RecordDeletion stores the users.user_deleted replay marker. Both IDs come
// from the event contract; anything unparseable is refused so a malformed
// event can never mark the wrong user.
func (s *Service) RecordDeletion(ctx context.Context, userID, eventID string, deletedAt time.Time) error {
	id, err := uuid.Parse(userID)
	if err != nil || id == uuid.Nil {
		return ErrInvalidArgument
	}
	event, err := uuid.Parse(eventID)
	if err != nil || event == uuid.Nil {
		return ErrInvalidArgument
	}
	if s.recorder == nil {
		return fmt.Errorf("record deletion %s: recorder unavailable", id)
	}
	return s.recorder.RecordDeletion(ctx, id, event, deletedAt.UTC())
}

// GetAccount returns the user's account, or ErrAccountNotFound.
func (s *Service) GetAccount(ctx context.Context, userID string) (Account, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return Account{}, ErrInvalidArgument
	}
	return s.store.Account(ctx, id.String())
}

// ListLedgerEntries returns the user's entries, newest first, or
// ErrAccountNotFound.
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
