package main

import "testing"

func TestTransportConfigurationPreservesComposeDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	for key, value := range map[string]string{
		"APP_ENV": "local", "DATABASE_URL": "postgresql://localhost/test", "KAFKA_BROKERS": "kafka:9092",
		"EXTERNAL_PORT": "", "INTERNAL_PORT": "", "RUN_MIGRATIONS": "",
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
	if cfg.transport.ExternalPort != "8080" || cfg.transport.InternalPort != "" || !cfg.runMigrations || cfg.frontendURL != "http://localhost:5173" {
		t.Fatalf("Compose defaults changed: %+v", cfg)
	}

	t.Setenv("EXTERNAL_PORT", "9000")
	t.Setenv("INTERNAL_PORT", "9001")
	t.Setenv("RUN_MIGRATIONS", "false")
	cfg, err = loadConfig()
	if err != nil || cfg.transport.ExternalPort != "9000" || cfg.transport.InternalPort != "9001" || cfg.runMigrations {
		t.Fatalf("separate listener configuration failed: %+v, %v", cfg, err)
	}
}

func TestTransportConfigurationRejectsInvalidPorts(t *testing.T) {
	t.Chdir(t.TempDir())
	for key, value := range map[string]string{
		"APP_ENV": "local", "DATABASE_URL": "postgresql://localhost/test", "KAFKA_BROKERS": "kafka:9092",
		"FRONTEND_BASE_URL": "", "JWT_PRIVATE_KEY_FILE": "", "SMTP_ADDR": "", "SMTP_FROM": "",
	} {
		t.Setenv(key, value)
	}
	for _, key := range []string{"EXTERNAL_PORT", "INTERNAL_PORT"} {
		t.Setenv(key, "70000")
		if _, err := loadConfig(); err == nil {
			t.Fatalf("invalid %s accepted", key)
		}
		t.Setenv(key, "")
	}
}
