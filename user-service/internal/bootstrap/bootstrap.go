// Package bootstrap creates the first super_admin from operator-supplied
// configuration. Persistence and concurrency live in store.Admin.Bootstrap.
package bootstrap

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
)

const DefaultDisplayName = "Admin"

type Store interface {
	Bootstrap(context.Context, *models.User) (store.BootstrapOutcome, error)
}

type Config struct {
	Email       string
	DisplayName string
}

type Bootstrapper struct {
	Store Store
}

// Run validates cfg and bootstraps the super_admin. The operator is trusted,
// so the registration domain whitelist is not applied.
func (b *Bootstrapper) Run(ctx context.Context, cfg Config) (models.User, store.BootstrapOutcome, error) {
	email, err := service.NormalizeEmail(cfg.Email)
	if err != nil {
		return models.User{}, 0, err
	}
	name := strings.TrimSpace(cfg.DisplayName)
	if name == "" {
		name = DefaultDisplayName
	}
	if utf8.RuneCountInString(name) > 100 {
		return models.User{}, 0, jwt.ErrInvalidProfile
	}
	user := models.User{Email: email, DisplayName: name}
	outcome, err := b.Store.Bootstrap(ctx, &user)
	if err != nil {
		return models.User{}, 0, err
	}
	return user, outcome, nil
}
