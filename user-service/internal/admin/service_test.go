package admin

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type fakeRepository struct {
	allowedDomain string
	actorID       uuid.UUID
	queriedDomain string
	allowed       bool
}

func (f *fakeRepository) ListAllowedDomains(context.Context) ([]AllowedDomain, error) {
	return nil, nil
}

func (f *fakeRepository) IsDomainAllowed(_ context.Context, domain string) (bool, error) {
	f.queriedDomain = domain
	return f.allowed, nil
}

func (f *fakeRepository) AllowDomain(_ context.Context, domain string, actorID uuid.UUID) error {
	f.allowedDomain, f.actorID = domain, actorID
	return nil
}

func (f *fakeRepository) RemoveAllowedDomain(context.Context, uuid.UUID) error {
	return nil
}

func TestServiceNormalizesDomain(t *testing.T) {
	repo := &fakeRepository{allowed: true}
	service := NewAllowedDomainService(repo)
	ctx := context.Background()
	actorID := uuid.New()
	if err := service.AllowDomain(ctx, "  EXAMPLE.COM  ", actorID); err != nil {
		t.Fatal(err)
	}
	if repo.allowedDomain != "example.com" || repo.actorID != actorID {
		t.Fatalf("stored domain %q for actor %v, want \"example.com\"", repo.allowedDomain, repo.actorID)
	}
	if allowed, err := service.IsDomainAllowed(ctx, " EXAMPLE.COM "); err != nil || !allowed {
		t.Fatalf("domain membership: allowed=%v err=%v", allowed, err)
	}
	if repo.queriedDomain != "example.com" {
		t.Fatalf("queried domain %q, want \"example.com\"", repo.queriedDomain)
	}
}
