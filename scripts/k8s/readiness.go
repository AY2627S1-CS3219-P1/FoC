package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Reapply the same manifest after an ambiguous API timeout. Never recover by
// deleting Jobs or persistent resources.
func (o *operator) applyManifest(path string) error {
	transient := regexp.MustCompile(
		`etcdserver: request timed out|context deadline exceeded|i/o timeout|TLS handshake timeout|unexpected EOF|ServiceUnavailable|TooManyRequests`,
	)
	for attempt := 0; attempt < 3; attempt++ {
		result, err := o.run(
			"kubectl",
			append(o.kubePrefix(), "apply", "-f", path),
			commandOptions{capture: true, allowFailure: true},
		)
		if err != nil {
			return err
		}
		if err := printOutput(result.stdout); err != nil {
			return err
		}
		if result.exitCode == 0 {
			return nil
		}
		if !transient.MatchString(result.stderr) || attempt == 2 {
			return fmt.Errorf("kubectl apply failed: %s", strings.TrimSpace(result.stderr))
		}
		note("Transient Kubernetes API failure; retrying the same manifest (attempt %d/3).", attempt+2)
		if err := sleep(o.ctx, time.Second*time.Duration(1<<attempt)); err != nil {
			return err
		}
	}
	return nil
}

func (o *operator) prepareJob(name string) error {
	status, err := o.query(
		"-n",
		"foc",
		"get",
		"job",
		name,
		"--ignore-not-found",
		"-o",
		`jsonpath={.metadata.name}:{.status.succeeded}:{.status.conditions[?(@.type=="Failed")].status}`,
	)
	if err != nil {
		return err
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return nil
	}
	remainder, found := strings.CutPrefix(status, name+":")
	if found && (regexp.MustCompile(`^[1-9]`).MatchString(remainder) || strings.HasSuffix(status, ":True")) {
		return o.k("-n", "foc", "delete", "job", name, "--wait=true", "--timeout="+o.timeout)
	}
	return fmt.Errorf("job %s is not terminal; inspect or wait for it before retrying; it has not been deleted", name)
}

func (o *operator) waitJob(name string) error {
	seconds, err := strconv.Atoi(envDefault("K8S_JOB_TIMEOUT_SECONDS", "600"))
	if err != nil || seconds <= 0 {
		return fmt.Errorf("K8S_JOB_TIMEOUT_SECONDS must be a positive integer")
	}
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for time.Now().Before(deadline) {
		output, err := o.query(
			"-n",
			"foc",
			"get",
			"job",
			name,
			"-o",
			`jsonpath={.status.succeeded}:{.status.conditions[?(@.type=="Failed")].status}`,
		)
		if err != nil {
			return err
		}
		status := strings.TrimSpace(output)
		if regexp.MustCompile(`^[1-9][0-9]*:`).MatchString(status) {
			return nil
		}
		if strings.HasSuffix(status, ":True") {
			_, logErr := o.run(
				"kubectl",
				append(o.kubePrefix(), "-n", "foc", "logs", "job/"+name, "--all-containers", "--tail=100"),
				commandOptions{allowFailure: true},
			)
			if logErr != nil {
				note("Could not retrieve Job logs: %v", logErr)
			}
			return fmt.Errorf("job %s failed; fix the cause before retrying", name)
		}
		if err := sleep(o.ctx, 2*time.Second); err != nil {
			return err
		}
	}
	_, describeErr := o.run(
		"kubectl",
		append(o.kubePrefix(), "-n", "foc", "describe", "job", name),
		commandOptions{allowFailure: true},
	)
	if describeErr != nil {
		note("Could not describe Job: %v", describeErr)
	}
	return fmt.Errorf("timed out waiting for job %s; existing running Jobs are preserved", name)
}

func (o *operator) waitForGateway() error {
	if err := o.k("wait", "--for=condition=Accepted", "gatewayclass/istio", "--timeout="+o.timeout); err != nil {
		return err
	}
	if err := o.k(
		"-n",
		"istio-ingress",
		"wait",
		"--for=condition=Programmed",
		"gateway/foc-public",
		"--timeout="+o.timeout,
	); err != nil {
		return err
	}
	routes := append([]string{}, apps...)
	if o.isLocal() {
		routes = append(routes, "floci-images")
	}
	for _, route := range routes {
		if err := o.waitForHTTPRoute(route); err != nil {
			return err
		}
	}
	return o.k("-n", "istio-ingress", "rollout", "status", "deployment/foc-public-istio", "--timeout="+o.timeout)
}

type routeCondition struct{ Type, Status, Reason, Message string }

func (o *operator) waitForHTTPRoute(name string) error {
	timeout, err := time.ParseDuration(o.timeout)
	if err != nil || timeout <= 0 {
		return fmt.Errorf("K8S_TIMEOUT must be a positive duration such as 600s or 10m")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		result, err := o.query("-n", "foc", "get", "httproute", name, "-o", "json")
		if err != nil {
			return err
		}
		var route struct {
			Status struct {
				Parents []struct {
					ParentRef      struct{ Name string }
					ControllerName string
					Conditions     []routeCondition
				}
			}
		}
		if err := json.Unmarshal([]byte(result), &route); err != nil {
			return err
		}
		for _, parent := range route.Status.Parents {
			if parent.ParentRef.Name != "foc-public" || parent.ControllerName != "istio.io/gateway-controller" {
				continue
			}
			ready := 0
			for _, condition := range parent.Conditions {
				if condition.Type != "Accepted" && condition.Type != "ResolvedRefs" {
					continue
				}
				if condition.Status == "False" {
					return fmt.Errorf("HTTPRoute %s was rejected: %s: %s", name, condition.Reason, condition.Message)
				}
				if condition.Status == "True" {
					ready++
				}
			}
			if ready == 2 {
				return nil
			}
		}
		if err := sleep(o.ctx, time.Second); err != nil {
			return err
		}
	}
	return fmt.Errorf("timed out waiting for HTTPRoute %s to be accepted with resolved backend references", name)
}

func (o *operator) checkGatewayOrigin(forwards []forwardSpec) error {
	if o.existing || !o.isLocal() {
		return nil
	}
	for _, forward := range forwards {
		if forward.service != "foc-public-istio" {
			continue
		}
		expected := fmt.Sprintf("https://localhost:%d", forward.localPort)
		output, err := o.query("-n", "foc", "get", "configmap", "foc-config", "-o", "json")
		if err != nil {
			return err
		}
		var config struct{ Data map[string]string }
		if err := json.Unmarshal([]byte(output), &config); err != nil {
			return err
		}
		for _, key := range originKeys {
			if config.Data[key] != expected {
				return fmt.Errorf(
					"gateway forward %s does not match deployed browser origins; rerun a full make k8s-up or make k8s-deploy with K8S_HTTPS_PORT=%d",
					expected,
					forward.localPort,
				)
			}
		}
	}
	return nil
}

// Used by smoke checks when locating the pinned local TLS certificate.
func (o *operator) localCA() string {
	return envDefault("K8S_TLS_CA_FILE", o.state+string(os.PathSeparator)+"secrets/tls.crt")
}
