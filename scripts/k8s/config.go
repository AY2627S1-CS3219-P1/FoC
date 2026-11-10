package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var apps = []string{
	"user-service",
	"supplier-service",
	"order-service",
	"credit-service",
	"communication-service",
	"content-moderation-service",
	"frontend",
}

var jobs = []string{
	"user-migrate",
	"supplier-migrate",
	"order-migrate",
	"credit-migrate",
	"communication-migrate",
	"moderation-migrate",
	"kafka-init",
}
var stages = []string{"gateway", "dependencies", "provisioning", "observability", "initialization", "applications"}

type operator struct {
	ctx             context.Context
	root            string
	state           string
	overlay         string
	cluster         string
	kubeContext     string
	existing        bool
	overlayExplicit bool
	imagePrefix     string
	imageTag        string
	timeout         string
}

func newOperator(ctx context.Context, root string) *operator {
	cluster := envDefault("K8S_CLUSTER", "foc")
	aquaRoot := absolute(root, envDefault("AQUA_ROOT_DIR", ".tools/aqua"))
	// The installed tools must be visible to every child, including aqua shims.
	_ = os.Setenv("AQUA_ROOT_DIR", aquaRoot)
	_ = os.Setenv("PATH", filepath.Join(aquaRoot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &operator{
		ctx: ctx, root: root,
		state:   absolute(root, envDefault("K8S_STATE_DIR", ".local/k8s")),
		overlay: absolute(root, envDefault("K8S_OVERLAY", "infra/k8s/overlays/local")),
		cluster: cluster, kubeContext: envDefault("K8S_CONTEXT", "kind-"+cluster),
		existing: os.Getenv("K8S_CONTEXT") != "", overlayExplicit: os.Getenv("K8S_OVERLAY") != "",
		imagePrefix: envDefault("K8S_IMAGE_PREFIX", "foc"), imageTag: envDefault("K8S_IMAGE_TAG", "local"),
		timeout: envDefault("K8S_TIMEOUT", "600s"),
	}
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func absolute(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func note(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }

func (o *operator) isLocal() bool {
	return o.overlay == filepath.Join(o.root, "infra/k8s/overlays/local")
}

func (o *operator) imageFor(name string) string { return o.imagePrefix + "/" + name + ":" + o.imageTag }

func (o *operator) prepareState() error {
	clusters := filepath.Join(o.state, "clusters")
	if err := os.MkdirAll(clusters, 0700); err != nil {
		return fmt.Errorf(
			"cannot create %s: %w; create it with your ownership or set K8S_STATE_DIR to a writable private directory",
			o.state,
			err,
		)
	}
	for _, dir := range []string{o.state, clusters} {
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

func (o *operator) checkContext() error {
	if err := requireCommands("kubectl"); err != nil {
		return err
	}
	contexts, err := o.query("config", "get-contexts", o.kubeContext, "-o", "name")
	if err != nil {
		return err
	}
	if strings.TrimSpace(contexts) != o.kubeContext {
		return fmt.Errorf("context %s does not exist; run make k8s-up for the local cluster", o.kubeContext)
	}
	// cluster-info lists kube-system Services, which namespace-scoped release identities cannot read.
	_, err = o.query("version", "-o", "json")
	return err
}

func (o *operator) checkOverlay() error {
	if o.existing && !o.overlayExplicit {
		return errors.New("an existing cluster requires explicit K8S_CONTEXT and K8S_OVERLAY")
	}
	for _, stage := range stages {
		path := filepath.Join(o.overlay, stage, "kustomization.yaml")
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("missing %s: %w", path, err)
		}
	}
	return nil
}

type forwardSpec struct {
	name       string
	namespace  string
	service    string
	localPort  int
	remotePort int
	scheme     string
}

func parsePort(name, value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || !regexp.MustCompile(`^\d+$`).MatchString(value) || port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535, or blank to disable forwarding", name)
	}
	return port, nil
}

func configuredForwards(getenv func(string) string) ([]forwardSpec, error) {
	values := []struct {
		key     string
		forward forwardSpec
	}{
		{
			"K8S_HTTPS_PORT",
			forwardSpec{
				name:       "HTTPS gateway",
				namespace:  "istio-ingress",
				service:    "foc-public-istio",
				remotePort: 443,
				scheme:     "https",
			},
		},
		{
			"K8S_KAFKA_UI_PORT",
			forwardSpec{name: "Kafka UI", namespace: "foc", service: "kafka-ui", remotePort: 8080, scheme: "http"},
		},
		{
			"K8S_GRAFANA_PORT",
			forwardSpec{
				name:       "Grafana",
				namespace:  "foc-observability",
				service:    "grafana",
				remotePort: 3000,
				scheme:     "http",
			},
		},
		{
			"K8S_MAILPIT_PORT",
			forwardSpec{name: "Mailpit", namespace: "foc", service: "mailpit", remotePort: 8025, scheme: "http"},
		},
	}
	var forwards []forwardSpec
	seen := make(map[int]bool)
	for _, value := range values {
		port, err := parsePort(value.key, getenv(value.key))
		if err != nil {
			return nil, err
		}
		if port == 0 {
			continue
		}
		if seen[port] {
			return nil, fmt.Errorf(
				"multiple Kubernetes forwards use host port %d; choose distinct K8S_*_PORT values",
				port,
			)
		}
		seen[port] = true
		value.forward.localPort = port
		forwards = append(forwards, value.forward)
	}
	return forwards, nil
}

func localOrigin() (string, error) {
	port, err := parsePort("K8S_HTTPS_PORT", os.Getenv("K8S_HTTPS_PORT"))
	if err != nil {
		return "", err
	}
	if port == 0 {
		port = 5173
	}
	return fmt.Sprintf("https://localhost:%d", port), nil
}

func assertPortAvailable(port int) error {
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("host port %d is unavailable; stop Compose or choose another K8S_*_PORT: %w", port, err)
	}
	return listener.Close()
}

func preflightForwards(forwards []forwardSpec) error {
	for _, forward := range forwards {
		if err := assertPortAvailable(forward.localPort); err != nil {
			return err
		}
	}
	return nil
}
