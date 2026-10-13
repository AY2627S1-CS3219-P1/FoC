package admin

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/alloweddomaindb"
	"github.com/google/uuid"
)

type PostgresAllowedAdminRepository struct {
	queries *alloweddomaindb.Queries
}

func NewPostgresAllowedAdminRepository(queries *alloweddomaindb.Queries) *PostgresAllowedAdminRepository {
	return &PostgresAllowedAdminRepository{queries: queries}
}

var _ AllowedDomainRepository = (*PostgresAllowedAdminRepository)(nil)

func (p PostgresAllowedAdminRepository) ListAllowedDomains(ctx context.Context) ([]AllowedDomain, error) {
	rows, err := p.queries.ListAllowedDomains(ctx)
	if err != nil {
		return nil, err
	}

	allowedDomains := make([]AllowedDomain, len(rows))
	for i, row := range rows {
		allowedDomains[i] = *NewAllowedDomain(row.ID, row.Domain)
	}

	return allowedDomains, nil
}

func (p PostgresAllowedAdminRepository) AllowDomain(ctx context.Context, domain string, actorID uuid.UUID) error {
	return p.queries.AllowDomain(ctx, alloweddomaindb.AllowDomainParams{Domain: domain, ActorID: actorID})
}

func (p PostgresAllowedAdminRepository) RemoveAllowedDomain(ctx context.Context, id uuid.UUID) error {
	rowsAffected, err := p.queries.RemoveAllowedDomain(ctx, id)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return errs.NewNotFoundError("allowed domain not found")
	}
	return nil
}
