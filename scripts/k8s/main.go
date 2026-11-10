// Command k8s manages the repository's Kubernetes deployment from the host.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"

	"github.com/joho/godotenv"
)

func main() {
	root, err := repositoryRoot()
	if err == nil {
		// Match the documented operator setup: exported values, including empty
		// ones, take precedence over the repository's .env file.
		err = godotenv.Load(filepath.Join(root, ".env"))
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan os.Signal, 1)
	signal.Notify(received, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(received)
	var signalStatus atomic.Int32
	go func() {
		select {
		case sig := <-received:
			code := int32(130)
			if sig == syscall.SIGTERM {
				code = 143
			}
			signalStatus.Store(code)
			cancel()
		case <-ctx.Done():
		}
	}()
	op := newOperator(ctx, root)
	if err := op.runCLI(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		if status := signalStatus.Load(); status != 0 {
			os.Exit(int(status))
		}
		os.Exit(1)
	}
}

func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "aqua.yaml")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "infra/k8s")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run k8s inside the FoC repository")
		}
		dir = parent
	}
}

func (o *operator) runCLI(args []string) error {
	command := "help"
	if len(args) > 0 {
		command = args[0]
	}
	argument := ""
	if len(args) > 1 {
		argument = args[1]
	}
	switch command {
	case "up":
		return o.up()
	case "deploy":
		if argument != "" {
			return o.deployOne(argument)
		}
		return o.deployAll()
	case "render":
		if err := o.checkOverlay(); err != nil {
			return err
		}
		if !contains(stages, argument) {
			return errors.New(
				"render requires gateway, dependencies, provisioning, observability, initialization, or applications",
			)
		}
		rendered, err := o.renderStage(argument)
		if err != nil {
			return err
		}
		if argument == "observability" {
			loki, helmErr := o.renderLoki()
			if helmErr != nil {
				return helmErr
			}
			rendered += "\n---\n" + loki
		}
		_, err = fmt.Fprint(os.Stdout, rendered)
		return err
	case "access":
		forwards, err := configuredForwards(os.Getenv)
		if err != nil {
			return err
		}
		return o.superviseForwards(forwards, true)
	case "kafka-ui", "mailpit", "grafana":
		forwards, err := configuredForwards(os.Getenv)
		if err != nil {
			return err
		}
		for _, forward := range forwards {
			if (command == "kafka-ui" && forward.service == "kafka-ui") ||
				(command == "mailpit" && forward.service == "mailpit") ||
				(command == "grafana" && forward.service == "grafana") {
				return o.superviseForwards([]forwardSpec{forward}, false)
			}
		}
		return fmt.Errorf("set the corresponding K8S_*_PORT to enable %s forwarding", command)
	case "status":
		return o.showStatus()
	case "smoke":
		return o.meshSmoke()
	case "public-smoke":
		return o.publicSmoke()
	case "persistence-smoke":
		return o.persistenceSmoke()
	case "proxy-startup-smoke":
		return o.proxyStartupSmoke()
	case "logging-validate":
		return o.validateObservability()
	case "logging-smoke":
		return o.loggingSmoke()
	case "storage-smoke":
		return o.storageSmoke()
	case "demo":
		return errors.New(
			"D3 demo is unavailable: the integrated registration/credit/order workflow is not verified for this deployment",
		)
	case "delete-cluster":
		return o.deleteCluster()
	default:
		return errors.New(
			"usage: k8s up|deploy [service]|render <stage>|access|status|smoke|public-smoke|logging-smoke|logging-validate|demo|delete-cluster|kafka-ui|mailpit|grafana",
		)
	}
}
