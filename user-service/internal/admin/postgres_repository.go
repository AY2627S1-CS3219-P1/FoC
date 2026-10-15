package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/alloweddomaindb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresAllowedAdminRepository struct {
	queries *alloweddomaindb.Queries
}

func NewPostgresAllowedAdminRepository(queries *alloweddomaindb.Queries) *PostgresAllowedAdminRepository {
	return &PostgresAllowedAdminRepository{queries: queries}
}

var _ AllowedDomainRepository = (*PostgresAllowedAdminRepository)(nil)

func (p *PostgresAllowedAdminRepository) IsDomainAllowed(ctx context.Context, domain string) (bool, error) {
	allowed, err := p.queries.IsDomainAllowed(ctx, domain)
	if err != nil {
		return false, fmt.Errorf("check allowed domain: %w", err)
	}
	return allowed, nil
}

func (p *PostgresAllowedAdminRepository) ListAllowedDomains(ctx context.Context) ([]AllowedDomain, error) {
	rows, err := p.queries.ListAllowedDomains(ctx)
	if err != nil {
		return nil, fmt.Errorf("list allowed domains: %w", err)
	}

	allowedDomains := make([]AllowedDomain, len(rows))
	for i, row := range rows {
		allowedDomains[i] = *NewAllowedDomain(row.ID, row.Domain)
	}

	return allowedDomains, nil
}

func (p *PostgresAllowedAdminRepository) AllowDomain(ctx context.Context, domain string, actorID uuid.UUID) error {
	err := p.queries.AllowDomain(ctx, alloweddomaindb.AllowDomainParams{Domain: domain, ActorID: actorID})
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23505" && (pgErr.ConstraintName == "allowed_email_domains_domain_key" || pgErr.ConstraintName == "idx_allowed_email_domains_live"):
			return fmt.Errorf("%w: %w", errDomainAlreadyExists, err)
		case pgErr.Code == "23514" && pgErr.ConstraintName == "allowed_email_domains_domain_check":
			return fmt.Errorf("%w: %w", errInvalidDomain, err)
		}
	}
	return fmt.Errorf("allow domain: %w", err)
}

func (p *PostgresAllowedAdminRepository) RemoveAllowedDomain(ctx context.Context, id uuid.UUID) error {
	rowsAffected, err := p.queries.RemoveAllowedDomain(ctx, id)
	if err != nil {
		return fmt.Errorf("remove allowed domain: %w", err)
	}
	if rowsAffected == 0 {
		return errDomainNotFound
	}
	return nil
}
