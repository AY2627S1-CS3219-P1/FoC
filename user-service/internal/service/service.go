package service

import (
	"errors"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth"
)

type Service struct {
	deps auth.Dependencies
	cfg  auth.Config
}

func NewService(deps auth.Dependencies, cfg auth.Config) (*Service, error) {
	base := cfg.FrontendBaseURL
	if base.Host == "" || base.User != nil || base.Opaque != "" ||
		(base.Scheme != "https" && base.Scheme != "http") || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("invalid frontend base URL")
	}
	if !cfg.LocalDevelopment && base.Scheme != "https" {
		return nil, errors.New("frontend base URL must use HTTPS outside local mode")
	}
	if cfg.AccessTokenTTL == 0 {
		cfg.AccessTokenTTL = AccessTokenLifetime
	}
	if cfg.RefreshTokenTTL == 0 {
		cfg.RefreshTokenTTL = RefreshTokenLifetime
	}
	if cfg.AccessTokenTTL < time.Second || cfg.RefreshTokenTTL < time.Second {
		return nil, errors.New("JWT token lifetimes must be at least one second")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{deps: deps, cfg: cfg}, nil
}
