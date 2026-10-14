package admin

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	errDomainNotFound      = errors.New("allowed domain not found")
	errDomainAlreadyExists = errors.New("allowed domain already exists")
	errInvalidDomain       = errors.New("invalid allowed domain")
)

type AllowedDomainRepository interface {
	ListAllowedDomains(context.Context) ([]AllowedDomain, error)
	AllowDomain(context.Context, string, uuid.UUID) error
	RemoveAllowedDomain(context.Context, uuid.UUID) error
}
