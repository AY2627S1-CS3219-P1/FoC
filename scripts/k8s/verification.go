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
		if err := o.assertPublicHealth(gateway, path); err != nil {
			return err
		}
	}
	// A protected RPC must reach Credit's public listener, not the frontend catchall.
	if _, err := o.gatewaySmokeRequest(
		gateway,
		"/credit.v1.CreditAdminService/GetCreditTotals",
		401,
		true,
	); err != nil {
		return err
	}
	for _, privatePath := range []string{"/loki/api/v1/labels", "/foc-loki", "/foc-loki/secret"} {
		if _, err := o.gatewaySmokeRequest(gateway, privatePath, 404, false); err != nil {
			return err
		}
	}
	if _, err := o.gatewaySmokeRequest(gateway, "/user.review.v1.ReviewService/ListMyReviews", 401, true); err != nil {
		return err
	}
	// Unrouted RPCs reach the frontend catchall, whose 404 can contain HTML.
	// The status proves the RPC is unavailable; its error page format does not.
	if _, err := o.gatewaySmokeRequest(gateway, keyPath, 404, true); err != nil {
		return err
	}
	if _, err := o.gatewaySmokeRequest(gateway, "/credit.v1.CreditReservationService/Reserve", 404, true); err != nil {
		return err
	}
	if _, err := o.gatewaySmokeRequest(
		gateway,
		"/order.v1.OrderInternalService/GetOrderParticipants",
		404,
		true,
	); err != nil {
		return err
	}
	for _, service := range []string{"user-service", "credit-service", "order-service", "content-moderation-service", "moderation-service"} {
		for _, target := range []struct {
			suffix string
			port   int
		}{{"-external", 8080}, {"-internal", 8081}} {
			if err := o.assertServiceTarget(service+target.suffix, target.port); err != nil {
				return err
			}
		}
	}
	if _, err := o.gatewaySmokeRequest(
		gateway,
		"/user.v1.HealthService/Check?foc-query-canary="+suffix,
		200,
		true,
	); err != nil {
		return err
	}
	if err := sleep(o.ctx, time.Second); err != nil {
		return err
	}
	logs, err := o.query(
		"-n",
		"istio-ingress",
		"logs",
		"deployment/foc-public-istio",
		"-c",
		"istio-proxy",
		"--since=2m",
	)
	if err != nil {
		return err
	}
	if !strings.Contains(logs, "/user.v1.HealthService/Check") || strings.Contains(logs, "foc-query-canary="+suffix) {
		return errors.New("gateway access logs must retain paths without query credentials")
	}
	if os.Getenv("K8S_BOOTSTRAP_SUPERADMIN_EMAIL") != "" {
		mailPort, err := smokePort("K8S_SMOKE_MAILPIT_PORT", 19025)
		if err != nil {
			return err
		}
		mail := forwardSpec{
			name:       "smoke Mailpit",
			namespace:  "foc",
			service:    "mailpit",
			localPort:  mailPort,
			remotePort: 8025,
			scheme:     "http",
		}
		if err := preflightForwards([]forwardSpec{mail}); err != nil {
			return err
		}
		mailChild, err := o.startForward(mail)
		if err != nil {
			return err
		}
		defer stopForwards([]*portForward{mailChild})
		origin, err := localOrigin()
		if err != nil {
			return err
		}
		if err := o.authSmoke(
			fmt.Sprintf("https://localhost:%d", port),
			origin,
			fmt.Sprintf("http://127.0.0.1:%d", mailPort),
		); err != nil {
			return err
		}
	} else {
		note("SKIP browser auth: set K8S_BOOTSTRAP_SUPERADMIN_EMAIL during bootstrap and smoke.")
	}
	note(
		"PASS gateway, receiving-side workload authorization, plaintext rejection, query-safe logs and configured browser authentication; business workflow remains unverified.",
	)
	return nil
}

