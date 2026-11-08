package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const lokiChartURL = "https://github.com/grafana-community/helm-charts/releases/download/loki-21.0.0/loki-21.0.0.tgz"

// Verify on every use, including cache hits. Helm never resolves an unpinned repo.
func (o *operator) lokiChart() (string, error) {
	checksum, err := os.ReadFile(filepath.Join(o.root, "infra/k8s/observability/chart.sha256"))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(checksum))
	if len(fields) != 2 || len(fields[0]) != 64 {
		return "", errors.New("invalid committed Loki chart checksum")
	}
	path := filepath.Join(o.root, ".tools/charts/loki-21.0.0.tgz")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		req, reqErr := http.NewRequestWithContext(o.ctx, http.MethodGet, lokiChartURL, nil)
		if reqErr != nil {
			return "", reqErr
		}
		client := &http.Client{Timeout: 2 * time.Minute}
		response, requestErr := client.Do(req)
		if requestErr != nil {
			return "", requestErr
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("chart download: HTTP %d", response.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(response.Body, 20<<20))
	}
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != fields[0] {
		return "", fmt.Errorf("Loki chart checksum mismatch: remove %s and retry", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	// Atomic replacement also prevents concurrent readers seeing partial downloads.
	temp, err := os.CreateTemp(filepath.Dir(path), ".loki-*.tgz")
	if err != nil {
		return "", err
	}
	defer removeTemporary(temp.Name())
	_, writeErr := temp.Write(data)
	closeErr := temp.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

func (o *operator) lokiValuesArgs() ([]string, error) {
	dir := filepath.Join(o.root, "infra/k8s/observability")
	environment := "production"
	if o.isLocal() {
		environment = "local"
	}
	args := []string{
		"-n",
		"foc-observability",
		"-f",
		filepath.Join(dir, "values.yaml"),
		"-f",
		filepath.Join(dir, environment+".yaml"),
	}
	if o.isLocal() {
		return args, nil
	}
	bucket, region, role, storage := os.Getenv(
		"K8S_LOKI_BUCKET",
	), os.Getenv(
		"K8S_LOKI_REGION",
	), os.Getenv(
		"K8S_LOKI_IRSA_ROLE",
	), os.Getenv(
		"K8S_LOKI_STORAGE_CLASS",
	)
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(bucket) || bucket == "order-images" ||
		bucket == "REPLACE_ME" ||
		!regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`).MatchString(region) ||
		!regexp.MustCompile(`^arn:aws(-[a-z]+)?:iam::[0-9]{12}:role/[A-Za-z0-9_+=.@/-]+$`).MatchString(role) ||
		!regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`).MatchString(storage) {
		return nil, errors.New(
			"production requires a dedicated K8S_LOKI_BUCKET, K8S_LOKI_REGION, K8S_LOKI_IRSA_ROLE and K8S_LOKI_STORAGE_CLASS",
		)
	}
	for _, setting := range []string{
		"loki.storage.bucketNames.chunks=" + bucket, "loki.storage.bucketNames.ruler=" + bucket, "loki.storage.bucketNames.admin=" + bucket,
		"loki.storage.s3.region=" + region, `serviceAccount.annotations.eks\.amazonaws\.com/role-arn=` + role,
		"singleBinary.persistence.storageClass=" + storage,
	} {
		args = append(args, "--set-string", setting)
	}
	return args, nil
}

func (o *operator) renderLoki() (string, error) {
	args, err := o.lokiValuesArgs()
	if err != nil {
		return "", err
	}
	chart, err := o.lokiChart()
	if err != nil {
		return "", err
	}
	if _, err := o.run("helm", append([]string{"lint", chart}, args...), commandOptions{capture: true}); err != nil {
		return "", err
	}
	result, err := o.run("helm", append([]string{"template", "loki", chart}, args...), commandOptions{capture: true})
	return result.stdout, err
}

func (o *operator) deployObservability() error {
	args, err := o.lokiValuesArgs()
	if err != nil {
		return err
	}
	chart, err := o.lokiChart()
	if err != nil {
		return err
	}
	if o.isLocal() {
		if err := o.prepareJob("loki-bucket-init"); err != nil {
			return err
		}
	}

	if err := o.applyStage("observability"); err != nil {
		return err
	}
	if o.isLocal() {
		if err := o.waitJob("loki-bucket-init"); err != nil {
			return err
		}
	}
	if err := o.helm(
		append(
			[]string{"upgrade", "--install", "loki", chart, "--wait", "--timeout", o.timeout},
			args...)...); err != nil {
		return err
	}
	for _, resource := range []string{"deployment/grafana", "deployment/alloy"} {
		if err := o.k("-n", "foc-observability", "rollout", "status", resource, "--timeout="+o.timeout); err != nil {
			return err
		}
	}
	return nil
}

func (o *operator) checkGrafanaSecret() error {
	if o.isLocal() {
		return nil
	}
	secret, err := o.query(
		"-n",
		"foc-observability",
		"get",
		"secret",
		"grafana-admin",
		"-o",
		`jsonpath={.data.username}:{.data.password}`,
	)
	if err != nil {
		return err
	}
	parts := strings.Split(secret, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("production grafana-admin Secret requires username and password")
	}
	return nil
}

// Use exactly the same immutable images for configuration validation and runtime.
func (o *operator) validateObservability() error {
	rendered, err := o.renderLoki()
	if err != nil {
		return err
	}
	const marker = "  config.yaml: |\n"
	_, block, found := strings.Cut(rendered, marker)
	if !found {
		return errors.New("chart did not render Loki config.yaml")
	}
	block, _, _ = strings.Cut(block, "\n---")
	var config strings.Builder
	for _, line := range strings.Split(block, "\n") {
		config.WriteString(strings.TrimPrefix(line, "    ") + "\n")
	}
	if err := o.prepareState(); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(o.state, ".logging-validation-")
	if err != nil {
		return err
	}
	defer removeTemporary(dir)
	path := filepath.Join(dir, "loki.yaml")
	if err := writePrivate(path, config.String()); err != nil {
		return err
	}
	// Validator containers have no credentials or network and read only public config.
	if err := os.Chmod(dir, 0755); err != nil {
		return err
	}
	if err := os.Chmod(path, 0644); err != nil {
		return err
	}
	if err := o.execute(
		"docker",
		"run",
		"--rm",
		"--network=none",
		"-v",
		path+":/config.yaml:ro",
		"grafana/loki:3.7.9@sha256:0db216b630ae1f348a1584c67f3cead8b4c1fc839169a39501db664744158e28",
		"-config.file=/config.yaml",
		"-verify-config=true",
	); err != nil {
		return err
	}
	return o.execute(
		"docker",
		"run",
		"--rm",
		"--network=none",
		"-v",
		filepath.Join(o.root, "infra/k8s/base/observability/config.alloy")+":/config.alloy:ro",
		"grafana/alloy:v1.21.3@sha256:8ab871206bd2d8f89277f53abffee5f452a293980788d52f85fa183a87a505a4",
		"validate",
		"/config.alloy",
	)
}
