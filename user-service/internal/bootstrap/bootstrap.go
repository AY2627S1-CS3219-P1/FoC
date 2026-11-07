// Package bootstrap creates the first super_admin from operator-supplied
// configuration. Persistence and concurrency live in store.Admin.Bootstrap.
package bootstrap

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
)

const DefaultDisplayName = "Admin"

type Store interface {
	// Bootstrap claims the singleton bootstrap row and creates or promotes the
	// user, revoking a promoted user's sessions in the same transaction and
	// returning their IDs.
	Bootstrap(context.Context, *models.User, time.Time) (store.BootstrapOutcome, []uuid.UUID, error)
}

type Config struct {
	Email       string
	DisplayName string
}

type Bootstrapper struct {
	Store Store
	// Revocations records the promoted user's sessions in the shared
	// dictionary; a nil writer panics on promotion, matching the other
	// service dependencies.
	Revocations service.AccessTokenRevocations
	Now         func() time.Time
}

// Run validates cfg and bootstraps the super_admin. The operator is trusted,
// so the registration domain whitelist is not applied. A promotion changes
// the role stored in the target's outstanding access tokens, so the store
// revokes their sessions in the bootstrap transaction and this records them
// in the revocation dictionary after it commits.
func (b *Bootstrapper) Run(ctx context.Context, cfg Config) (models.User, store.BootstrapOutcome, error) {
	email, err := service.NormalizeEmail(cfg.Email)
	if err != nil {
		return models.User{}, 0, err
	}
	name := strings.TrimSpace(cfg.DisplayName)
	if name == "" {
		name = DefaultDisplayName
	}
	if utf8.RuneCountInString(name) > service.MaxDisplayNameLength {
		return models.User{}, 0, jwt.ErrInvalidProfile
	}
	user := models.User{Email: email, DisplayName: name}
	outcome, revoked, err := b.Store.Bootstrap(ctx, &user, b.now())
	if err != nil {
		return models.User{}, 0, err
	}
	service.RecordRevokedSessions(ctx, b.Revocations, revoked)
	return user, outcome, nil
}

func (b *Bootstrapper) now() time.Time {
	if b.Now != nil {
		return b.Now().UTC()
	}
	return time.Now().UTC()
}
