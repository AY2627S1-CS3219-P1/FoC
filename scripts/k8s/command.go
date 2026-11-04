package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type commandOptions struct {
	capture      bool
	allowFailure bool
	stdinFile    string
}

type commandResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// exec.Command passes arguments directly, without a shell or interpolation.
func (o *operator) run(program string, args []string, options commandOptions) (commandResult, error) {
	cmd := exec.CommandContext(o.ctx, program, args...)
	cmd.Dir = o.root
	cmd.WaitDelay = 3 * time.Second
	var stdout, stderr bytes.Buffer
	if options.capture {
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
	} else {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	if options.stdinFile != "" {
		input, err := os.Open(options.stdinFile)
		if err != nil {
			return commandResult{}, err
		}
		defer func() { _ = input.Close() }()
		cmd.Stdin = input
	}
	err := cmd.Run()
	result := commandResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	// Missing executables and cancellation are failures even when a caller
	// accepts a command's nonzero status.
	if o.ctx.Err() != nil {
		return result, o.ctx.Err()
	}
	if cmd.ProcessState == nil {
		return result, fmt.Errorf("start %s: %w", program, err)
	}
	result.exitCode = cmd.ProcessState.ExitCode()
	if options.allowFailure {
		return result, nil
	}
	return result, fmt.Errorf("%s failed (exit %d): %s", program, result.exitCode, result.stderr)
}

func (o *operator) execute(program string, args ...string) error {
	_, err := o.run(program, args, commandOptions{})
	return err
}

func (o *operator) kubePrefix() []string {
	if o.existing {
		return []string{"--context", o.kubeContext}
	}
	return []string{"--kubeconfig", filepath.Join(o.state, "clusters", o.cluster+".config"), "--context", o.kubeContext}
}

func (o *operator) k(args ...string) error {
	return o.execute("kubectl", append(o.kubePrefix(), args...)...)
}

func (o *operator) query(args ...string) (string, error) {
	result, err := o.run("kubectl", append(o.kubePrefix(), args...), commandOptions{capture: true})
	return result.stdout, err
}

func (o *operator) helm(args ...string) error {
	prefix := []string{"--kube-context", o.kubeContext}
	if !o.existing {
		prefix = append([]string{"--kubeconfig", filepath.Join(o.state, "clusters", o.cluster+".config")}, prefix...)
	}
	return o.execute("helm", append(prefix, args...)...)
}

func requireCommands(names ...string) error {
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("missing %s; run make k8s-tools (Docker and OpenSSL are host prerequisites)", name)
		}
	}
	return nil
}

func (o *operator) aquaWhich(name string) (string, error) {
	result, err := o.run(envDefault("AQUA", "aqua"), []string{"which", name}, commandOptions{capture: true})
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(result.stdout)
	if path == "" {
		return "", fmt.Errorf("aqua could not locate %s; run make k8s-tools", name)
	}
	return path, nil
}

func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Keep manifest writes private: rendered Secrets may contain credentials.
func writePrivate(path, content string) error {
	return os.WriteFile(path, []byte(content), 0600)
}

func printOutput(output string) error {
	_, err := io.WriteString(os.Stdout, output)
	return err
}

func removeTemporary(path string) {
	if err := os.RemoveAll(path); err != nil {
		note("Could not remove temporary path %s: %v", path, err)
	}
}
