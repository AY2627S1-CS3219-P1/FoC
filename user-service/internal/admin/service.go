// Package admin covers the admin-side features of the user service:
// the registration domain whitelist (U1.1.2), role changes incl.
// suspension/reinstatement (U4, U6) and account warnings (U7).
// Authorisation is decided in the handlers from the caller's role.
package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"user-service/internal/apperr"
	"user-service/internal/clock"
	"user-service/internal/models"
	"user-service/internal/validate"
)

var (
	ErrUserNotFound    = apperr.ErrNotFound.WithMessage("user not found")
	ErrDomainNotFound  = apperr.ErrNotFound.WithMessage("email domain not found")
	ErrWarningNotFound = apperr.ErrNotFound.WithMessage("warning not found")
	ErrDomainExists    = apperr.New(http.StatusConflict, "domain_exists", "this domain is already whitelisted")
	ErrRoleConflict    = apperr.New(http.StatusConflict, "role_conflict",
		"the user's role changed while you were editing; reload and try again")
	ErrWarningRemoved = apperr.New(http.StatusConflict, "warning_already_removed", "this warning has already been removed")
)

type Service struct {
	repo  Repository
	clock clock.Clock
}

func NewService(repo Repository, clk clock.Clock) *Service {
	return &Service{repo: repo, clock: clk}
}

func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (*models.User, error) {
	u, err := s.repo.GetUser(ctx, id)
	if errors.Is(err, errNotFound) {
		return nil, ErrUserNotFound
	}
	return u, err
}

// ---- email domain whitelist (U1.1.2) ----

func (s *Service) ListDomains(ctx context.Context) ([]models.AllowedEmailDomain, error) {
	return s.repo.ListDomains(ctx)
}

func (s *Service) AddDomain(ctx context.Context, actorID uuid.UUID, raw string) (*models.AllowedEmailDomain, error) {
	f := apperr.Fields{}
	domain := validate.Domain(f, "domain", raw)
	if err := f.Err(); err != nil {
		return nil, err
	}
	d := &models.AllowedEmailDomain{Domain: domain, CreatedBy: &actorID, CreatedAt: s.clock.Now()}
	if err := s.repo.AddDomain(ctx, d); err != nil {
		if errors.Is(err, errDuplicate) {
			return nil, ErrDomainExists
		}
		return nil, err
	}
	return d, nil
}

func (s *Service) DeleteDomain(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteDomain(ctx, id); errors.Is(err, errNotFound) {
		return ErrDomainNotFound
	} else {
		return err
	}
}

// ---- role changes (U4, U6) ----

type ChangeRoleInput struct {
	To       models.RoleName
	Reason   *string
	ReportID *uuid.UUID
}

// ChangeRole moves target to in.To. The handler must already have checked
// Principal.CanAssignRole. Suspend/reinstate require a reason (U6.2, U6.9.1).
func (s *Service) ChangeRole(ctx context.Context, actorID uuid.UUID, target *models.User, in ChangeRoleInput) (*models.RoleChange, error) {
	f := apperr.Fields{}
	if !in.To.Valid() {
		f.Add("role", "must be one of super_admin, admin, user, suspended")
	} else if in.To == target.Role {
		f.Add("role", "user already has this role")
	}
	needsReason := in.To == models.RoleSuspended || target.Role == models.RoleSuspended
	reason := validate.Reason(f, "reason", in.Reason, needsReason)
	if err := f.Err(); err != nil {
		return nil, err
	}

	c := &models.RoleChange{
		UserID: target.ID, FromRole: target.Role, ToRole: in.To,
		Reason: reason, ActorID: &actorID, ReportID: in.ReportID, CreatedAt: s.clock.Now(),
	}
	if err := s.repo.ChangeRole(ctx, c); err != nil {
		if errors.Is(err, errRoleConflict) {
			return nil, ErrRoleConflict
		}
		return nil, err
	}
	target.Role = in.To
	return c, nil
}

func (s *Service) RoleHistory(ctx context.Context, userID uuid.UUID) ([]models.RoleChange, error) {
	return s.repo.RoleChanges(ctx, userID)
}

// ---- warnings (U7) ----

type IssueWarningInput struct {
	UserID        uuid.UUID
	RequestID     uuid.UUID  // U7.2 originating request
	ReportID      *uuid.UUID // set when it came from a report
	Reason        *string    // U7.3
	SourceEventID *uuid.UUID // for idempotent event consumption
}

// IssueWarning records a warning (U7.1). Replaying the same SourceEventID
// returns the original warning with created=false.
func (s *Service) IssueWarning(ctx context.Context, in IssueWarningInput) (*models.AccountWarning, bool, error) {
	f := apperr.Fields{}
	if in.RequestID == uuid.Nil {
		f.Add("request_id", "is required")
	}
	reason := validate.Reason(f, "reason", in.Reason, true)
	if err := f.Err(); err != nil {
		return nil, false, err
	}
	if _, err := s.GetUser(ctx, in.UserID); err != nil {
		return nil, false, err
	}
	w := &models.AccountWarning{
		UserID: in.UserID, RequestID: in.RequestID, ReportID: in.ReportID,
		Reason: *reason, Status: models.WarningActive, SourceEventID: in.SourceEventID,
		CreatedAt: s.clock.Now(),
	}
	created, err := s.repo.CreateWarning(ctx, w)
	return w, created, err
}

func (s *Service) GetWarning(ctx context.Context, id uuid.UUID) (*models.AccountWarning, error) {
	w, err := s.repo.GetWarning(ctx, id)
	if errors.Is(err, errNotFound) {
		return nil, ErrWarningNotFound
	}
	return w, err
}

func (s *Service) Warnings(ctx context.Context, userID uuid.UUID) ([]models.AccountWarning, error) {
	return s.repo.Warnings(ctx, userID)
}

// RemoveWarning marks a warning removed, e.g. when its appeal is
// Overturned (U7.6). An Upheld appeal simply leaves it (U7.5).
func (s *Service) RemoveWarning(ctx context.Context, id uuid.UUID, reason *string, appealID *uuid.UUID) (*models.AccountWarning, error) {
	f := apperr.Fields{}
	rs := validate.Reason(f, "reason", reason, true)
	if err := f.Err(); err != nil {
		return nil, err
	}
	w, err := s.repo.RemoveWarning(ctx, id, s.clock.Now(), rs, appealID)
	if errors.Is(err, errNotFound) {
		if _, gerr := s.GetWarning(ctx, id); gerr != nil {
			return nil, gerr
		}
		return nil, ErrWarningRemoved
	}
	return w, err
}
