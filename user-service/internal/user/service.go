package user

import (
	"context"

	"github.com/google/uuid"

	"user-service/internal/apperr"
	"user-service/internal/clock"
	"user-service/internal/models"
	"user-service/internal/validate"
)

// Service holds profile and favourites business rules. Authorisation is the
// handler's job; the service assumes the caller is allowed.
type Service struct {
	repo      Repository
	suppliers SupplierChecker
	clock     clock.Clock
}

func NewService(repo Repository, suppliers SupplierChecker, clk clock.Clock) *Service {
	return &Service{repo: repo, suppliers: suppliers, clock: clk}
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context, p ListParams) ([]User, int64, error) {
	if p.Role != nil && !p.Role.Valid() {
		return nil, 0, apperr.Invalid("role", "must be one of super_admin, admin, user, suspended")
	}
	return s.repo.List(ctx, p.Normalize())
}

// Update applies a partial profile update (U3.3, U3.6, U1.4).
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*User, error) {
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	f := apperr.Fields{}
	if in.DisplayName != nil {
		u.DisplayName = validate.DisplayName(f, "display_name", *in.DisplayName)
	}
	if in.Description != nil {
		u.Description = validate.Description(f, "description", *in.Description)
	}
	if in.TelegramHandle != nil {
		u.TelegramHandle = validate.Telegram(f, "telegram_handle", in.TelegramHandle)
	}
	if in.PhoneNumber != nil {
		u.PhoneNumber = validate.Phone(f, "phone_number", in.PhoneNumber)
	}
	// U1.4: can't clear the last contact method.
	if len(f) == 0 && u.TelegramHandle == nil && u.PhoneNumber == nil {
		f.Add("telegram_handle", "keep at least a Telegram handle or a phone number")
	}
	if err := f.Err(); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

func (s *Service) Favourites(ctx context.Context, userID uuid.UUID) ([]models.FavouriteSupplier, error) {
	return s.repo.ListFavourites(ctx, userID)
}

// AddFavourite validates the supplier then saves it; repeating is a no-op (U3.4.1).
func (s *Service) AddFavourite(ctx context.Context, userID, supplierID uuid.UUID) error {
	ok, err := s.suppliers.SupplierExists(ctx, supplierID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSupplierNotFound
	}
	return s.repo.AddFavourite(ctx, userID, supplierID, s.clock.Now())
}

// RemoveFavourite deletes it; removing a non-favourite is a no-op (U3.4.2).
func (s *Service) RemoveFavourite(ctx context.Context, userID, supplierID uuid.UUID) error {
	return s.repo.RemoveFavourite(ctx, userID, supplierID)
}
