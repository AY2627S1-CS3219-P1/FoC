package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const nodeImage = "kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f"

func (o *operator) createLocalCluster() error {
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(o.cluster) {
		return errors.New("invalid kind cluster name")
	}
	if err := requireCommands("kind"); err != nil {
		return err
	}
	if err := o.prepareState(); err != nil {
		return err
	}
	result, err := o.run("kind", []string{"get", "clusters"}, commandOptions{capture: true})
	if err != nil {
		return err
	}
	config := filepath.Join(o.state, "clusters", o.cluster+".config")
	if contains(strings.Fields(result.stdout), o.cluster) {
		return o.execute("kind", "export", "kubeconfig", "--name", o.cluster, "--kubeconfig", config)
	}
	return o.execute(
		"kind",
		"create",
		"cluster",
		"--name",
		o.cluster,
		"--image",
		envDefault("K8S_NODE_IMAGE", nodeImage),
		"--kubeconfig",
		config,
		"--wait",
		o.timeout,
	)
}

func (o *operator) installMesh() error {
	if err := requireCommands("helm"); err != nil {
		return err
	}
	istioctl, err := o.aquaWhich("istioctl")
	if err != nil {
		return err
	}
	charts := filepath.Join(filepath.Dir(filepath.Dir(istioctl)), "manifests/charts")
	for _, chart := range []string{"base", "istio-control/istio-discovery"} {
		if _, err := os.Stat(filepath.Join(charts, chart, "Chart.yaml")); err != nil {
			return fmt.Errorf("pinned Istio charts missing; run make k8s-tools: %w", err)
		}
	}
	for _, file := range []string{"namespaces.yaml", "admission.yaml"} {
		if err := o.applyManifest(filepath.Join(o.root, "infra/k8s/platform", file)); err != nil {
			return err
		}
	}
	crds := []string{
		"gatewayclasses.gateway.networking.k8s.io",
		"gateways.gateway.networking.k8s.io",
		"httproutes.gateway.networking.k8s.io",
	}
	if o.existing {
		for _, name := range crds {
			result, err := o.run(
				"kubectl",
				append(
					o.kubePrefix(),
					"get",
					"crd",
					name,
					"-o",
					`jsonpath={.status.conditions[?(@.type=="Established")].status}:{.spec.versions[?(@.name=="v1")].served}`,
				),
				commandOptions{capture: true, allowFailure: true},
			)
			if err != nil {
				return err
			}
			if result.exitCode != 0 || strings.TrimSpace(result.stdout) != "True:true" {
				return fmt.Errorf(
					"external cluster must already serve Gateway API v1 Standard resources, including %s",
					name,
				)
			}
		}
	} else {
		if err := o.applyManifest(
			filepath.Join(o.root, "infra/k8s/platform/gateway-api/standard-install.yaml"),
		); err != nil {
			return err
		}
		for _, name := range crds {
			if err := o.k("wait", "--for=condition=Established", "crd/"+name, "--timeout="+o.timeout); err != nil {
				return err
			}
		}
	}
	if err := o.helm(
		"upgrade",
		"--install",
		"istio-base",
		filepath.Join(charts, "base"),
		"-n",
		"istio-system",
		"--wait",
		"--timeout",
		o.timeout,
	); err != nil {
		return err
	}
	return o.helm(
		"upgrade",
		"--install",
		"istiod",
		filepath.Join(charts, "istio-control/istio-discovery"),
		"-n",
		"istio-system",
		"-f",
		filepath.Join(o.root, "infra/k8s/platform/istiod-values.yaml"),
		"--wait",
		"--timeout",
		o.timeout,
	)
}

func (o *operator) buildImages(names []string) error {
	if os.Getenv("K8S_SKIP_BUILD") == "1" {
		return nil
	}
	if err := requireCommands("docker"); err != nil {
		return err
	}
	if o.existing && os.Getenv("K8S_PUSH_IMAGES") != "1" {
		return errors.New(
			"existing cluster image builds require K8S_PUSH_IMAGES=1 and a registry K8S_IMAGE_PREFIX; otherwise use K8S_SKIP_BUILD=1",
		)
	}
	for _, name := range names {
		dockerfile := "service"
		if contains([]string{"frontend", "kafka-init", "migrations"}, name) {
			dockerfile = name
		}
		if err := o.execute(
			"docker",
			"build",
			"--file",
			filepath.Join(o.root, "docker/k8s", dockerfile+".Dockerfile"),
			"--build-arg",
			"SERVICE="+name,
			"--tag",
			o.imageFor(name),
			o.root,
		); err != nil {
			return err
		}
		if o.existing {
			if err := o.execute("docker", "push", o.imageFor(name)); err != nil {
				return err
			}
		} else if err := o.execute("kind", "load", "docker-image", "--name", o.cluster, o.imageFor(name)); err != nil {
			return err
		}
	}
	return nil
}

