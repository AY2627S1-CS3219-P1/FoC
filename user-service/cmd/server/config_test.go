package main

import (
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/server"
)

func TestTransportConfigurationPreservesComposeDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	for key, value := range map[string]string{
		"APP_ENV": "local", "DATABASE_URL": "postgresql://localhost/test", "KAFKA_BROKERS": "kafka:9092",
		"PORT": "", "RPC_MODE": "", "RUN_MIGRATIONS": "",
		"FRONTEND_BASE_URL": "", "JWT_PRIVATE_KEY_FILE": "",
		"SMTP_ADDR": "", "SMTP_FROM": "",
		"DB_MAX_OPEN": "", "DB_MAX_IDLE": "", "SMTP_TIMEOUT": "",
		"JWT_ACCESS_TOKEN_TTL": "", "JWT_REFRESH_TOKEN_TTL": "",
	} {
		t.Setenv(key, value)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.transport.Mode != server.Single || cfg.port != "8080" || !cfg.runMigrations || cfg.frontendURL != "http://localhost:5173" {
		t.Fatal("Compose defaults changed")
	}
	t.Setenv("RPC_MODE", "mesh")
	t.Setenv("RUN_MIGRATIONS", "false")
	cfg, err = loadConfig()
	if err != nil || cfg.transport.Mode != server.Mesh || cfg.runMigrations {
		t.Fatalf("mesh configuration failed: %v", err)
	}
	t.Setenv("RPC_MODE", "typo")
	if _, err := loadConfig(); err == nil {
		t.Fatal("invalid RPC_MODE accepted")
	}
}
