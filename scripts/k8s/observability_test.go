package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func productionLoggingEnv(t *testing.T) {
	t.Helper()
	t.Setenv("K8S_LOKI_BUCKET", "foc-production-logs")
	t.Setenv("K8S_LOKI_REGION", "ap-southeast-1")
	t.Setenv("K8S_LOKI_IRSA_ROLE", "arn:aws:iam::123456789012:role/foc-loki")
	t.Setenv("K8S_LOKI_STORAGE_CLASS", "gp3")
}

func TestInvalidOverlayPrecedesProductionLoggingValidation(t *testing.T) {
	for _, command := range []string{"deploy", "up"} {
		for _, scenario := range []string{"missing-overlay", "implicit-external-overlay"} {
			t.Run(command+"/"+scenario, func(t *testing.T) {
				op := testOperator(t)
				for _, key := range []string{"K8S_LOKI_BUCKET", "K8S_LOKI_REGION", "K8S_LOKI_IRSA_ROLE", "K8S_LOKI_STORAGE_CLASS"} {
					t.Setenv(key, "")
				}
				want := "missing "
				if scenario == "missing-overlay" {
					op.overlay = filepath.Join(t.TempDir(), "missing")
				} else {
					op.overlay = filepath.Join(op.root, "infra/k8s/overlays/production")
					op.existing = true
					want = "requires explicit K8S_CONTEXT and K8S_OVERLAY"
				}

				if err := op.runCLI([]string{command}); err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("expected overlay error containing %q, got %v", want, err)
				}
			})
		}
	}
}

func TestProductionLoggingRejectsIncompleteConfigBeforeCommands(t *testing.T) {
	for _, command := range []string{"deploy", "up"} {
		for _, key := range []string{"K8S_LOKI_BUCKET", "K8S_LOKI_REGION", "K8S_LOKI_IRSA_ROLE", "K8S_LOKI_STORAGE_CLASS"} {
			t.Run(command+"/"+key, func(t *testing.T) {
				op := testOperator(t)
				productionLoggingEnv(t)
				op.overlay = filepath.Join(op.root, "infra/k8s/overlays/production")
				t.Setenv(key, "")
				if err := op.runCLI(
					[]string{command},
				); err == nil ||
					!strings.Contains(err.Error(), "production requires") {
					t.Fatalf("accepted missing %s: %v", key, err)
				}
			})
		}
	}
	op := testOperator(t)
	productionLoggingEnv(t)
	op.overlay = filepath.Join(op.root, "infra/k8s/overlays/production")
	args, err := op.lokiValuesArgs()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		strings.Join(args, " "),
		`serviceAccount.annotations.eks\.amazonaws\.com/role-arn=arn:aws:iam::123456789012:role/foc-loki`,
	) {
		t.Fatal("IRSA annotation not set on the pinned Loki service account")
	}
}

