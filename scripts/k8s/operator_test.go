package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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

func testOperator(t *testing.T) *operator {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &operator{
		ctx:         context.Background(),
		root:        root,
		state:       t.TempDir(),
		overlay:     filepath.Join(root, "infra/k8s/overlays/local"),
		cluster:     "foc-test",
		kubeContext: "kind-foc-test",
		imagePrefix: "foc",
		imageTag:    "local",
		timeout:     "5s",
	}
}

func fakeCommand(t *testing.T, name, script string) {
	t.Helper()
	path := filepath.Join(strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0], name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestForwardsValidatePortsAndDisabledValues(t *testing.T) {
	for _, value := range []string{"-1", "+80", "0", "65536", "1.5", "hello"} {
		if _, err := parsePort("port", value); err == nil {
			t.Errorf("accepted invalid port %q", value)
		}
	}
	values := map[string]string{"K8S_HTTPS_PORT": "9443", "K8S_KAFKA_UI_PORT": "", "K8S_MAILPIT_PORT": "8025"}
	forwards, err := configuredForwards(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if len(forwards) != 2 || forwards[0].localPort != 9443 || forwards[0].service != "foc-public-istio" {
		t.Fatalf("wrong enabled forwards: %+v", forwards)
	}
	values["K8S_MAILPIT_PORT"] = "9443"
	if _, err := configuredForwards(func(key string) string { return values[key] }); err == nil {
		t.Fatal("duplicate host ports were accepted")
	}
}

func TestCommandArgumentsAreNotShellCode(t *testing.T) {
	op := testOperator(t)
	fakeCommand(t, "argument-probe", `printf '%s' "$1"`)
	marker := filepath.Join(t.TempDir(), "executed")
	argument := "$(touch " + marker + "); 'quoted'\nsecond line"
	result, err := op.run("argument-probe", []string{argument}, commandOptions{capture: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.stdout != argument {
		t.Fatalf("arguments changed: %q", result.stdout)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("argument was interpreted as shell code")
	}
}

func TestRunningJobsArePreserved(t *testing.T) {
	op := testOperator(t)
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("K8S_TEST_LOG", log)
	fakeCommand(t, "kubectl", `printf '%s\n' "$*" >> "$K8S_TEST_LOG"
case "$*" in *' get job '*) printf 'migration:0:';; esac`)
	if err := op.prepareJob("migration"); err == nil || !strings.Contains(err.Error(), "not terminal") {
		t.Fatalf("running job: %v", err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "delete") {
		t.Fatal("running job was deleted")
	}
	fakeCommand(t, "kubectl", `printf '%s\n' "$*" >> "$K8S_TEST_LOG"
case "$*" in *' get job '*) printf 'migration:1:';; esac`)
	if err := op.prepareJob("migration"); err != nil {
		t.Fatal(err)
	}
	calls, err = os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "delete job migration --wait=true") {
		t.Fatal("completed job was not replaced")
	}
}

func TestClusterDeletionRequiresExactConfirmationAndManagedContext(t *testing.T) {
	op := testOperator(t)
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("K8S_TEST_LOG", log)
	fakeCommand(t, "kind", `printf '%s\n' "$*" >> "$K8S_TEST_LOG"`)
	t.Setenv("K8S_CONFIRM_DELETE", "foc")
	if err := op.deleteCluster(); err == nil {
		t.Fatal("wrong confirmation accepted")
	}
	t.Setenv("K8S_CONFIRM_DELETE", op.cluster)
	op.existing = true
	if err := op.deleteCluster(); err == nil {
		t.Fatal("external context deletion accepted")
	}
	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("kind ran before authorization checks")
	}
	op.existing = false
	if err := op.deleteCluster(); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "delete cluster --name foc-test\n" {
		t.Fatalf("wrong deletion command: %s", calls)
	}
}

func TestLocalCredentialsSurviveRedeploy(t *testing.T) {
	op := testOperator(t)
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Fatal("OpenSSL is required to verify local credential generation")
	}
	fakeCommand(
		t,
		"kubectl",
		`case "$*" in *' create secret '*) printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: test\n';; esac`,
	)
	t.Setenv("K8S_TELEGRAM_BOT_TOKEN", "first-token")
	if err := op.generateLocalSecrets(); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(op.state, "secrets")
	before := make(map[string][32]byte)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		before[entry.Name()] = sha256.Sum256(data)
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("credential %s permissions: %o", entry.Name(), info.Mode().Perm())
		}
	}
	for _, name := range []string{"grafana-user", "grafana-password"} {
		if _, ok := before[name]; !ok {
			t.Fatalf("missing persisted %s", name)
		}
	}
	t.Setenv("K8S_TELEGRAM_BOT_TOKEN", "replacement-token")
	if err := op.generateLocalSecrets(); err != nil {
		t.Fatal(err)
	}
	for name, expected := range before {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(data) != expected {
			t.Errorf("credential %s was rotated", name)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "user-password"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := op.generateLocalSecrets(); err == nil || !strings.Contains(err.Error(), "empty credential") {
		t.Fatalf("empty existing password was not rejected: %v", err)
	}
}

func TestSecretGeneratorRejectsIncompleteTLSAndConcurrentRun(t *testing.T) {
	op := testOperator(t)
	fakeCommand(t, "kubectl", "exit 0")
	directory := filepath.Join(op.state, "secrets")
	if err := os.MkdirAll(filepath.Join(directory, ".generating"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := op.generateLocalSecrets(); err == nil || !strings.Contains(err.Error(), "cannot acquire") {
		t.Fatalf("generator lock not respected: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, ".generating")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "tls.key"), []byte("preserve-me"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := op.generateLocalSecrets(); err == nil || !strings.Contains(err.Error(), "incomplete local TLS") {
		t.Fatalf("incomplete TLS was not rejected: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "tls.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "preserve-me" {
		t.Fatal("existing TLS key was overwritten")
	}
}

func TestPortForwardIsReapedOnCancellation(t *testing.T) {
	op := testOperator(t)
	// Reuse the test executable as a real TCP listener so no Kubernetes cluster
	// is necessary to exercise startup readiness and process ownership.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fakeCommand(t, "aqua", `printf '%s' "$K8S_TEST_EXECUTABLE"`)
	t.Setenv("K8S_TEST_EXECUTABLE", executable)
	t.Setenv("K8S_TEST_LISTENER", "1")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op.ctx = ctx
	child, err := op.startForward(
		forwardSpec{name: "test", namespace: "test", service: "test", localPort: port, remotePort: 80, scheme: "http"},
	)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	stopForwards([]*portForward{child})
	select {
	case <-child.done:
	case <-time.After(4 * time.Second):
		t.Fatal("child was not reaped")
	}
	if waitForLocalListener(port) {
		t.Fatal("port-forward listener leaked")
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("K8S_TEST_LISTENER") == "1" {
		if path := os.Getenv("K8S_TEST_WAIT_PID"); path != "" {
			if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
				os.Exit(1)
			}
			time.Sleep(30 * time.Second)
		}

		port := strings.Split(os.Args[len(os.Args)-1], ":")[0]
		listener, err := net.Listen("tcp4", "127.0.0.1:"+port)
		if err != nil {
			os.Exit(1)
		}
		for {
			conn, err := listener.Accept()
			if err != nil {
				os.Exit(1)
			}
			_ = conn.Close()
		}
	}
	if os.Getenv("K8S_TEST_CLI") == "1" {
		os.Args = []string{"k8s", "render", "dependencies"}
		if command := os.Getenv("K8S_TEST_CLI_COMMAND"); command != "" {
			os.Args = []string{"k8s", command}
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCLIReadsDotEnvWithoutOverridingExports(t *testing.T) {
	op := testOperator(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "aqua.yaml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, stage := range stages {
		dir := filepath.Join(root, "infra/k8s/overlays/local", stage)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "kustomization.yaml"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(root, ".env"),
		[]byte("K8S_IMAGE_PREFIX=from-dotenv/foc\nK8S_IMAGE_TAG=dotenv-tag\nK8S_MAILPIT_PORT=8025\n"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	fakeCommand(t, "kubectl", `printf '%s/%s/%s' "$K8S_IMAGE_PREFIX" "$K8S_IMAGE_TAG" "$K8S_MAILPIT_PORT"`)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(op.ctx, executable)
	cmd.Dir = root
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "K8S_") || strings.HasPrefix(entry, "AQUA_ROOT_DIR=") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(
		cmd.Env,
		"K8S_TEST_CLI=1",
		"K8S_IMAGE_TAG=exported",
		"K8S_MAILPIT_PORT=",
		"K8S_STATE_DIR="+filepath.Join(root, "state"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI failed: %v: %s", err, output)
	}
	if string(output) != "from-dotenv/foc/exported/" {
		t.Fatalf("wrong environment precedence: %s", output)
	}
}

func TestProductionDeploySkipsLocalDatabaseProvisioning(t *testing.T) {
	productionLoggingEnv(t)
	op := testOperator(t)
	fakeCommand(t, "helm", `printf '%s\n' "$*" >> "$K8S_TEST_LOG"`)
	op.existing, op.overlayExplicit = true, true
	op.overlay = filepath.Join(op.root, "infra/k8s/overlays/production")
	op.imagePrefix, op.imageTag = "registry.test/foc", "commit123"
	t.Setenv("K8S_SKIP_BUILD", "1")
	t.Setenv("K8S_BOOTSTRAP_SUPERADMIN_EMAIL", "")
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("K8S_TEST_LOG", log)
	fakeCommand(t, "kubectl", `printf '%s\n' "$*" >> "$K8S_TEST_LOG"
case "$*" in
 *'config get-contexts'*) printf 'kind-foc-test';;
 *'create --dry-run=client --validate=false -k '*'-o json'*) printf '%s' '{"kind":"Gateway","metadata":{"name":"foc-public","namespace":"istio-ingress"},"spec":{"listeners":[{"protocol":"HTTPS","tls":{"certificateRefs":[{"name":"foc-public-tls"}]}}]}}';;
 *'kustomize'*) printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: fixture\n';;
 *'get job'*'jsonpath={.status.succeeded}'*) printf '1:';;
 *'get secret grafana-admin'*) printf 'dXNlcg==:cGFzcw==';;
 *'get httproute'*) printf '%s' '{"status":{"parents":[{"parentRef":{"name":"foc-public"},"controllerName":"istio.io/gateway-controller","conditions":[{"type":"Accepted","status":"True"},{"type":"ResolvedRefs","status":"True"}]}]}}';;
esac`)
	if err := op.deployAll(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(data)
	for _, forbidden := range []string{"statefulset/postgres", "db-provision", "order-storage-init", "deployment/mailpit", "deployment/floci"} {
		if strings.Contains(calls, forbidden) {
			t.Errorf("production accessed local resource %s", forbidden)
		}
	}
	if !strings.Contains(calls, "get secret user-db") || !strings.Contains(calls, "get secret communication-db") {
		t.Fatal("production did not require per-service secrets")
	}
	if !strings.Contains(calls, "get secret foc-public-tls") {
		t.Fatal("production HTTPS listener did not require its certificate Secret")
	}
	migration := strings.Index(calls, "get job user-migrate -o")
	application := strings.Index(calls, "rollout restart deployment/user-service")
	if migration < 0 || application <= migration {
		t.Fatal("applications started before migration completion")
	}
}

func TestCLISignalExitStatusAndForwardCleanup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signal os.Signal
		status int
	}{{"interrupt", os.Interrupt, 130}, {"termination", syscall.SIGTERM, 143}} {
		t.Run(tc.name, func(t *testing.T) {
			op := testOperator(t)
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "aqua.yaml"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "infra/k8s"), 0700); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			fakeCommand(t, "kubectl", `case "$*" in
 *get-contexts*) printf 'kind-foc';;
 *port-forward*) K8S_TEST_LISTENER=1 exec "$K8S_TEST_EXECUTABLE" "$@";;
 esac`)
			fakeCommand(t, "aqua", `printf '%s' "$K8S_TEST_KUBECTL"`)
			kubectl, err := exec.LookPath("kubectl")
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable)
			cmd.Dir = root
			for _, entry := range os.Environ() {
				if strings.HasPrefix(entry, "K8S_") || strings.HasPrefix(entry, "AQUA_ROOT_DIR=") {
					continue
				}
				cmd.Env = append(cmd.Env, entry)
			}
			cmd.Env = append(
				cmd.Env,
				"K8S_TEST_CLI=1",
				"K8S_TEST_CLI_COMMAND=access",
				"K8S_TEST_EXECUTABLE="+executable,
				"K8S_TEST_KUBECTL="+kubectl,
				"K8S_MAILPIT_PORT="+strconv.Itoa(port),
				"K8S_STATE_DIR="+op.state,
			)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			defer func() { _ = cmd.Process.Kill() }()
			deadline := time.Now().Add(5 * time.Second)
			for !waitForLocalListener(port) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if !waitForLocalListener(port) {
				_ = cmd.Process.Kill()
				<-done
				t.Fatalf("CLI forward never started: %s", output.String())
			}
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Fatal("CLI cleanup hung")
			}
			if cmd.ProcessState.ExitCode() != tc.status {
				t.Fatalf("signal exit=%d want=%d: %s", cmd.ProcessState.ExitCode(), tc.status, output.String())
			}
			if waitForLocalListener(port) {
				t.Fatal("CLI left its forward running")
			}
		})
	}
}
