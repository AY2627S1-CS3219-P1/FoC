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
	args := []string{
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
	}
	if topology := os.Getenv("K8S_KIND_CONFIG"); topology != "" {
		args = append(args, "--config", absolute(o.root, topology))
	}
	return o.execute("kind", args...)
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
	platform, err := o.query("kustomize", filepath.Join(o.root, "infra/k8s/platform"))
	if err != nil {
		return err
	}
	if err := o.applyRendered(platform); err != nil {
		return err
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
	return o.applyRendered(rendered)
}

func (o *operator) applyRendered(rendered string) error {
	if err := o.prepareState(); err != nil {
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
	"PUBLIC_MODERATION_SERVICE_URL",
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
	if _, err := o.lokiValuesArgs(); err != nil {
		return err
	}
	if err := o.checkContext(); err != nil {
		return err
	}
	if err := o.checkGrafanaSecret(); err != nil {
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
		for _, name := range []string{"user-db", "supplier-db", "order-db", "credit-db", "communication-db", "moderation-db", "moderation-llm", "jwt-signing", "kafka-ui-auth", "telegram"} {
