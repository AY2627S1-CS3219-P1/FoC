package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (o *operator) gatewaySmokeRequest(
	forward forwardSpec,
	path string,
	expectedStatus int,
	connect bool,
) (string, error) {
	args := []string{
		"--silent",
		"--show-error",
		"--max-time",
		"10",
		"--write-out",
		"\n%{http_code}",
		"--cacert",
		o.localCA(),
	}
	if connect {
		args = append(
			args,
			"--header",
			"Content-Type: application/json",
			"--header",
			"Connect-Protocol-Version: 1",
			"--data",
			"{}",
		)
	}
	args = append(args, fmt.Sprintf("https://localhost:%d%s", forward.localPort, path))
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