func (o *operator) renderStage(stage string) (string, error) {
	if err := o.prepareState(); err != nil {
		return "", err
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9._:/-]+$`).MatchString(o.imagePrefix) ||
		!regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`).MatchString(o.imageTag) {
		return "", errors.New("invalid image prefix or tag")
	}
	rendered, err := o.query("kustomize", filepath.Join(o.overlay, stage))
	if err != nil {
		return "", err
	}
	// Production deliberately has no provisioning resources.
	if strings.TrimSpace(rendered) == "" {
		return "", nil
	}
	dir, err := os.MkdirTemp(o.state, ".render-")
	if err != nil {
		return "", err
	}
	defer removeTemporary(dir)
	if err := writePrivate(filepath.Join(dir, "resources.yaml"), rendered); err != nil {
		return "", err
	}
	var config strings.Builder
	config.WriteString(
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - resources.yaml\nimages:\n",
	)
	for _, name := range append(append([]string{}, apps...), "kafka-init", "migrations") {
		fmt.Fprintf(
			&config,
			"  - name: foc/%s\n    newName: %s/%s\n    newTag: %q\n",
			name,
			o.imagePrefix,
			name,
			o.imageTag,
		)
	}
	if err := writePrivate(filepath.Join(dir, "kustomization.yaml"), config.String()); err != nil {
		return "", err
	}
	return o.query("kustomize", dir)
}

func (o *operator) applyStage(stage string) error {
	rendered, err := o.renderStage(stage)
	if err != nil {
		return err
	}
	if strings.TrimSpace(rendered) == "" {
		return nil
	}
	dir, err := os.MkdirTemp(o.state, ".apply-")
	if err != nil {
		return err
	}
	defer removeTemporary(dir)
	path := filepath.Join(dir, "resources.yaml")
	if err := writePrivate(path, rendered); err != nil {
		return err
	}
	return o.applyManifest(path)
}

func (o *operator) patchConfig(data map[string]string) error {
	patch, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		return err
	}
	return o.k("-n", "foc", "patch", "configmap", "foc-config", "--type=merge", "--patch", string(patch))
}

var originKeys = []string{
	"FRONTEND_BASE_URL",
	"ORIGIN",
	"PUBLIC_USER_SERVICE_URL",
	"PUBLIC_SUPPLIER_SERVICE_URL",
	"PUBLIC_ORDER_SERVICE_URL",
	"PUBLIC_COMMUNICATION_SERVICE_URL",
	"PUBLIC_CREDIT_SERVICE_URL",
	"ORDER_S3_PUBLIC_ENDPOINT",
}

func (o *operator) patchLocalConfig() error {
	if !o.isLocal() {
		return nil
	}
	origin, err := localOrigin()
	if err != nil {
		return err
	}
	data := make(map[string]string)
	for _, key := range originKeys {
		data[key] = origin
	}
	return o.patchConfig(data)
}

func (o *operator) deployAll() error {
	if err := o.checkOverlay(); err != nil {
		return err
	}
	if err := o.checkContext(); err != nil {
		return err
	}
	if !o.isLocal() {
		config, err := o.query("kustomize", filepath.Join(o.overlay, "dependencies"))
		if err != nil {
			return err
		}
		if strings.Contains(config, "example.invalid") || strings.Contains(config, "REPLACE_ME") {
			return errors.New(
				"production overlay is a template; configure a real hostname/origin and external Secrets first",
			)
		}
		if o.imagePrefix == "foc" || o.imageTag == "local" {
			return errors.New("production requires a registry K8S_IMAGE_PREFIX and immutable K8S_IMAGE_TAG")
		}
	}
	if err := o.buildImages(append(append([]string{}, apps...), "kafka-init", "migrations")); err != nil {
		return err
	}
	if o.isLocal() {
		if err := o.generateLocalSecrets(); err != nil {
			return err
		}
	} else {
		for _, name := range []string{"user-db", "supplier-db", "order-db", "credit-db", "communication-db", "jwt-signing", "smtp", "kafka-ui-auth", "telegram"} {
			if _, err := o.query("-n", "foc", "get", "secret", name); err != nil {
				return err
			}
		}
		if err := o.checkGatewayTLSSecrets(); err != nil {
			return err
		}
		note("Using operator-provided configuration and Secrets; see the production overlay runbook.")
	}
	for _, stage := range []string{"gateway", "dependencies"} {
		if err := o.applyStage(stage); err != nil {
			return err
		}
	}
	if err := o.patchLocalConfig(); err != nil {
		return err
	}
	if email := os.Getenv("K8S_BOOTSTRAP_SUPERADMIN_EMAIL"); email != "" {
		if err := o.patchConfig(
			map[string]string{
				"BOOTSTRAP_SUPERADMIN_EMAIL":        email,
				"BOOTSTRAP_SUPERADMIN_DISPLAY_NAME": envDefault("K8S_BOOTSTRAP_SUPERADMIN_DISPLAY_NAME", "Admin"),
			},
		); err != nil {
			return err
		}
	}
	if o.isLocal() {
		if err := o.rollout("statefulset/postgres"); err != nil {
			return err
		}
	}
	for _, resource := range []string{"statefulset/kafka", "deployment/kafka-ui"} {
		if err := o.rollout(resource); err != nil {
			return err
		}
	}
	if o.isLocal() {
		for _, resource := range []string{"deployment/mailpit", "deployment/floci"} {
			if err := o.rollout(resource); err != nil {
				return err
			}
		}
		if err := o.prepareJob("db-provision"); err != nil {
			return err
		}
		if err := o.applyStage("provisioning"); err != nil {
			return err
		}
		if err := o.waitJob("db-provision"); err != nil {
			return err
		}
		if err := o.prepareJob("order-storage-init"); err != nil {
			return err
		}
	}
	for _, job := range jobs {
		if err := o.prepareJob(job); err != nil {
			return err
		}
	}
	if err := o.applyStage("initialization"); err != nil {
		return err
	}
	for _, job := range jobs {
		if err := o.waitJob(job); err != nil {
			return err
		}
	}
	if o.isLocal() {
		if err := o.waitJob("order-storage-init"); err != nil {
			return err
		}
	}
	if err := o.applyStage("applications"); err != nil {
		return err
	}
	if err := o.waitForGateway(); err != nil {
		return err
	}
	for _, app := range apps {
		if err := o.k("-n", "foc", "rollout", "restart", "deployment/"+app); err != nil {
			return err
		}
		if err := o.rollout("deployment/" + app); err != nil {
			return err
		}
	}
	note(
		"Bootstrap deployed User, Supplier, Order, Credit, Communication and frontend; the complete D3 business workflow remains unverified.",
	)
	return nil
}

