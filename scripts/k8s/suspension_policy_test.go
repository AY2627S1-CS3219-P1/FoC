package main

import (
	"io"
	"os"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Source: only User Service may invoke suspension coordination. The current
// namespace-wide ALLOW requires an overriding DENY, not another ALLOW rule.
// This checks the manifest contract; live mTLS enforcement needs k8s-smoke.
func TestSuspensionPolicyRequiresUserWorkloadIdentity(t *testing.T) {
	file, err := os.Open("../../infra/k8s/base/dependencies/security.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	decoder := yaml.NewDecoder(file)
	for {
		var document struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec map[string]any `yaml:"spec"`
		}
		if err := decoder.Decode(&document); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if document.Kind != "AuthorizationPolicy" || document.Metadata.Name != "order-suspension-user-only" {
			continue
		}
		expected := map[string]any{
			"action":   "DENY",
			"selector": map[string]any{"matchLabels": map[string]any{"app": "order-service"}},
			"rules": []any{
				map[string]any{
					"from": []any{
						map[string]any{
							"source": map[string]any{"notPrincipals": []any{"cluster.local/ns/foc/sa/user-service"}},
						},
					},
					"to": []any{map[string]any{"operation": map[string]any{
						"ports": []any{"8081"}, "paths": []any{"/order.v1.OrderInternalService/ApplyAccountSuspension"},
					}}},
				},
			},
		}
		if !reflect.DeepEqual(document.Spec, expected) {
			t.Fatalf("User-only suspension boundary changed: %#v", document.Spec)
		}
		return
	}
	t.Fatal("User-only suspension policy missing")
}