func (o *operator) persistenceSmoke() error {
	if err := o.requireManagedLocal(); err != nil {
		return err
	}
	note("Restarting PostgreSQL and Kafka pods without deleting PVCs; applications may briefly become unready.")
	database, err := o.query(
		"-n",
		"foc",
		"get",
		"secret",
		"user-db",
		"-o",
		"go-template={{.data.DB_NAME | base64decode}}",
	)
	if err != nil {
		return err
	}
	user, err := o.query(
		"-n",
		"foc",
		"get",
		"secret",
		"postgres-admin",
		"-o",
		"go-template={{.data.POSTGRES_USER | base64decode}}",
	)
	if err != nil {
		return err
	}
	postgres := []string{
		"-n",
		"foc",
		"exec",
		"statefulset/postgres",
		"-c",
		"postgres",
		"--",
		"psql",
		"-X",
		"-U",
		strings.TrimSpace(user),
		"-d",
		strings.TrimSpace(database),
		"-Atc",
		"SELECT count(*)::text || ':' || coalesce(min(user_id::text), '') FROM admin_bootstrap;",
	}
	before, err := o.query(postgres...)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`^1:[0-9a-fA-F-]{36}$`).MatchString(strings.TrimSpace(before)) {
		return errors.New(
			"persistence smoke requires an existing bootstrap identity; bootstrap an administrator before restarting dependencies",
		)
	}
	suffix := uniqueSuffix()
	topic := "foc.infrastructure-smoke-" + suffix
	record := "persistence-" + suffix
	kafka := func(args []string, options commandOptions) (commandResult, error) {
		prefix := append(o.kubePrefix(), "-n", "foc", "exec")
		if options.stdinFile != "" {
			prefix = append(prefix, "-i")
		}
		prefix = append(prefix, "statefulset/kafka", "-c", "kafka", "--", "env", "KAFKA_HEAP_OPTS=-Xms32m -Xmx128m")
		return o.run("kubectl", append(prefix, args...), options)
	}
	if _, err := kafka(
		[]string{
			"/opt/kafka/bin/kafka-topics.sh",
			"--bootstrap-server",
			"127.0.0.1:9092",
			"--create",
			"--topic",
			topic,
			"--partitions",
			"1",
			"--replication-factor",
			"1",
		},
		commandOptions{},
	); err != nil {
		return err
	}
	input := filepath.Join(o.state, "persistence-"+suffix+".txt")
	if err := writePrivate(input, record+"\n"); err != nil {
		return err
	}
	defer removeTemporary(input)
	if _, err := kafka(
		[]string{
			"/opt/kafka/bin/kafka-console-producer.sh",
			"--bootstrap-server",
			"127.0.0.1:9092",
			"--topic",
			topic,
			"--command-property",
			"acks=all",
		},
		commandOptions{capture: true, stdinFile: input},
	); err != nil {
		return err
	}
	read := func() error {
		result, err := kafka(
			[]string{
				"/opt/kafka/bin/kafka-console-consumer.sh",
				"--bootstrap-server",
				"127.0.0.1:9092",
				"--topic",
				topic,
				"--from-beginning",
				"--max-messages",
				"1",
				"--timeout-ms",
				"45000",
			},
			commandOptions{capture: true},
		)
		if err != nil {
			return err
		}
		if strings.TrimSpace(result.stdout) != record {
			return errors.New("Kafka acknowledged record missing")
		}
		return nil
	}
	if err := read(); err != nil {
		return err
	}
	if err := o.k("-n", "foc", "rollout", "restart", "statefulset/postgres", "statefulset/kafka"); err != nil {
		return err
	}
	for _, dep := range []string{"postgres", "kafka"} {
		if err := o.rollout("statefulset/" + dep); err != nil {
			return err
		}
	}
	after, err := o.query(postgres...)
	if err != nil {
		return err
	}
	if after != before {
		return errors.New("PostgreSQL bootstrap identity changed after restart")
	}
	if err := read(); err != nil {
		return err
	}
	for _, app := range apps {
		if err := o.rollout("deployment/" + app); err != nil {
			return err
		}
	}
	note("PASS PostgreSQL identity and Kafka record survived pod replacement; retained test topic %s.", topic)
	return nil
}