func (o *operator) rollout(resource string) error {
	return o.k("-n", "foc", "rollout", "status", resource, "--timeout="+o.timeout)
}

func (o *operator) deployOne(service string) error {
	if !contains(apps, service) {
		return fmt.Errorf("unknown service %q; use %s", service, strings.Join(apps, ", "))
	}
	if err := o.checkOverlay(); err != nil {
		return err
	}
	if err := o.checkContext(); err != nil {
		return err
	}
	if err := o.buildImages([]string{service}); err != nil {
		return err
	}
	if err := o.k("-n", "foc", "set", "image", "deployment/"+service, service+"="+o.imageFor(service)); err != nil {
		return err
	}
	if err := o.k("-n", "foc", "rollout", "restart", "deployment/"+service); err != nil {
		return err
	}
	if err := o.rollout("deployment/" + service); err != nil {
		return err
	}
	note("Application updated. Schema changes require a full make k8s-deploy first.")
	return nil
}

func (o *operator) up() error {
	if err := o.checkOverlay(); err != nil {
		return err
	}
	var forwards []forwardSpec
	if o.isLocal() {
		var err error
		forwards, err = configuredForwards(os.Getenv)
		if err != nil {
			return err
		}
	}
	if err := preflightForwards(forwards); err != nil {
		return err
	}
	if !o.existing {
		if err := o.createLocalCluster(); err != nil {
			return err
		}
	}
	if err := o.checkContext(); err != nil {
		return err
	}
	if err := o.installMesh(); err != nil {
		return err
	}
	if err := o.deployAll(); err != nil {
		return err
	}
	if !o.isLocal() {
		note("Production overlay selected; automatic local port-forwards are disabled.")
		return nil
	}
	return o.superviseForwards(forwards, false)
}

func (o *operator) deleteCluster() error {
	if o.existing {
		return errors.New("cluster deletion is local-only; unset K8S_CONTEXT and select K8S_CLUSTER explicitly")
	}
	if os.Getenv("K8S_CONFIRM_DELETE") != o.cluster {
		return fmt.Errorf(
			"this deletes cluster %s and its PVC data; rerun with K8S_CONFIRM_DELETE=%s; local credentials are retained",
			o.cluster,
			o.cluster,
		)
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(o.cluster) {
		return errors.New("invalid kind cluster name")
	}
	if err := requireCommands("kind"); err != nil {
		return err
	}
	if err := o.execute("kind", "delete", "cluster", "--name", o.cluster); err != nil {
		return err
	}
	note("Deleted local cluster %s and cluster data. Credentials remain in %s.", o.cluster, o.state)
	return nil
}

func (o *operator) showStatus() error {
	if err := o.checkContext(); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"-n", "foc", "get", "pods,jobs,pvc,services", "-o", "wide"},
		{"-n", "istio-system", "get", "pods"}, {"-n", "istio-ingress", "get", "pods,services"},
		{"get", "gatewayclass/istio"}, {"get", "-A", "gateways,httproutes"},
		{"-n", "foc", "get", "events", "--field-selector", "type=Warning", "--sort-by=.metadata.creationTimestamp"},
	} {
		if err := o.k(args...); err != nil {
			return err
		}
	}
	if _, err := exec.LookPath("istioctl"); err == nil {
		return o.execute("istioctl", append(o.kubePrefix(), "proxy-status")...)
	}
	return nil
}
