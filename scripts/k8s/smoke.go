package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

var publicHealthPaths = []string{
	"/user.v1.HealthService/Check",
	"/supplier.v1.HealthService/Check",
	"/order.v1.HealthService/Check",
	"/communication.v1.HealthService/Check",
	"/moderation.v1.HealthService/Check",
	"/grpc.health.v1.Health/Check",
}

func (o *operator) gatewaySmokeRequest(
	forward forwardSpec,
	path string,
	expectedStatus int,
	connect bool,
) (string, error) {
	return o.smokeRequest(
		fmt.Sprintf("https://localhost:%d", forward.localPort),
		o.localCA(),
		path,
		expectedStatus,
		connect,
	)
}

func (o *operator) smokeRequest(origin, ca, path string, expectedStatus int, connect bool) (string, error) {
	args := []string{
		"--disable",
		"--silent",
		"--show-error",
		"--max-time",
		"10",
		"--write-out",
		"\n%{http_code}",
	}
	if ca != "" {
		args = append(args, "--cacert", ca)
	}
	if connect {
		// Request JSON for RPC responses; frontend 404 pages may still be HTML.
		args = append(
			args,
			"--header",
			"Accept: application/json",
			"--header",
			"Content-Type: application/json",
			"--header",
			"Connect-Protocol-Version: 1",
			"--data",
			"{}",
		)
	}
	args = append(args, origin+path)
	result, err := o.run("curl", args, commandOptions{capture: true})
	if err != nil {
		return "", err
	}
	index := strings.LastIndex(result.stdout, "\n")
	if index < 0 {
		return "", fmt.Errorf("gateway request did not return an HTTP status for %s", path)
	}
	status, err := strconv.Atoi(strings.TrimSpace(result.stdout[index+1:]))
	if err != nil || status != expectedStatus {
		return "", fmt.Errorf("gateway request %s returned HTTP %d; expected %d", path, status, expectedStatus)
	}
	return result.stdout[:index], nil
}

func (o *operator) assertPublicHealth(forward forwardSpec, path string) error {
	body, err := o.gatewaySmokeRequest(forward, path, 200, true)
	if err != nil {
		return err
	}
	return assertHealthResponse(body, path)
}

func assertHealthResponse(body, path string) error {
	var response struct{ Status string }
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return fmt.Errorf("health RPC %s did not return JSON: %w", path, err)
	}
	if !contains([]string{"ok", "SERVING", "SERVING_STATUS_SERVING"}, response.Status) {
		return fmt.Errorf("health RPC %s did not report serving", path)
	}
	return nil
}

func (o *operator) assertServiceTarget(name string, target int) error {
	output, err := o.query("-n", "foc", "get", "service", name, "-o", "json")
	if err != nil {
		return err
	}
	var service struct {
		Spec struct {
			Ports []struct {
				Port       int
				TargetPort any
			}
		}
	}
	if err := json.Unmarshal([]byte(output), &service); err != nil {
		return err
	}
	for _, port := range service.Spec.Ports {
		if port.Port == 8080 && port.TargetPort == float64(target) {
			return nil
		}
	}
	return fmt.Errorf("service %s does not target application port %d", name, target)
}
