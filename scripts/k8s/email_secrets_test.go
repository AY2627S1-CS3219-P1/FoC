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
		{"SES", "foc", `[{"configMapRef":{"name":"foc-config"}},{"secretRef":{"name":"communication-db"}}]`, false, false},
		{"optional", "foc", `[{"secretRef":{"name":"smtp","optional":true}}]`, false, false},
		{"wrong namespace", "other", `[]`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rendered := fmt.Sprintf(
				`{"kind":"List","items":[{"kind":"Deployment","metadata":{"name":"communication-service","namespace":%q},"spec":{"template":{"spec":{"containers":[{"envFrom":%s}]}}}}]}`,
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

// The pinned kubectl emits separate JSON objects for multi-resource kustomizations.
func TestApplicationSMTPSecretAcceptsKubectlJSONStream(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		required  bool
	}{
		{"SMTP", `[{"secretRef":{"name":"smtp"}}]`, true},
		{"SES", `[{"configMapRef":{"name":"foc-config"}},{"secretRef":{"name":"communication-db"}}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := fmt.Sprintf(
				`{"kind":"Deployment","metadata":{"name":"communication-service","namespace":"foc"},"spec":{"template":{"spec":{"containers":[{"envFrom":%s}]}}}}`,
				tc.env,
			)
			for _, input := range []string{user, `{"kind":"Service","metadata":{"name":"frontend"}}` + "\n" + user + "\n" + `{"kind":"Deployment","metadata":{"name":"frontend"}}`} {
				required, err := applicationRequiresSMTP(input)
				if err != nil || required != tc.required {
					t.Fatalf("required=%v error=%v", required, err)
				}
			}
			if _, err := applicationRequiresSMTP(user + "\n{"); err == nil {
				t.Fatal("malformed trailing document accepted")
			}
		})
	}
}
