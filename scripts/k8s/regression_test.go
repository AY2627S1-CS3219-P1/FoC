package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestForwardGuardsRejectOccupiedPortAndWrongOrigins(t *testing.T) {
	op := testOperator(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if err := assertPortAvailable(listener.Addr().(*net.TCPAddr).Port); err == nil {
		t.Fatal("occupied port accepted")
	}
	for _, key := range []string{"ORIGIN", "PUBLIC_ORDER_SERVICE_URL", "ORDER_S3_PUBLIC_ENDPOINT"} {
		t.Run(key, func(t *testing.T) {
			data := make(map[string]string)
			for _, field := range originKeys {
				data[field] = "https://localhost:19443"
			}
			data[key] = "https://localhost:5173"
			config, err := json.Marshal(map[string]any{"data": data})
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("K8S_TEST_CONFIG", string(config))
			fakeCommand(t, "kubectl", `printf '%s' "$K8S_TEST_CONFIG"`)
			err = op.checkGatewayOrigin([]forwardSpec{{service: "foc-public-istio", localPort: 19443}})
			if err == nil || !strings.Contains(err.Error(), "deployed browser origins") {
				t.Fatalf("wrong origin was accepted: %v", err)
			}
		})
	}
}

func TestManifestRetryPreservesResourcesAndRejectsPermanentErrors(t *testing.T) {
	for _, failure := range []string{"etcdserver: request timed out", "Forbidden: immutable Job template"} {
		t.Run(failure, func(t *testing.T) {
			op := testOperator(t)
			counter := filepath.Join(t.TempDir(), "attempts")
			t.Setenv("K8S_TEST_COUNTER", counter)
			t.Setenv("K8S_TEST_FAILURE", failure)
			fakeCommand(t, "kubectl", `count=0
if test -f "$K8S_TEST_COUNTER"; then count=$(cat "$K8S_TEST_COUNTER"); fi
count=$((count+1)); printf '%s' "$count" > "$K8S_TEST_COUNTER"
case "$*" in *' apply -f '*) ;; *) echo 'unexpected resource mutation' >&2; exit 5;; esac
if test "$count" = 1; then printf '%s' "$K8S_TEST_FAILURE" >&2; exit 1; fi
printf 'job/fixture unchanged\n'`)
			err := op.applyManifest("fixture.yaml")
			retry := strings.HasPrefix(failure, "etcdserver")
			if (err == nil) != retry {
				t.Fatalf("retry classification: %v", err)
			}
			data, err := os.ReadFile(counter)
			if err != nil {
				t.Fatal(err)
			}
			expected := "1"
			if retry {
				expected = "2"
			}
			if string(data) != expected {
				t.Fatalf("wrong attempt count %s", data)
			}
		})
	}
}

func TestDisruptionSmokesRejectExternalContext(t *testing.T) {
	op := testOperator(t)
	op.existing = true
	for _, command := range []string{"persistence-smoke", "proxy-startup-smoke", "storage-smoke", "smoke"} {
		if err := op.runCLI([]string{command}); err == nil || !strings.Contains(err.Error(), "managed local") {
			t.Fatalf("external disruption %s: %v", command, err)
		}
	}
}

func TestCancellationDuringForwardStartupReapsChild(t *testing.T) {
	op := testOperator(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("K8S_TEST_EXECUTABLE", executable)
	t.Setenv("K8S_TEST_LISTENER", "1")
	t.Setenv("K8S_TEST_WAIT_PID", pidPath)
	fakeCommand(t, "aqua", `printf '%s' "$K8S_TEST_EXECUTABLE"`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op.ctx = ctx
	outcome := make(chan error, 1)
	go func() { _, err := op.startForward(forwardSpec{name: "starting", localPort: 1}); outcome <- err }()
	deadline := time.Now().Add(5 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		data, err = os.ReadFile(pidPath)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("helper never started")
	}
	cancel()
	select {
	case err := <-outcome:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup cancellation hung")
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child process remained after cancellation: %v", err)
	}
}

func TestAllKustomizeStagesRenderWithSelectedImages(t *testing.T) {
	op := testOperator(t)
	if os.Getenv("K8S_RENDER_TESTS") != "1" {
		t.Skip("run make k8s-test to include real kubectl rendering")
	}
	aquaRoot := absolute(op.root, envDefault("AQUA_ROOT_DIR", ".tools/aqua"))
	t.Setenv("PATH", filepath.Join(aquaRoot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Fatal(err)
	}
	op.imagePrefix, op.imageTag = "registry.example/foc", "unit-test"
	for _, overlay := range []string{"local", "production"} {
		op.overlay = filepath.Join(op.root, "infra/k8s/overlays", overlay)
		for _, stage := range stages {
			output, err := op.renderStage(stage)
			if err != nil {
				t.Fatalf("%s/%s: %v", overlay, stage, err)
			}
			if stage == "initialization" || stage == "applications" {
				names := []string{"user-service", "credit-service"}
				if stage == "initialization" {
					names = []string{"migrations", "kafka-init"}
				}
				for _, name := range names {
					if !strings.Contains(output, "image: registry.example/foc/"+name+":unit-test") {
						t.Fatalf("%s/%s missing selected image %s", overlay, stage, name)
					}
				}
				if strings.Contains(output, "image: foc/") {
					t.Fatal("unmodified application image remained")
				}
			}
			if stage == "applications" && !strings.Contains(output, "/user.v1.AllowedDomainAdminService") {
				t.Fatal("new public admin RPC is missing from gateway allowlist")
			}
			if overlay == "production" && stage == "provisioning" && strings.TrimSpace(output) != "" {
				t.Fatal("production unexpectedly provisions local database")
			}
		}
	}
}

func TestPersistenceRequiresDataBeforeRestart(t *testing.T) {
	op := testOperator(t)
	log := filepath.Join(t.TempDir(), "commands")
	t.Setenv("K8S_TEST_LOG", log)
	fakeCommand(t, "kubectl", `printf '%s\n' "$*" >> "$K8S_TEST_LOG"
case "$*" in *'config get-contexts'*) printf 'kind-foc-test';; *' psql '*) printf '0:';; esac`)
	err := op.persistenceSmoke()
	if err == nil || !strings.Contains(err.Error(), "existing bootstrap identity") {
		t.Fatalf("empty persistence assertion was accepted: %v", err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "rollout restart") || strings.Contains(string(calls), "kafka-topics") {
		t.Fatal("empty identity triggered disruption or Kafka mutations")
	}
}
