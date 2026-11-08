package main

import (
	"strings"
	"testing"
)

func TestLoadConfigWorkerConcurrency(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr string
	}{
		{name: "default when unset", value: "", want: 2},
		{name: "explicit value", value: "4", want: 4},
		{name: "whitespace padded", value: " 3 ", want: 3},
		{name: "zero rejected", value: "0", wantErr: "positive integer"},
		{name: "negative rejected", value: "-1", wantErr: "positive integer"},
		{name: "non numeric rejected", value: "abc", wantErr: "positive integer"},
		{name: "float rejected", value: "2.5", wantErr: "positive integer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadConfig(func(key string) string {
				switch key {
				case "CMS_WORKER_CONCURRENCY":
					return tt.value
				case "INTERNAL_PORT":
					return "8081"
				case "DATABASE_URL":
					return "postgresql://localhost/test"
				case "USER_SERVICE_URL":
					return "http://localhost"
				default:
					return ""
				}
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if cfg.WorkerConcurrency != tt.want {
				t.Fatalf("WorkerConcurrency = %d, want %d", cfg.WorkerConcurrency, tt.want)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidDurations(t *testing.T) {
	for _, name := range []string{
		"CMS_LLM_REQUEST_TIMEOUT", "CMS_JOB_LEASE", "CMS_JOB_HEARTBEAT_INTERVAL",
		"CMS_JOB_RETRY_INITIAL_DELAY", "CMS_JOB_RETRY_MAX_DELAY", "CMS_JOB_POLL_INTERVAL",
	} {
		for _, value := range []string{"invalid", "0s", "-1s"} {
			t.Run(name+"/"+value, func(t *testing.T) {
				_, err := loadConfig(func(key string) string {
					switch key {
					case name:
						return value
					case "INTERNAL_PORT":
						return "8081"
					case "DATABASE_URL":
						return "postgresql://localhost/test"
					case "USER_SERVICE_BASE_URL":
						return "http://localhost"
					default:
						return ""
					}
				})
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("error = %v, want invalid %s", err, name)
				}
			})
		}
	}
}
