package main

import (
	"fmt"
	"testing"
)

func TestApplicationSMTPSecretFollowsRenderedReference(t *testing.T) {
	for _, tc := range []struct {
		name, namespace, env string
		required, invalid    bool
	}{
		{"SMTP", "foc", `[{"secretRef":{"name":"smtp"}}]`, true, false},
		{"SES", "foc", `[{"configMapRef":{"name":"foc-config"}},{"secretRef":{"name":"user-db"}}]`, false, false},
		{"optional", "foc", `[{"secretRef":{"name":"smtp","optional":true}}]`, false, false},
		{"wrong namespace", "other", `[]`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rendered := fmt.Sprintf(
				`{"kind":"List","items":[{"kind":"Deployment","metadata":{"name":"user-service","namespace":%q},"spec":{"template":{"spec":{"containers":[{"envFrom":%s}]}}}}]}`,
				tc.namespace,
				tc.env,
			)
			got, err := applicationRequiresSMTP(rendered)
			if (err != nil) != tc.invalid || got != tc.required {
				t.Fatalf("required=%v error=%v", got, err)
			}
		})
	}
	for _, invalid := range []string{`{`, `{"kind":"List","items":[]}`} {
		if _, err := applicationRequiresSMTP(invalid); err == nil {
			t.Fatal("invalid application manifest accepted")
		}
	}
}
