package main

import (
	"strings"
	"testing"
	"time"
)

func setEmailConfigEnv(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{
		"DATABASE_URL": "postgres://localhost/test", "INTERNAL_PORT": "8081", "PORT": "8080",
		"EMAIL_PROVIDER": "", "EMAIL_FROM": "", "AWS_REGION": "", "SES_TIMEOUT": "",
		"SMTP_ADDR": "", "SMTP_FROM": "", "SMTP_TIMEOUT": "",
	} {
		t.Setenv(key, value)
	}
}

func TestSESConfigurationDoesNotRequireSMTP(t *testing.T) {
	setEmailConfigEnv(t)
	t.Setenv("EMAIL_PROVIDER", "ses")
	t.Setenv("EMAIL_FROM", "sender@example.test")
	t.Setenv("AWS_REGION", "ap-southeast-1")
	t.Setenv("SMTP_TIMEOUT", "unused-invalid-value")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.emailProvider != "ses" || cfg.emailFrom != "sender@example.test" ||
		cfg.awsRegion != "ap-southeast-1" || cfg.sesTimeout != 5*time.Second {
		t.Fatalf("SES configuration was not selected: %+v", cfg)
	}
	for _, tc := range []struct{ key, value string }{
		{"EMAIL_PROVIDER", "unknown"}, {"EMAIL_FROM", "invalid"}, {"EMAIL_FROM", ""},
		{"EMAIL_FROM", "sender@example.test\r\nBcc: other@example.test"}, {"AWS_REGION", ""},
		{"SES_TIMEOUT", "-1s"}, {"SES_TIMEOUT", "0s"}, {"SES_TIMEOUT", "invalid"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("invalid %s accepted: %v", tc.key, err)
			}
		})
	}
	t.Setenv("SES_TIMEOUT", "2s")
	if cfg, err := loadConfig(); err != nil || cfg.sesTimeout != 2*time.Second {
		t.Fatalf("SES timeout was not applied: %+v, %v", cfg, err)
	}
}

func TestSMTPRemainsDefaultAndOptional(t *testing.T) {
	setEmailConfigEnv(t)
	t.Setenv("SES_TIMEOUT", "unused-invalid-value")
	cfg, err := loadConfig()
	if err != nil || cfg.emailProvider != "smtp" || cfg.smtp.Addr != "" {
		t.Fatalf("optional SMTP defaults changed: %+v, %v", cfg, err)
	}
	t.Setenv("SMTP_ADDR", "mailpit:1025")
	if _, err := loadConfig(); err == nil {
		t.Fatal("SMTP enabled without a sender")
	}
	t.Setenv("SMTP_FROM", "FoC <no-reply@foc.local>")
	t.Setenv("SMTP_TIMEOUT", "2s")
	if cfg, err := loadConfig(); err != nil || cfg.smtp.Timeout != 2*time.Second {
		t.Fatalf("SMTP configuration changed: %+v, %v", cfg, err)
	}
	t.Setenv("SMTP_TIMEOUT", "0s")
	if _, err := loadConfig(); err == nil {
		t.Fatal("non-positive SMTP timeout accepted")
	}
}
