package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

func (o *operator) smokeGateway() error {
	if !o.isLocal() {
		return errors.New("the Gateway smoke command currently supports the local overlay only")
	}
	if err := o.checkContext(); err != nil {
		return err
	}
	if err := o.waitForGateway(); err != nil {
		return err
	}
	if err := requireCommands("curl"); err != nil {
		return err
	}
	forwards, err := configuredForwards(os.Getenv)
	if err != nil {
		return err
	}
	var gateway *forwardSpec
	for i := range forwards {
		if forwards[i].service == "foc-public-istio" {
			gateway = &forwards[i]
			break
		}
	}
	if gateway == nil {
		return errors.New("set K8S_HTTPS_PORT to run the Gateway smoke check")
	}
	var children []*portForward
	defer func() { stopForwards(children) }()
	if waitForLocalListener(gateway.localPort) {
		note("Reusing the gateway forward already attached on localhost:%d.", gateway.localPort)
	} else {
		if err := preflightForwards([]forwardSpec{*gateway}); err != nil {
			return err
		}
		child, err := o.startForward(*gateway)
		if err != nil {
			return err
		}
		children = append(children, child)
	}
	if _, err := o.gatewaySmokeRequest(*gateway, "/", 200, false); err != nil {
		return err
	}
	for _, path := range []string{"/user.v1.HealthService/Check", "/supplier.v1.HealthService/Check", "/order.v1.HealthService/Check", "/communication.v1.HealthService/Check", "/grpc.health.v1.Health/Check"} {
		if err := o.assertPublicHealth(*gateway, path); err != nil {
			return err
		}
	}
	for _, path := range []string{"/user.v1.PublicKeyService/GetPublicKeys", "/credit.v1.CreditReservationService/Reserve"} {
		if _, err := o.gatewaySmokeRequest(*gateway, path, 404, true); err != nil {
			return err
		}
	}
	for _, service := range []string{"user-service", "credit-service"} {
		if err := o.assertServiceTarget(service+"-external", 8080); err != nil {
			return err
		}
		if err := o.assertServiceTarget(service+"-internal", 8081); err != nil {
			return err
		}
	}
	note(
		"Gateway smoke passed: frontend and public health RPCs respond; User keys and Credit reservations are not public; Services target separate public/internal ports.",
	)
	return nil
}
