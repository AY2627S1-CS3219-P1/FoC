package admin

import (
	"context"
	"errors"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/google/uuid"
)

type AllowedDomainService struct {
	repo AllowedDomainRepository
}

func NewAllowedDomainService(repo *AllowedDomainRepository) AllowedDomainService {
	if repo == nil {
		panic("AllowedDomainRepository cannot be nil")
	}

	return AllowedDomainService{repo: *repo}
}

func (s AllowedDomainService) ListAllowedDomains(
	ctx context.Context,
) ([]AllowedDomain, error) {
	return s.repo.ListAllowedDomains(ctx)
}

func (s AllowedDomainService) AllowDomain(
	ctx context.Context,
	domain string,
	actorId uuid.UUID,
) error {
	err := s.repo.AllowDomain(ctx, domain, actorId)
	switch {
	case errors.Is(err, errDomainAlreadyExists):
		return errs.WrapAlreadyExistsError(err, "domain already allowed")
	case errors.Is(err, errInvalidDomain):
		return errs.WrapBadRequestError(err, "invalid allowed domain")
	default:
		return err
	}
}

func (s AllowedDomainService) RemoveAllowedDomain(
	ctx context.Context,
	id uuid.UUID,
) error {
	err := s.repo.RemoveAllowedDomain(ctx, id)
	if errors.Is(err, errDomainNotFound) {
		return errs.WrapNotFoundError(err, "allowed domain not found")
	}
	return err
}
