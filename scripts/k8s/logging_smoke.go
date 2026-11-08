package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Run only against the managed local cluster: this check restarts stateful services.
func (o *operator) loggingSmoke() (err error) {
	if err := o.requireManagedLocal(); err != nil {
		return err
	}
	if err := o.loggingAdmissionSmoke(); err != nil {
		return err
	}
	cleanupReplicas, pods, err := o.spreadLoggingReplicas()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, cleanupReplicas())
		if err == nil {
			note(
				"PASS logs from replicas on distinct nodes, authenticated Grafana, mesh denial, S3 uploads, historical queries, replacement collection and restored deployment",
			)
		}
	}()
	ownership, err := o.query(
		"-n",
		"foc-observability",
		"get",
		"statefulset/loki",
		"-o",
		`jsonpath={.metadata.labels.app\.kubernetes\.io/managed-by}:{.metadata.annotations.meta\.helm\.sh/release-name}`,
	)
	if err != nil {
		return err
	}
	if ownership != "Helm:loki" {
		return fmt.Errorf("unexpected Loki ownership: %s", ownership)
	}
	client := "logging-check-" + uniqueSuffix()
	if err := o.makeSmokeClient("foc-observability", client, "grafana", true); err != nil {
		return err
	}
	defer o.cleanupKube("-n", "foc-observability", "delete", "pod", client, "--ignore-not-found", "--wait=false")
	denied := "logging-denied-" + uniqueSuffix()
	if err := o.makeSmokeClient("foc", denied, "order-service", true); err != nil {
		return err
	}
	defer o.cleanupKube("-n", "foc", "delete", "pod", denied, "--ignore-not-found", "--wait=false")
	loki := "http://loki.foc-observability.svc.cluster.local:3100"
	if err := o.assertTransportBlocked("foc", denied, loki+"/loki/api/v1/labels"); err != nil {
		return err
	}
	gatewayClient := "logging-gateway-" + uniqueSuffix()
	if err := o.makeSmokeClient("istio-ingress", gatewayClient, "foc-public", true); err != nil {
		return err
	}
	defer o.cleanupKube("-n", "istio-ingress", "delete", "pod", gatewayClient, "--ignore-not-found", "--wait=false")
	if err := o.assertTransportBlocked("istio-ingress", gatewayClient, loki+"/loki/api/v1/labels"); err != nil {
		return err
	}
	deniedBucket, err := o.query(
		"-n",
		"istio-ingress",
		"exec",
		gatewayClient,
		"-c",
		"client",
		"--",
		"curl",
		"-sS",
		"-o",
		"/dev/null",
		"-w",
		"%{http_code}",
		"--aws-sigv4",
		"aws:amz:us-east-1:s3",
		"--user",
		"test:test",
		"http://floci.foc.svc.cluster.local:4566/foc-loki?list-type=2",
	)
	if err != nil {
		return err
	}
	if strings.TrimSpace(deniedBucket) != "403" {
		return errors.New("gateway identity reached Loki bucket")
	}
	// Public route checks are also covered by storage/mesh smoke.
	port, err := smokePort("K8S_LOGGING_SMOKE_PORT", 19300)
	if err != nil {
		return err
	}
	if err := assertPortAvailable(port); err != nil {
		return err
	}
	forward, err := o.startForward(
		forwardSpec{
			name:       "Grafana logging check",
			namespace:  "foc-observability",
			service:    "grafana",
			localPort:  port,
			remotePort: 3000,
			scheme:     "http",
		},
	)
	if err != nil {
		return err
	}
	defer stopForwards([]*portForward{forward})
	username, err := os.ReadFile(filepath.Join(o.state, "secrets", "grafana-user"))
	if err != nil {
		return err
	}
	password, err := os.ReadFile(filepath.Join(o.state, "secrets", "grafana-password"))
	if err != nil {
		return err
	}
	end := time.Now()
	start := end.Add(-time.Hour)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	// Grafana's data-source API must require a login even on the private forward.
	unauthenticated, err := (&http.Client{Timeout: 10 * time.Second}).Get(base + "/api/datasources")
	if err != nil {
		return err
	}
	_ = unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("Grafana anonymous API status %d", unauthenticated.StatusCode)
	}
	query := func(selector string) error {
		params := url.Values{
			"query": {selector},
			"start": {fmt.Sprint(start.UnixNano())},
			"end":   {fmt.Sprint(end.UnixNano())},
			"limit": {"1000"},
		}
		req, err := http.NewRequestWithContext(
			o.ctx,
			http.MethodGet,
			base+"/api/datasources/proxy/uid/loki/loki/api/v1/query_range?"+params.Encode(),
			nil,
		)
		if err != nil {
			return err
		}
		req.SetBasicAuth(strings.TrimSpace(string(username)), strings.TrimSpace(string(password)))
		response, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("Grafana Loki query HTTP %d", response.StatusCode)
		}
		var result struct {
			Status string
			Data   struct {
				Result []struct {
					Stream map[string]string
					Values [][]string
				}
			}
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			return err
		}
		if result.Status != "success" || len(result.Data.Result) == 0 {
			return fmt.Errorf("no logs for %s", selector)
		}
		for _, stream := range result.Data.Result {
			for _, label := range []string{"namespace", "service", "pod", "container", "node"} {
				if stream.Stream[label] == "" {
					return fmt.Errorf("log stream missing %s", label)
				}
			}
		}
		return nil
	}
	selectors := []string{
		`{namespace="foc",container="user-service"}`,
		`{namespace="foc",container="postgres"}`,
		`{namespace="foc",container="frontend"}`,
		`{namespace="foc",container="istio-proxy"}`,
		`{namespace="istio-ingress",container="istio-proxy"}`,
	}
	for _, pod := range pods {
		selectors = append(selectors, fmt.Sprintf(`{namespace="foc",pod=%q,container="user-service"}`, pod))
	}
	for _, selector := range selectors {
		deadline := time.Now().Add(90 * time.Second)
		for {
			if err := query(selector); err == nil {
				break
			} else if time.Now().After(deadline) {
				return err
			}
			if err := sleep(o.ctx, 2*time.Second); err != nil {
				return err
			}
		}
	}
	if err := o.k(
		"-n",
		"foc-observability",
		"exec",
		client,
		"-c",
		"client",
		"--",
		"curl",
		"-fsS",
		"-X",
		"POST",
		loki+"/flush",
	); err != nil {
		return err
	}
	// A signed listing proves the flush uploaded objects, rather than serving only WAL data.
	storageClient := "logging-storage-" + uniqueSuffix()
	if err := o.makeSmokeClient("foc-observability", storageClient, "loki", true); err != nil {
		return err
	}
	defer o.cleanupKube("-n", "foc-observability", "delete", "pod", storageClient, "--ignore-not-found", "--wait=false")
	listing, err := o.query(
		"-n",
		"foc-observability",
		"exec",
		storageClient,
		"-c",
		"client",
		"--",
		"curl",
		"-fsS",
		"--aws-sigv4",
		"aws:amz:us-east-1:s3",
		"--user",
		"test:test",
		"http://floci.foc.svc.cluster.local:4566/foc-loki?list-type=2",
	)
	if err != nil {
		return err
	}
	if !strings.Contains(listing, "<Contents>") {
		return errors.New("Loki bucket has no uploaded objects after flush")
	}
	for _, target := range []struct{ namespace, resource string }{{"foc-observability", "statefulset/loki"}, {"foc", "deployment/floci"}, {"foc-observability", "deployment/alloy"}} {
		if err := o.k("-n", target.namespace, "rollout", "restart", target.resource); err != nil {
			return err
		}
		if err := o.k(
			"-n",
			target.namespace,
			"rollout",
			"status",
			target.resource,
			"--timeout="+o.timeout,
		); err != nil {
			return err
		}
	}
	for _, selector := range selectors {
		if err := query(selector); err != nil {
			return err
		}
	}
	start = time.Now()
	if err := o.k("-n", "foc", "rollout", "restart", "deployment/user-service"); err != nil {
		return err
	}
	if err := o.rollout("deployment/user-service"); err != nil {
		return err
	}
	current, err := o.query("-n", "foc", "get", "pods", "-l", "app=user-service", "-o", "json")
	if err != nil {
		return err
	}
	var replacements struct {
		Items []struct {
			Metadata struct {
				Name              string
				DeletionTimestamp *string
			}
		}
	}
	if err := json.Unmarshal([]byte(current), &replacements); err != nil {
		return err
	}
	var resumed []string
	for _, pod := range replacements.Items {
		if pod.Metadata.DeletionTimestamp == nil {
			if contains(pods, pod.Metadata.Name) {
				return errors.New("application restart retained an old pod")
			}
			resumed = append(
				resumed,
				fmt.Sprintf(`{namespace="foc",pod=%q,container="user-service"}`, pod.Metadata.Name),
			)
		}
	}
	if len(resumed) != 2 {
		return errors.New("expected two replacement application replicas")
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		end = time.Now()
		complete := true
		for _, selector := range resumed {
			if err := query(selector); err != nil {
				complete = false
				break
			}
		}
		if complete {
			return nil
		}
		if err := sleep(o.ctx, 2*time.Second); err != nil {
			return err
		}
	}

	return errors.New("application logs did not resume after replacement")
}

