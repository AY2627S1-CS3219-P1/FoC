package config

import (
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	HTTPAddr      string `env:"HTTP_ADDR" envDefault:":8080"`
	DatabaseURL   string `env:"DATABASE_URL,required"`
	RunMigrations bool   `env:"RUN_MIGRATIONS" envDefault:"true"`
	DBMaxOpen     int    `env:"DB_MAX_OPEN_CONNS" envDefault:"25"`
	DBMaxIdle     int    `env:"DB_MAX_IDLE_CONNS" envDefault:"25"`

	Auth AuthConfig
	Mail MailConfig
}

type AuthConfig struct {
	// Frontend base URL used to build magic links, e.g. https://app.example.com
	AppBaseURL   string        `env:"APP_BASE_URL" envDefault:"http://localhost:5173"`
	SessionTTL   time.Duration `env:"SESSION_TTL" envDefault:"720h"` // 30 days
	CookieName   string        `env:"SESSION_COOKIE_NAME" envDefault:"session"`
	CookieSecure bool          `env:"SESSION_COOKIE_SECURE" envDefault:"true"`
	// Max magic links per 10 minutes.
	LinkLimitPerEmail int `env:"LINK_LIMIT_PER_EMAIL" envDefault:"5"`
	LinkLimitPerIP    int `env:"LINK_LIMIT_PER_IP" envDefault:"20"`
}

type MailConfig struct {
	SMTPAddr string `env:"SMTP_ADDR" envDefault:"localhost:1025"`
	From     string `env:"MAIL_FROM" envDefault:"no-reply@localhost"`
	Username string `env:"SMTP_USERNAME"`
	Password string `env:"SMTP_PASSWORD"`
}

func Load() (Config, error) {
	var c Config
	err := env.Parse(&c)
	return c, err
}
