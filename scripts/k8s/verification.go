package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func uniqueSuffix() string { return fmt.Sprintf("%x", rand.Text()[:8]) }

func (o *operator) requireManagedLocal() error {
	if o.existing || o.kubeContext != "kind-"+o.cluster || !o.isLocal() {
		return errors.New("smoke disruption is restricted to the managed local kind cluster")
	}
	return o.checkContext()
}

func (o *operator) cleanupKube(args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cleanup := *o
	cleanup.ctx = ctx
	if _, err := cleanup.run(
		"kubectl",
		append(cleanup.kubePrefix(), args...),
		commandOptions{capture: true, allowFailure: true},
	); err != nil {
		note("Smoke cleanup failed: %v", err)
	}
}

func (o *operator) applyJSON(value any) error {
	if err := o.prepareState(); err != nil {
		return err
	}
	file, err := os.CreateTemp(o.state, ".smoke-*.json")
	if err != nil {
		return err
	}
	defer removeTemporary(file.Name())
	err = json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return o.applyManifest(file.Name())
}

func smokePort(key string, fallback int) (int, error) {
	port, err := parsePort(key, os.Getenv(key))
	if err != nil {
		return 0, err
	}
	if port == 0 {
		port = fallback
	}
	return port, nil
}

func smokeImage() (string, error) {
	image := envDefault("K8S_SMOKE_IMAGE", "curlimages/curl:8.12.1")
	if !regexp.MustCompile(`^[a-zA-Z0-9._:/@-]+$`).MatchString(image) {
		return "", errors.New("K8S_SMOKE_IMAGE contains unsupported characters")
	}
	return image, nil
}

func (o *operator) makeSmokeClient(namespace, name, account string, injected bool) error {
	image, err := smokeImage()
	if err != nil {
		return err
	}
	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]string{
				"app":                     "foc-smoke",
				"sidecar.istio.io/inject": strconv.FormatBool(injected),
			},
			"annotations": map[string]string{"sidecar.istio.io/nativeSidecar": "true"},
		},
		"spec": map[string]any{
			"serviceAccountName":    account,
			"restartPolicy":         "Never",
			"activeDeadlineSeconds": 600,
			"containers": []any{
				map[string]any{
					"name":    "client",
					"image":   image,
					"command": []string{"sleep", "600"},
					"securityContext": map[string]any{
						"runAsNonRoot":             true,
						"runAsUser":                65532,
						"allowPrivilegeEscalation": false,
						"capabilities":             map[string]any{"drop": []string{"ALL"}},
					},
					"resources": map[string]any{
						"requests": map[string]string{"cpu": "10m", "memory": "16Mi"},
						"limits":   map[string]string{"memory": "64Mi"},
					},
				},
			},
		},
	}
	if err := o.applyJSON(manifest); err != nil {
		return err
	}
	return o.k("-n", namespace, "wait", "--for=condition=Ready", "pod/"+name, "--timeout="+o.timeout)
}

func (o *operator) clientRequest(namespace, pod, url string, expected int, body string, headers ...string) error {
	args := []string{
		"-n",
		namespace,
		"exec",
		pod,
		"-c",
		"client",
		"--",
		"curl",
		"-sS",
		"--max-time",
		"15",
		"-w",
		"\n%{http_code}",
		"-H",
		"Content-Type: application/json",
		"-H",
		"Connect-Protocol-Version: 1",
		"-d",
		body,
	}
	for _, header := range headers {
		args = append(args, "-H", header)
	}
	output, err := o.query(append(args, url)...)
	if err != nil {
		return err
	}
	output = strings.TrimRight(output, "\r\n")
	index := strings.LastIndex(output, "\n")
	if index < 0 {
		return errors.New("missing internal RPC status")
	}
	status := strings.TrimSpace(output[index+1:])
	if status != strconv.Itoa(expected) {
		return fmt.Errorf("%s/%s calling %s: expected HTTP %d, got %s", namespace, pod, url, expected, status)
	}
	if expected == 200 && strings.Contains(url, "GetPublicKeys") && !strings.Contains(output, "ES256") {
		return errors.New("internal key response lacks ES256 keys")
	}
	if expected == 404 && strings.Contains(url, "GetReservation") {
		var result struct{ Code string }
		if err := json.Unmarshal([]byte(output[:index]), &result); err != nil || result.Code != "not_found" {
			return errors.New("Order did not reach the reservation RPC's not_found response")
		}
	}
	note("PASS %s/%s internal RPC HTTP %d", namespace, pod, expected)
	return nil
}

func (o *operator) assertTransportBlocked(namespace, pod, url string) error {
	result, err := o.run(
		"kubectl",
		append(
			o.kubePrefix(),
			"-n",
			namespace,
			"exec",
			pod,
			"-c",
			"client",
			"--",
			"curl",
			"-sS",
			"--max-time",
			"10",
			"-o",
			"/dev/null",
			"-w",
			"%{http_code}",
			"-H",
			"Content-Type: application/json",
			"-d",
			"{}",
			url,
		),
		commandOptions{capture: true, allowFailure: true},
	)
	if err != nil {
		return err
	}
	if result.exitCode == 0 && strings.TrimSpace(result.stdout) == "403" {
		return nil
	}
	if contains([]string{"35", "52", "56"}, strconv.Itoa(result.exitCode)) {
		return nil
	}
	return fmt.Errorf(
		"expected explicit mesh rejection, got curl=%d HTTP=%s",
		result.exitCode,
		strings.TrimSpace(result.stdout),
	)
}

