package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The requested deployment contract is one HTTPS origin, /grafana/ and /kafka/,
// with login retained and no exposure of the logging API or Kafka broker.
func TestAWSAdminSubpaths(t *testing.T) {
	if os.Getenv("K8S_RENDER_TESTS") != "1" {
		t.Skip("set K8S_RENDER_TESTS=1 with pinned Kubernetes tools")
	}
	a, directory, _ := testApplication(t)
	config := testConfiguration()
	config["public_hostname"] = "demo.execute-api.ap-southeast-1.amazonaws.com"
	overlay := filepath.Join(directory, "overlay")
	if err := a.renderOverlay(config, overlay, "-----BEGIN CERTIFICATE-----\nOFFLINE\n"); err != nil {
		t.Fatal(err)
	}
	data, err := exec.Command("kubectl", "kustomize", overlay).CombinedOutput()
	if err != nil {
		t.Fatalf("kustomize: %s", data)
	}
	objects := decodeManifests(t, string(data))
	route := namedManifest(t, objects, "HTTPRoute", "admin-uis")
	assertJSONContract(t, object(route["metadata"])["namespace"], "foc", "route namespace")
	assertJSONContract(t, object(route["spec"])["rules"], []any{
		map[string]any{
			"matches": []any{map[string]any{"path": map[string]any{"type": "Exact", "value": "/grafana"}}},
			"filters": []any{map[string]any{"type": "RequestRedirect", "requestRedirect": map[string]any{
				"scheme": "https", "port": 443,
				"path": map[string]any{"type": "ReplaceFullPath", "replaceFullPath": "/grafana/"}, "statusCode": 301,
			}}},
		},
		map[string]any{
			"matches":     []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/grafana/"}}},
			"backendRefs": []any{map[string]any{"name": "grafana", "namespace": "foc-observability", "port": 3000}},
		},
		map[string]any{
			"matches": []any{map[string]any{"path": map[string]any{"type": "Exact", "value": "/kafka"}}},
			"filters": []any{map[string]any{"type": "RequestRedirect", "requestRedirect": map[string]any{
				"scheme": "https", "port": 443,
				"path": map[string]any{"type": "ReplaceFullPath", "replaceFullPath": "/kafka/"}, "statusCode": 301,
			}}},
		},
		map[string]any{
			"matches":     []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/kafka/"}}},
			"backendRefs": []any{map[string]any{"name": "kafka-ui", "port": 8080}},
		},
	}, "admin route allowlist")
	grant := namedManifest(t, objects, "ReferenceGrant", "grafana-from-app-route")
	assertJSONContract(t, object(grant["spec"])["to"], []any{
		map[string]any{"group": "", "kind": "Service", "name": "grafana"},
	}, "cross-namespace grant limited to Grafana")
	for _, check := range []struct {
		deployment string
		want       map[string]string
	}{
		{"grafana", map[string]string{
			"GF_SERVER_ROOT_URL": "https://demo.execute-api.ap-southeast-1.amazonaws.com/grafana/", "GF_SERVER_SERVE_FROM_SUB_PATH": "true",
			"GF_AUTH_ANONYMOUS_ENABLED": "false", "GF_USERS_ALLOW_SIGN_UP": "false",
			"GF_SECURITY_COOKIE_SECURE": "true", "GF_AUTH_LOGIN_COOKIE_NAME": "foc_grafana_session",
			"GF_LIVE_MAX_CONNECTIONS": "0",
		}},
		{"kafka-ui", map[string]string{
			"AUTH_TYPE": "LOGIN_FORM", "DYNAMIC_CONFIG_ENABLED": "false", "SERVER_SERVLET_CONTEXT_PATH": "/kafka",
			"SERVER_REACTIVE_SESSION_COOKIE_PATH": "/kafka", "SERVER_REACTIVE_SESSION_COOKIE_SECURE": "true",
			"SERVER_REACTIVE_SESSION_COOKIE_NAME": "foc_kafka_session", "SERVER_FORWARD_HEADERS_STRATEGY": "framework",
		}},
	} {
		deployment := namedManifest(t, objects, "Deployment", check.deployment)
		container := object(array(podSpec(deployment)["containers"])[0])
		env := map[string]string{}
		for _, entry := range array(container["env"]) {
			item := object(entry)
			env[stringValue(item, "name")] = stringValue(item, "value")
		}
		for key, want := range check.want {
			if env[key] != want {
				t.Errorf("%s %s = %q, want %q", check.deployment, key, env[key], want)
			}
		}
	}
	kafka := namedManifest(t, objects, "Deployment", "kafka-ui")
	assertJSONContract(t, object(kafka["spec"])["strategy"], map[string]any{
		"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": 0, "maxSurge": 1},
	}, "Kafka UI availability during rollout")
	container := object(array(podSpec(kafka)["containers"])[0])
	assertJSONContract(t, object(container["readinessProbe"])["exec"], map[string]any{
		"command": []any{"wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/kafka/actuator/health"},
	}, "Kafka UI readiness checks its loopback-only application listener")
	for _, name := range []string{"gateway-grafana", "gateway-kafka-ui"} {
		policy := namedManifest(t, objects, "AuthorizationPolicy", name)
		assertJSONContract(t, object(array(object(policy["spec"])["rules"])[0])["from"], []any{
			map[string]any{
				"source": map[string]any{"principals": []any{"cluster.local/ns/istio-ingress/sa/foc-public"}},
			},
		}, "only gateway identity may enter admin UIs")
	}
	local, err := exec.Command("kubectl", "kustomize", filepath.Join(a.root, "infra/k8s/overlays/local")).
		CombinedOutput()
	if err != nil {
		t.Fatalf("local kustomize: %s", local)
	}
	for _, manifest := range decodeManifests(t, string(local)) {
		if manifest["kind"] == "HTTPRoute" && stringValue(object(manifest["metadata"]), "name") == "admin-uis" {
			t.Fatal("AWS admin exposure leaked into local deployments")
		}
	}
}
