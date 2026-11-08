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
