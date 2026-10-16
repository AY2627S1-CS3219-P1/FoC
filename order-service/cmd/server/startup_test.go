package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
)

func TestAuthenticationRetryCancelsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	started := time.Now()
	_, err := newAuthenticator(ctx, time.Hour, func(context.Context) (*httpauth.Authenticator, error) {
		calls++
		cancel()
		return nil, errors.New("keys unavailable")
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d, err=%v", calls, err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("retry did not stop promptly")
	}
}

func TestAuthenticationRetryPropagatesDeadlineToAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := newAuthenticator(ctx, time.Hour, func(attempt context.Context) (*httpauth.Authenticator, error) {
		if _, ok := attempt.Deadline(); !ok {
			t.Fatal("attempt lacks deadline")
		}
		<-attempt.Done()
		return nil, attempt.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}

func TestAuthenticationRetrySucceeds(t *testing.T) {
	calls := 0
	want := new(httpauth.Authenticator)
	got, err := newAuthenticator(context.Background(), time.Nanosecond, func(context.Context) (*httpauth.Authenticator, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("not ready")
		}
		return want, nil
	})
	if err != nil || got != want || calls != 2 {
		t.Fatalf("calls=%d, result=%p, err=%v", calls, got, err)
	}
}

func TestConfiguration(t *testing.T) {
	valid := map[string]string{"DATABASE_URL": "postgresql://foc:password@localhost:5432/order_dev", "USER_SERVICE_BASE_URL": "http://localhost:8081", "APP_ENV": "local"}
	for _, tc := range []struct{ name, key, value string }{
		{"missing database", "DATABASE_URL", ""},
		{"invalid database", "DATABASE_URL", "broken"},
		{"missing auth", "USER_SERVICE_BASE_URL", ""},
		{"auth path", "USER_SERVICE_BASE_URL", "http://localhost/api"},
		{"auth credentials", "USER_SERVICE_BASE_URL", "http://user:secret@localhost"},
		{"unsafe production auth", "APP_ENV", "production"},
		{"invalid port", "PORT", "-1"},
		{"invalid migration toggle", "RUN_MIGRATIONS", "sometimes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string {
				if key == tc.key {
					return tc.value
				}
				return valid[key]
			}
			if _, err := loadConfig(getenv); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	c, err := loadConfig(func(key string) string { return valid[key] })
	if err != nil || c.Port != "8080" || !c.RunMigrations {
		t.Fatalf("config=%+v, err=%v", c, err)
	}
	c, err = loadConfig(func(key string) string {
		if key == "RUN_MIGRATIONS" {
			return "false"
		}
		return valid[key]
	})
	if err != nil || c.RunMigrations {
		t.Fatalf("config=%+v, err=%v", c, err)
	}
}

func TestCreationConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]string
		valid  bool
	}{
		{"disabled without dependencies", map[string]string{}, true},
		{"enabled missing dependencies", map[string]string{"ORDER_CREATION_ENABLED": "true"}, false},
		{"invalid toggle", map[string]string{"ORDER_CREATION_ENABLED": "perhaps"}, false},
		{"URL without token", map[string]string{"CREDIT_SERVICE_BASE_URL": "http://credit:8080"}, false},
		{"token without URL", map[string]string{"CREDIT_SERVICE_TOKEN_FILE": "/run/secrets/credit"}, false},
		{"enabled local", map[string]string{"ORDER_CREATION_ENABLED": "true", "CREDIT_SERVICE_BASE_URL": "http://credit:8080", "CREDIT_SERVICE_TOKEN_FILE": "/run/secrets/credit", "SUPPLIER_SERVICE_BASE_URL": "http://supplier:8080"}, true},
		{"recovery with admission disabled", map[string]string{"CREDIT_SERVICE_BASE_URL": "http://credit:8080", "CREDIT_SERVICE_TOKEN_FILE": "/run/secrets/credit"}, true},
		{"no credential URL", map[string]string{"CREDIT_SERVICE_BASE_URL": "http://user:secret@credit:8080", "CREDIT_SERVICE_TOKEN_FILE": "/run/secrets/credit"}, false},
		{"no service path", map[string]string{"SUPPLIER_SERVICE_BASE_URL": "http://supplier:8080/rpc"}, false},
		{"HTTPS outside local", map[string]string{"APP_ENV": "production", "USER_SERVICE_BASE_URL": "https://user.example", "CREDIT_SERVICE_BASE_URL": "http://credit:8080", "CREDIT_SERVICE_TOKEN_FILE": "/run/secrets/credit"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := map[string]string{"DATABASE_URL": "postgresql://order:example@localhost:5432/order_test", "USER_SERVICE_BASE_URL": "http://user:8080", "APP_ENV": "local"}
			for key, value := range tc.values {
				base[key] = value
			}
			_, err := loadConfig(func(key string) string { return base[key] })
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
