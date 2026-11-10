package main

import (
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func baseEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":          "postgresql://foc:pw@postgres:5432/moderation_dev?sslmode=disable",
		"USER_SERVICE_BASE_URL": "http://user-service:8081",
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	c, err := loadConfig(env(baseEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "8080" || c.InternalPort != "8081" || !c.RunMigrations || c.KafkaBrokers != nil || c.S3Bucket != "" {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestLoadConfigReadsEveryOption(t *testing.T) {
	values := baseEnv()
	values["PORT"], values["INTERNAL_PORT"] = "9000", "9001"
	values["ORDER_SERVICE_BASE_URL"] = "http://order-service:8081"
	values["RUN_MIGRATIONS"] = "false"
	values["KAFKA_BROKERS"] = "kafka:9092, other:9092"
	values["MODERATION_S3_BUCKET"] = "moderation-images"
	values["MODERATION_S3_UPLOAD_TTL"] = "5m"

	c, err := loadConfig(env(values))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "9000" || c.RunMigrations || len(c.KafkaBrokers) != 2 || c.KafkaBrokers[1] != "other:9092" ||
		c.S3Bucket != "moderation-images" || c.S3UploadTTL != 5*time.Minute ||
		c.OrderServiceURL != "http://order-service:8081" {
		t.Fatalf("config = %+v", c)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	for name, change := range map[string]func(map[string]string){
		"missing database":   func(v map[string]string) { delete(v, "DATABASE_URL") },
		"missing user url":   func(v map[string]string) { delete(v, "USER_SERVICE_BASE_URL") },
		"user url with path": func(v map[string]string) { v["USER_SERVICE_BASE_URL"] = "http://user-service:8081/x" },
		"bad order url":      func(v map[string]string) { v["ORDER_SERVICE_BASE_URL"] = "order-service" },
		"same ports":         func(v map[string]string) { v["PORT"], v["INTERNAL_PORT"] = "8080", "8080" },
		"port out of range":  func(v map[string]string) { v["PORT"] = "70000" },
		"bad migrations":     func(v map[string]string) { v["RUN_MIGRATIONS"] = "maybe" },
		"bad ttl":            func(v map[string]string) { v["MODERATION_S3_DOWNLOAD_TTL"] = "-1s" },
	} {
		t.Run(name, func(t *testing.T) {
			values := baseEnv()
			change(values)
			if _, err := loadConfig(env(values)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
