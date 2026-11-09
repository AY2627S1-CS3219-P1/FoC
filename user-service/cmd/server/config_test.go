package main

import "testing"

func TestTransportConfigurationPreservesComposeDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	for key, value := range map[string]string{
		"APP_ENV": "local", "DATABASE_URL": "postgresql://localhost/test", "KAFKA_BROKERS": "kafka:9092",
		"PORT": "", "INTERNAL_PORT": "8081", "RUN_MIGRATIONS": "",
		"FRONTEND_BASE_URL": "", "JWT_PRIVATE_KEY_FILE": "",
		"SMTP_ADDR": "", "SMTP_FROM": "", "EMAIL_PROVIDER": "",
		"DB_MAX_OPEN": "", "DB_MAX_IDLE": "", "SMTP_TIMEOUT": "",
		"JWT_ACCESS_TOKEN_TTL": "", "JWT_REFRESH_TOKEN_TTL": "",
	} {
		t.Setenv(key, value)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.port != "8080" || cfg.internalPort != "8081" || !cfg.runMigrations ||
		cfg.frontendURL != "http://localhost:5173" {
		t.Fatalf("Compose defaults changed: %+v", cfg)
	}

	t.Setenv("PORT", "9000")
	t.Setenv("INTERNAL_PORT", "9001")
	t.Setenv("RUN_MIGRATIONS", "false")
	cfg, err = loadConfig()
	if err != nil || cfg.port != "9000" || cfg.internalPort != "9001" || cfg.runMigrations {
		t.Fatalf("separate listener configuration failed: %+v, %v", cfg, err)
	}
}

func TestTransportConfigurationRejectsInvalidPorts(t *testing.T) {
	t.Chdir(t.TempDir())
	for key, value := range map[string]string{
		"APP_ENV": "local", "DATABASE_URL": "postgresql://localhost/test", "KAFKA_BROKERS": "kafka:9092",
		"FRONTEND_BASE_URL": "", "JWT_PRIVATE_KEY_FILE": "", "SMTP_ADDR": "", "SMTP_FROM": "", "EMAIL_PROVIDER": "",
	} {
		t.Setenv(key, value)
	}
	for _, key := range []string{"PORT", "INTERNAL_PORT"} {
		t.Setenv(key, "70000")
		if _, err := loadConfig(); err == nil {
			t.Fatalf("invalid %s accepted", key)
		}
		t.Setenv(key, "")
	}
}

// SES is opt-in and must not require the SMTP login it replaces.
func TestSESConfigurationDoesNotRequireSMTP(t *testing.T) {
	t.Chdir(t.TempDir())
	setValidConfigEnv(t)
	t.Setenv("KAFKA_BROKERS", "kafka:9092")
	t.Setenv("EMAIL_PROVIDER", "ses")
	t.Setenv("EMAIL_FROM", "sender@example.test")
	t.Setenv("AWS_REGION", "ap-southeast-1")
	t.Setenv("SMTP_ADDR", "")
	t.Setenv("SMTP_FROM", "")
	t.Setenv("SMTP_TIMEOUT", "unused-invalid-value")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.emailProvider != "ses" || cfg.emailFrom != "sender@example.test" || cfg.awsRegion != "ap-southeast-1" {
		t.Fatal("SES configuration was not selected")
	}
	for key, value := range map[string]string{
		"EMAIL_PROVIDER": "unknown", "EMAIL_FROM": "invalid", "AWS_REGION": "", "SES_TIMEOUT": "-1s",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := loadConfig(); err == nil {
				t.Fatalf("invalid %s accepted", key)
			}
		})
	}
}
