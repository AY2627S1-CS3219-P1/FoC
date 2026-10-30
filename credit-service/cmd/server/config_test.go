package main

import (
	"slices"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":          "postgres://credit:credit@localhost:5432/credit_dev",
		"USER_SERVICE_BASE_URL": "https://user.example",
		"INTERNAL_PORT":         "8081",
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	c, err := loadConfig(env(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Transport.ExternalPort != "8080" || c.Transport.InternalPort != "8081" ||
		!c.RunMigrations || c.KafkaBrokers != nil {
		t.Fatalf("config %+v", c)
	}
}

func TestLoadConfigReadsKafkaBrokers(t *testing.T) {
	values := validEnv()
	values["KAFKA_BROKERS"] = "kafka-1:9092,kafka-2:9092"

	c, err := loadConfig(env(values))
	if err != nil || !slices.Equal(c.KafkaBrokers, []string{"kafka-1:9092", "kafka-2:9092"}) {
		t.Fatalf("brokers %v: %v", c.KafkaBrokers, err)
	}
}

func TestLoadConfigRejectsInvalidSettings(t *testing.T) {
	tests := map[string]map[string]string{
		"shared port":       {"PORT": "9000", "INTERNAL_PORT": "9000"},
		"missing internal":  {"INTERNAL_PORT": ""},
		"bad external port": {"PORT": "70000"},
		"bad internal port": {"INTERNAL_PORT": "70000"},
		"missing database":  {"DATABASE_URL": ""},
		"bad user scheme":   {"USER_SERVICE_BASE_URL": "ftp://user-service:8080"},
		"user service path": {"USER_SERVICE_BASE_URL": "https://user.example/api"},
		"migrations flag":   {"RUN_MIGRATIONS": "sometimes"},
	}
	for name, overrides := range tests {
		t.Run(name, func(t *testing.T) {
			values := validEnv()
			for k, v := range overrides {
				values[k] = v
			}
			if _, err := loadConfig(env(values)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestLoadConfigAllowsHTTPServiceURLs(t *testing.T) {
	values := validEnv()
	values["APP_ENV"] = "production"
	values["USER_SERVICE_BASE_URL"] = "http://user-service:8080"

	if _, err := loadConfig(env(values)); err != nil {
		t.Fatal(err)
	}
}
