package admin

import (
	"context"
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
	return s.repo.AllowDomain(ctx, domain, actorId)
}

func (s AllowedDomainService) RemoveAllowedDomain(
	ctx context.Context,
	id uuid.UUID,
) error {
	return s.repo.RemoveAllowedDomain(ctx, id)
}
