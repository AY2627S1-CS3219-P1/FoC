// Command healthcheck is the Kubernetes probe binary baked into every service
// image. The runtime image is distroless — no shell, curl or wget — so the
// exec-based probes in infra/k8s need a self-contained program to run:
//
//	healthcheck live <ports>            TCP-dial each loopback port (liveness)
//	healthcheck ready <port> <procedure>  issue the real Connect health RPC
//	                                      (readiness)
//
// "live" proves the process is listening; "ready" proves the service answers
// correctly, so readiness is only reported once the application can serve.
// Built with GO111MODULE=off during image builds; standard library only.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 2 && args[0] == "live" {
		for _, port := range strings.Split(args[1], ",") {
			connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
			if err != nil {
				return err
			}
			_ = connection.Close()
		}
		return nil
	}
	if len(args) != 3 || args[0] != "ready" {
		return errors.New("usage: healthcheck live <ports> | ready <port> <procedure>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+net.JoinHostPort("127.0.0.1", args[1])+args[2], strings.NewReader("{}"))
	if err != nil {
		return err
	}
	// Connect RPCs are POSTs with this content type; the empty request body
	// is valid for every Health/Check procedure.
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	client := &http.Client{
		Timeout: 3 * time.Second,
		// Loopback to our own process; never honor ambient proxy variables.
		Transport: &http.Transport{Proxy: nil},
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health RPC returned HTTP %d", response.StatusCode)
	}
	var health struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health); err != nil {
		return err
	}
	if !isServingStatus(health.Status) {
		return errors.New("health RPC is not serving")
	}
	return nil
}

func isServingStatus(status string) bool {
	return status == "ok" || status == "SERVING" || status == "SERVING_STATUS_SERVING"
}