// Temporarily spread two real application replicas across distinct workers.
func (o *operator) spreadLoggingReplicas() (func() error, []string, error) {
	availableNodes, err := o.query("get", "nodes", "-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	if err != nil {
		return nil, nil, err
	}
	if len(strings.Fields(availableNodes)) < 2 {
		return nil, nil, errors.New(
			"logging smoke requires a fresh multi-node kind cluster; set K8S_KIND_CONFIG=infra/k8s/observability/kind.yaml",
		)
	}
	original, err := o.query("-n", "foc", "get", "deployment/user-service", "-o", "json")
	if err != nil {
		return nil, nil, err
	}
	var deployment struct {
		Spec struct {
			Replicas int
			Strategy any
			Template struct {
				Spec struct{ TopologySpreadConstraints any }
			}
		}
	}
	if err := json.Unmarshal([]byte(original), &deployment); err != nil {
		return nil, nil, err
	}
	patch := map[string]any{
		"spec": map[string]any{
			"replicas": 2,
			"strategy": map[string]any{
				"type":          "RollingUpdate",
				"rollingUpdate": map[string]any{"maxSurge": 0, "maxUnavailable": 1},
			},
			"template": map[string]any{
				"spec": map[string]any{
					// Scope counts to this ReplicaSet without imposing rules on future Pods.
					"topologySpreadConstraints": []any{map[string]any{
						"maxSkew": 1, "topologyKey": "kubernetes.io/hostname", "whenUnsatisfiable": "DoNotSchedule",
						"nodeTaintsPolicy": "Honor", "matchLabelKeys": []string{"pod-template-hash"},
						"labelSelector": map[string]any{"matchLabels": map[string]string{"app": "user-service"}},
					}},
				},
			},
		},
	}
	cleanup := func() error {
		budget, err := time.ParseDuration(o.timeout)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		restoration := *o
		restoration.ctx = ctx
		// Finish restoring the original template before restoring its rollout policy.
		operations := []map[string]any{
			{"op": "replace", "path": "/spec/replicas", "value": deployment.Spec.Replicas},
			{
				"op":   "replace",
				"path": "/spec/strategy",
				"value": map[string]any{
					"type":          "RollingUpdate",
					"rollingUpdate": map[string]any{"maxSurge": 0, "maxUnavailable": 1},
				},
			},
			{
				"op":    "add",
				"path":  "/spec/template/spec/topologySpreadConstraints",
				"value": deployment.Spec.Template.Spec.TopologySpreadConstraints,
			},
		}
		data, err := json.Marshal(operations)
		if err != nil {
			return err
		}
		if err := restoration.k(
			"-n",
			"foc",
			"patch",
			"deployment/user-service",
			"--type=json",
			"--patch",
			string(data),
		); err != nil {
			return err
		}
		if err := restoration.rollout("deployment/user-service"); err != nil {
			return err
		}
		originalStrategy, err := json.Marshal(
			[]map[string]any{{"op": "replace", "path": "/spec/strategy", "value": deployment.Spec.Strategy}},
		)
		if err != nil {
			return err
		}
		if err := restoration.k(
			"-n",
			"foc",
			"patch",
			"deployment/user-service",
			"--type=json",
			"--patch",
			string(originalStrategy),
		); err != nil {
			return err
		}
		return restoration.rollout("deployment/user-service")
	}

	data, err := json.Marshal(patch)
	if err != nil {
		return nil, nil, err
	}
	if err := o.k(
		"-n",
		"foc",
		"patch",
		"deployment/user-service",
		"--type=merge",
		"--patch",
		string(data),
	); err != nil {
		return nil, nil, err
	}
	if err := o.rollout("deployment/user-service"); err != nil {
		return nil, nil, errors.Join(err, cleanup())
	}
	pods, err := o.query("-n", "foc", "get", "pods", "-l", "app=user-service", "-o", "json")
	if err != nil {
		return nil, nil, errors.Join(err, cleanup())
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name              string
				DeletionTimestamp *string
			}
			Spec struct{ NodeName string }
		}
	}
	if err := json.Unmarshal([]byte(pods), &list); err != nil {
		return nil, nil, errors.Join(err, cleanup())
	}
	var names []string
	nodes := map[string]bool{}
	for _, pod := range list.Items {
		if pod.Metadata.DeletionTimestamp == nil {
			names = append(names, pod.Metadata.Name)
			nodes[pod.Spec.NodeName] = true
		}
	}
	if len(names) != 2 || len(nodes) != 2 {
		return nil, nil, errors.Join(
			errors.New(
				"logging acceptance requires two application replicas on distinct nodes; use K8S_KIND_CONFIG=infra/k8s/observability/kind.yaml",
			),
			cleanup(),
		)
	}
	return cleanup, names, nil
}

