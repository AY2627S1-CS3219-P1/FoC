package admin

import "github.com/google/uuid"

type AllowedDomain struct {
	ID     uuid.UUID
	Domain string
}

func NewAllowedDomain(id uuid.UUID, domain string) *AllowedDomain {
	return &AllowedDomain{id, domain}
}