func TestLokiChartRejectsTamperedCache(t *testing.T) {
	op := testOperator(t)
	op.root = t.TempDir()
	chartDir := filepath.Join(op.root, ".tools/charts")
	checksumDir := filepath.Join(op.root, "infra/k8s/observability")
	for _, dir := range []string{chartDir, checksumDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	checksum := fmt.Sprintf("%x  loki-21.0.0.tgz\n", sha256.Sum256([]byte("trusted")))
	if err := os.WriteFile(filepath.Join(checksumDir, "chart.sha256"), []byte(checksum), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(chartDir, "loki-21.0.0.tgz")
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := op.lokiChart(); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered chart accepted: %v", err)
	}
	if err := os.WriteFile(path, []byte("trusted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := op.lokiChart(); err != nil {
		t.Fatal(err)
	}
}

func TestGrafanaForwardParticipatesInPortConflictChecks(t *testing.T) {
	values := map[string]string{"K8S_GRAFANA_PORT": "3000", "K8S_MAILPIT_PORT": "3000"}
	if _, err := configuredForwards(func(key string) string { return values[key] }); err == nil {
		t.Fatal("Grafana port conflict accepted")
	}
	values["K8S_MAILPIT_PORT"] = ""
	forwards, err := configuredForwards(func(key string) string { return values[key] })
	if err != nil || len(forwards) != 1 || forwards[0].namespace != "foc-observability" {
		t.Fatalf("Grafana forward: %+v %v", forwards, err)
	}
}

func TestObservabilityRenderOwnershipAndAdmissionFields(t *testing.T) {
	if os.Getenv("K8S_RENDER_TESTS") != "1" {
		t.Skip("requires installed Helm and kubectl")
	}
	op := testOperator(t)
	aquaRoot := absolute(op.root, envDefault("AQUA_ROOT_DIR", ".tools/aqua"))
	t.Setenv("PATH", filepath.Join(aquaRoot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	productionLoggingEnv(t)
	for _, env := range []string{"local", "production"} {
		op.overlay = filepath.Join(op.root, "infra/k8s/overlays", env)
		kustomize, err := op.renderStage("observability")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(kustomize, "kind: StatefulSet") ||
			strings.Contains(kustomize, "kind: ServiceAccount\nmetadata:\n  name: loki\n") {
			t.Fatal("Kustomize owns Loki resources")
		}
		helm, err := op.renderLoki()
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"kind: StatefulSet", "replicas: 1", "runAsUser: 10001", "runAsNonRoot: true", "allowPrivilegeEscalation: false", "- ALL", "whenDeleted: Retain", "whenScaled: Retain", "storage: \"5Gi\"", "retention_period: 168h", "retention_enabled: true", "period: 24h", "schema: v13"} {
			if !strings.Contains(helm, want) {
				t.Errorf("%s missing %s", env, want)
			}
		}
		for _, bad := range []string{"kind: ClusterRole", "kind: Job", "initContainers:", "name: loki-gateway", "name: loki-canary", "name: loki-chunks-cache", "name: loki-results-cache", "name: loki-sc-rules"} {
			if strings.Contains(helm, bad) {
				t.Errorf("unexpected chart resource %s", bad)
			}
		}
	}
}

func TestProductionGrafanaSecretRequiresBothKeys(t *testing.T) {
	op := testOperator(t)
	op.overlay = filepath.Join(op.root, "infra/k8s/overlays/production")
	for _, invalid := range []string{"", ":", "dXNlcg==:", ":cGFzcw=="} {
		t.Setenv("K8S_TEST_SECRET", invalid)
		fakeCommand(t, "kubectl", `printf '%s' "$K8S_TEST_SECRET"`)
		if err := op.checkGrafanaSecret(); err == nil {
			t.Fatalf("accepted incomplete Grafana Secret %q", invalid)
		}
	}
	t.Setenv("K8S_TEST_SECRET", "dXNlcg==:cGFzcw==")
	if err := op.checkGrafanaSecret(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalLokiWaitsForBucketAndUsesHelmOwnership(t *testing.T) {
	op := testOperator(t)
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("K8S_TEST_LOG", log)
	fakeCommand(t, "kubectl", `printf '%s\n' "$*" >> "$K8S_TEST_LOG"
case "$*" in
 *'get job loki-bucket-init --ignore-not-found'*) ;;
 *'get job loki-bucket-init -o'*) printf '1:' ;;
 *'kustomize'*) printf 'apiVersion: v1\nkind: Service\nmetadata:\n  name: grafana\n' ;;
esac`)
	fakeCommand(t, "helm", `printf '%s\n' "$*" >> "$K8S_TEST_LOG"`)
	if err := op.deployObservability(); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(calls)
	bucket := strings.Index(commands, "get job loki-bucket-init -o")
	install := strings.Index(commands, "upgrade --install loki ")
	ready := strings.Index(commands, "rollout status deployment/grafana")
	if bucket < 0 || install <= bucket || ready <= install {
		t.Fatalf("incorrect bucket/Loki/readiness order: %s", commands)
	}
	if !strings.Contains(commands, "--wait --timeout 5s") {
		t.Fatal("Loki install does not wait for readiness")
	}
	for _, line := range strings.Split(commands, "\n") {
		if strings.Contains(line, " apply ") && strings.Contains(line, "loki-21.0.0") {
			t.Fatal("Helm resources were also applied through kubectl")
		}
	}
}

func TestLoggingAdmissionLiveCluster(t *testing.T) {
	if os.Getenv("K8S_RUNTIME_TESTS") != "1" {
		t.Skip("requires an isolated managed kind cluster")
	}
	fixture := testOperator(t)
	op := newOperator(fixture.ctx, fixture.root)
	if err := op.requireManagedLocal(); err != nil {
		t.Fatal(err)
	}
	if err := op.prepareState(); err != nil {
		t.Fatal(err)
	}
	if err := op.loggingAdmissionSmoke(); err != nil {
		t.Fatal(err)
	}
}

func TestLoggingCredentialsAndPVCRetentionLiveCluster(t *testing.T) {
	if os.Getenv("K8S_RUNTIME_TESTS") != "1" {
		t.Skip("disruptive: requires an isolated managed kind cluster")
	}
	fixture := testOperator(t)
	op := newOperator(fixture.ctx, fixture.root)
	if err := op.requireManagedLocal(); err != nil {
		t.Fatal(err)
	}
	credentials := map[string][32]byte{}
	for _, name := range []string{"grafana-user", "grafana-password"} {
		data, err := os.ReadFile(filepath.Join(op.state, "secrets", name))
		if err != nil {
			t.Fatal(err)
		}
		credentials[name] = sha256.Sum256(data)
	}
	beforeSecret, err := op.query("-n", "foc-observability", "get", "secret", "grafana-admin", "-o", "jsonpath={.data}")
	if err != nil {
		t.Fatal(err)
	}
	if err := op.generateLocalSecrets(); err != nil {
		t.Fatal(err)
	}
	for name, want := range credentials {
		data, err := os.ReadFile(filepath.Join(op.state, "secrets", name))
		if err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(data) != want {
			t.Fatalf("credential rotated: %s", name)
		}
	}
	afterSecret, err := op.query("-n", "foc-observability", "get", "secret", "grafana-admin", "-o", "jsonpath={.data}")
	if err != nil {
		t.Fatal(err)
	}
	if beforeSecret != afterSecret {
		t.Fatal("Grafana Secret data changed")
	}
	claimUID := func() string {
		uid, err := op.query("-n", "foc-observability", "get", "pvc/storage-loki-0", "-o", "jsonpath={.metadata.uid}")
		if err != nil {
			t.Fatal(err)
		}
		if uid == "" {
			t.Fatal("missing Loki claim UID")
		}
		return uid
	}
	before := claimUID()
	if err := op.k("-n", "foc-observability", "scale", "statefulset/loki", "--replicas=0"); err != nil {
		t.Fatal(err)
	}
	// Restore even on an assertion failure so the disposable cluster is inspectable.
	defer func() {
		if err := op.deployObservability(); err != nil {
			t.Errorf("restore observability: %v", err)
			return
		}
		if claimUID() != before {
			t.Error("Loki claim changed on Helm reinstall")
		}
	}()
	if err := op.k(
		"-n",
		"foc-observability",
		"wait",
		"--for=delete",
		"pod/loki-0",
		"--timeout="+op.timeout,
	); err != nil {
		t.Fatal(err)
	}
	if claimUID() != before {
		t.Fatal("Loki claim changed after scale-down")
	}
	if err := op.helm("uninstall", "loki", "-n", "foc-observability", "--wait", "--timeout", op.timeout); err != nil {
		t.Fatal(err)
	}
	if claimUID() != before {
		t.Fatal("Loki claim changed after Helm removal")
	}
	note("PASS runtime credential preservation and Loki PVC retention through scale-down and Helm removal")
}

func TestLoggingReplicasRestoreLiveCluster(t *testing.T) {
	if os.Getenv("K8S_RUNTIME_TESTS") != "1" {
		t.Skip("disruptive: requires an isolated managed kind cluster")
	}
	fixture := testOperator(t)
	op := newOperator(fixture.ctx, fixture.root)
	if err := op.requireManagedLocal(); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		output, err := op.query(
			"-n",
			"foc",
			"get",
			"deployment/user-service",
			"-o",
			`jsonpath={.spec.replicas}:{.spec.strategy}:{.spec.template.spec.affinity}:{.spec.template.spec.topologySpreadConstraints}`,
		)
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	before := state()
	cleanup, _, err := op.spreadLoggingReplicas()
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if state() != before {
		t.Fatal("logging replica check did not restore original deployment settings")
	}
	if err := op.rollout("deployment/user-service"); err != nil {
		t.Fatal(err)
	}
}