// Exercise the real mutating/validating admission chain without starting Pods.
func (o *operator) loggingAdmissionSmoke() error {
	for _, variant := range []string{"valid", "injection-disabled", "inherited-uid", "extra-init"} {
		security := map[string]any{
			"runAsNonRoot":             true,
			"runAsUser":                10001,
			"allowPrivilegeEscalation": false,
			"capabilities":             map[string]any{"drop": []string{"ALL"}},
		}
		spec := map[string]any{
			"serviceAccountName": "loki",
			"restartPolicy":      "Never",
			"securityContext":    map[string]any{"runAsUser": 10001},
			"containers": []any{
				map[string]any{
					"name":            "check",
					"image":           "grafana/loki:3.7.9@sha256:0db216b630ae1f348a1584c67f3cead8b4c1fc839169a39501db664744158e28",
					"securityContext": security,
				},
			},
		}
		labels := map[string]string{"app": "logging-admission"}
		expected := ""
		switch variant {
		case "injection-disabled":
			labels["sidecar.istio.io/inject"] = "false"
			expected = "FoC pods cannot disable injection using a label."
		case "inherited-uid":
			delete(security, "runAsUser")
			expected = "Application containers must be non-root"
		case "extra-init":
			spec["initContainers"] = []any{
				map[string]any{
					"name":            "unexpected",
					"image":           "grafana/loki:3.7.9@sha256:0db216b630ae1f348a1584c67f3cead8b4c1fc839169a39501db664744158e28",
					"securityContext": security,
				},
			}
			expected = "Only the pinned Istio proxy"
		}
		pod := map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":        "logging-admission-" + uniqueSuffix(),
				"namespace":   "foc-observability",
				"labels":      labels,
				"annotations": map[string]string{"sidecar.istio.io/nativeSidecar": "true"},
			},
			"spec": spec,
		}
		file, err := os.CreateTemp(o.state, ".logging-admission-*.json")
		if err != nil {
			return err
		}
		path := file.Name()
		encodeErr := json.NewEncoder(file).Encode(pod)
		closeErr := file.Close()
		if encodeErr != nil {
			removeTemporary(path)
			return encodeErr
		}
		if closeErr != nil {
			removeTemporary(path)
			return closeErr
		}
		result, err := o.run(
			"kubectl",
			append(o.kubePrefix(), "create", "--dry-run=server", "-f", path),
			commandOptions{capture: true, allowFailure: true},
		)
		removeTemporary(path)
		if err != nil {
			return err
		}
		if expected == "" {
			if result.exitCode != 0 {
				return fmt.Errorf("valid Loki security context rejected: %s", result.stderr)
			}
		} else if result.exitCode == 0 || !strings.Contains(result.stderr, "foc-require-mesh") || !strings.Contains(result.stderr, expected) {
			return fmt.Errorf("admission variant %s did not receive expected policy denial: %s", variant, result.stderr)
		}
	}
	note("PASS native-injection admission, explicit container UID enforcement and extra-init rejection")
	return nil
}
