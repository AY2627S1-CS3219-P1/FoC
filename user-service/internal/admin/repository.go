package admin

import (
	"context"

	"github.com/google/uuid"
)

type AllowedDomainRepository interface {
	ListAllowedDomains(context.Context) ([]AllowedDomain, error)
	AllowDomain(context.Context, string, uuid.UUID) error
	RemoveAllowedDomain(context.Context, uuid.UUID) error
}