func (o *operator) proxyStartupSmoke() error {
	if err := o.requireManagedLocal(); err != nil {
		return err
	}
	namespace := "foc-proxy-startup-" + uniqueSuffix()
	if err := o.k("create", "namespace", namespace); err != nil {
		return err
	}
	defer o.cleanupKube("delete", "namespace", namespace, "--ignore-not-found", "--wait=false")
	image, err := smokeImage()
	if err != nil {
		return err
	}
	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      "blocked-app",
			"namespace": namespace,
			"labels":    map[string]string{"sidecar.istio.io/inject": "true"},
			"annotations": map[string]string{
				"sidecar.istio.io/nativeSidecar": "true",
				"proxy.istio.io/config":          `{"discoveryAddress":"127.0.0.1:1"}`,
			},
		},
		"spec": map[string]any{
			"restartPolicy":         "Never",
			"activeDeadlineSeconds": 420,
			"containers": []any{
				map[string]any{
					"name":    "application",
					"image":   image,
					"command": []string{"sleep", "400"},
					"securityContext": map[string]any{
						"runAsNonRoot":             true,
						"runAsUser":                65532,
						"allowPrivilegeEscalation": false,
						"capabilities":             map[string]any{"drop": []string{"ALL"}},
					},
				},
			},
		},
	}
	if err := o.applyJSON(manifest); err != nil {
		return err
	}
	output, err := o.query("-n", namespace, "get", "pod", "blocked-app", "-o", "json")
	if err != nil {
		return err
	}
	var pod struct {
		Spec struct {
			InitContainers []struct {
				Name         string
				StartupProbe *struct{ FailureThreshold, PeriodSeconds, TimeoutSeconds int }
			}
		}
	}
	if err := json.Unmarshal([]byte(output), &pod); err != nil {
		return err
	}
	budget := 0
	for _, container := range pod.Spec.InitContainers {
		if container.Name == "istio-proxy" && container.StartupProbe != nil {
			probe := container.StartupProbe
			timeout := probe.TimeoutSeconds
			if timeout == 0 {
				timeout = 1
			}
			budget = probe.FailureThreshold*(probe.PeriodSeconds+timeout) + 90
		}
	}
	if budget == 0 {
		return errors.New("injected proxy has no startup probe")
	}
	deadline := time.Now().Add(time.Duration(budget) * time.Second)
	for time.Now().Before(deadline) {
		state, err := o.query(
			"-n",
			namespace,
			"get",
			"pod",
			"blocked-app",
			"-o",
			`jsonpath={.status.containerStatuses[?(@.name=="application")].containerID}:{.status.initContainerStatuses[?(@.name=="istio-proxy")].restartCount}`,
		)
		if err != nil {
			return err
		}
		id, restarts, _ := strings.Cut(strings.TrimSpace(state), ":")
		if id != "" {
			return errors.New("application started despite an unavailable proxy")
		}
		count, _ := strconv.Atoi(restarts)
		if count > 0 {
			note("PASS unavailable proxy exhausted its startup probe and restarted; application never started.")
			return nil
		}
		if err := sleep(o.ctx, 2*time.Second); err != nil {
			return err
		}
	}
	return errors.New("did not observe a bounded proxy startup failure")
}

func (o *operator) storageSmoke() error {
	if err := o.requireManagedLocal(); err != nil {
		return err
	}
	if err := requireCommands("go"); err != nil {
		return err
	}
	httpsPort, err := smokePort("K8S_STORAGE_SMOKE_HTTPS_PORT", 19443)
	if err != nil {
		return err
	}
	s3Port, err := smokePort("K8S_STORAGE_SMOKE_S3_PORT", 19466)
	if err != nil {
		return err
	}
	forwards := []forwardSpec{
		{
			name:       "storage gateway",
			namespace:  "istio-ingress",
			service:    "foc-public-istio",
			localPort:  httpsPort,
			remotePort: 443,
			scheme:     "https",
		},
		{
			name:       "private storage cleanup",
			namespace:  "foc",
			service:    "floci",
			localPort:  s3Port,
			remotePort: 4566,
			scheme:     "http",
		},
	}
	if err := preflightForwards(forwards); err != nil {
		return err
	}
	var children []*portForward
	defer func() { stopForwards(children) }()
	for _, forward := range forwards {
		child, err := o.startForward(forward)
		if err != nil {
			return err
		}
		children = append(children, child)
	}
	_, err = o.run(
		"go",
		[]string{
			"-C",
			filepath.Join(o.root, "order-service"),
			"test",
			"-mod=readonly",
			"./internal/images",
			"-run",
			"^TestMeshObjectStorage$",
			"-count=1",
			"-v",
		},
		commandOptions{
			env: []string{
				"GOWORK=off",
				"AWS_REGION=us-east-1",
				"AWS_ACCESS_KEY_ID=test",
				"AWS_SECRET_ACCESS_KEY=test",
				fmt.Sprintf("S3_SMOKE_ENDPOINT=http://127.0.0.1:%d", s3Port),
				fmt.Sprintf("S3_SMOKE_PUBLIC_ENDPOINT=https://localhost:%d", httpsPort),
				"S3_SMOKE_CA_FILE=" + o.localCA(),
			},
		},
	)
	return err
}