func (o *operator) meshSmoke() error {
	if err := o.requireManagedLocal(); err != nil {
		return err
	}
	for _, app := range apps {
		if err := o.rollout("deployment/" + app); err != nil {
			return err
		}
	}
	for _, dep := range []string{"postgres", "kafka"} {
		if err := o.rollout("statefulset/" + dep); err != nil {
			return err
		}
	}
	for _, job := range append([]string{"db-provision", "order-storage-init", "user-storage-init"}, jobs...) {
		if err := o.waitJob(job); err != nil {
			return err
		}
	}
	mode, err := o.query("-n", "foc", "get", "peerauthentication", "strict", "-o", "jsonpath={.spec.mtls.mode}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(mode) != "STRICT" {
		return errors.New("STRICT mTLS policy missing")
	}
	suffix := uniqueSuffix()
	outsider := "foc-untrusted-" + suffix
	client := "foc-smoke-" + suffix
	order := "foc-order-" + suffix
	if err := o.k("create", "namespace", outsider); err != nil {
		return err
	}
	defer o.cleanupKube("delete", "namespace", outsider, "--ignore-not-found", "--wait=false")
	defer o.cleanupKube("-n", "foc", "delete", "pod", client, order, "--ignore-not-found", "--wait=false")
	defer o.cleanupKube("-n", "istio-ingress", "delete", "pod", client, "--ignore-not-found", "--wait=false")
	for _, c := range []struct {
		ns, name, sa string
		injected     bool
	}{{"foc", client, "default", true}, {"foc", order, "order-service", true}, {"istio-ingress", client, "foc-public", true}, {outsider, "meshed", "default", true}, {outsider, "plaintext", "default", false}} {
		if err := o.makeSmokeClient(c.ns, c.name, c.sa, c.injected); err != nil {
			return err
		}
	}
	keyPath := "/user.v1.PublicKeyService/GetPublicKeys"
	keys := "http://user-service-internal.foc.svc.cluster.local:8080" + keyPath
	credit := "http://credit-service-internal.foc.svc.cluster.local:8080/grpc.health.v1.Health/Check"
	for _, tc := range []struct {
		ns, pod, url string
		status       int
	}{{"foc", client, keys, 200}, {"foc", client, "http://user-service-internal.foc.svc.cluster.local:8080/user.v1.HealthService/Check", 404}, {"istio-ingress", client, keys, 403}, {outsider, "meshed", keys, 403}, {"foc", order, credit, 200}, {"foc", client, credit, 403}, {"istio-ingress", client, credit, 403}, {outsider, "meshed", credit, 403}} {
		if err := o.clientRequest(tc.ns, tc.pod, tc.url, tc.status, "{}"); err != nil {
			return err
		}
	}
	if err := o.clientRequest(
		"foc",
		client,
		credit,
		403,
		"{}",
		"X-Forwarded-Client-Cert: URI=spiffe://cluster.local/ns/foc/sa/order-service",
		"Authorization: Bearer forged-workload-token",
	); err != nil {
		return err
	}
	var requestID [16]byte
	if _, err := rand.Read(requestID[:]); err != nil {
		return err
	}
	requestID[6] = (requestID[6] & 0x0f) | 0x40
	requestID[8] = (requestID[8] & 0x3f) | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", requestID[:4], requestID[4:6], requestID[6:8], requestID[8:10], requestID[10:])
	reservation := "http://credit-service-internal.foc.svc.cluster.local:8080/credit.v1.CreditReservationService/GetReservation"
	reservationBody := fmt.Sprintf(`{"requestId":%q}`, id)
	if err := o.clientRequest("foc", order, reservation, 404, reservationBody); err != nil {
		return err
	}
	for _, caller := range [][2]string{{"foc", client}, {"istio-ingress", client}, {outsider, "meshed"}} {
		if err := o.clientRequest(caller[0], caller[1], reservation, 403, reservationBody); err != nil {
			return err
		}
	}
	if err := o.assertTransportBlocked(outsider, "plaintext", reservation); err != nil {
		return err
	}
	userPod, err := o.query(
		"-n",
		"foc",
		"get",
		"pod",
		"-l",
		"app=user-service",
		"-o",
		"jsonpath={.items[0].metadata.name}",
	)
	if err != nil {
		return err
	}
	userPod = strings.TrimSpace(userPod)
	userIP, err := o.query("-n", "foc", "get", "pod", userPod, "-o", "jsonpath={.status.podIP}")
	if err != nil {
		return err
	}
	for _, c := range [][2]string{{"foc", client}, {outsider, "meshed"}} {
		if err := o.assertTransportBlocked(
			c[0],
			c[1],
			"http://"+strings.TrimSpace(userIP)+":8081"+keyPath,
		); err != nil {
			return err
		}
	}
	if err := o.assertTransportBlocked(outsider, "plaintext", keys); err != nil {
		return err
	}
	config, err := o.query(
		"-n",
		"foc",
		"exec",
		userPod,
		"-c",
		"istio-proxy",
		"--",
		"pilot-agent",
		"request",
		"GET",
		"config_dump",
	)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`"(require_client_certificate|requireClientCertificate)"\s*:\s*true`).MatchString(config) {
		return errors.New("proxy config does not require client certificates")
	}
	port, err := smokePort("K8S_SMOKE_HTTPS_PORT", 18443)
	if err != nil {
		return err
	}
	gateway := forwardSpec{
		name:       "smoke gateway",
		namespace:  "istio-ingress",
		service:    "foc-public-istio",
		localPort:  port,
		remotePort: 443,
		scheme:     "https",
	}
	if err := preflightForwards([]forwardSpec{gateway}); err != nil {
		return err
	}
	child, err := o.startForward(gateway)
	if err != nil {
		return err
	}
	defer stopForwards([]*portForward{child})
	if _, err := o.gatewaySmokeRequest(gateway, "/", 200, false); err != nil {
		return err
	}
	for _, path := range publicHealthPaths {
