package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type portForward struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error // Written before done closes; readers wait for done.
}

func waitForLocalListener(port int) bool {
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Launch the real kubectl binary (not a shell shim), so signals reach the
// port-forward process. The caller owns only successfully started forwards.
func (o *operator) startForward(forward forwardSpec) (*portForward, error) {
	kubectl, err := o.aquaWhich("kubectl")
	if err != nil {
		return nil, err
	}
	args := append(
		o.kubePrefix(),
		"-n",
		forward.namespace,
		"port-forward",
		"--address",
		"127.0.0.1",
		"service/"+forward.service,
		fmt.Sprintf("%d:%d", forward.localPort, forward.remotePort),
	)
	cmd := exec.Command(kubectl, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = o.root, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	child := &portForward{cmd: cmd, done: make(chan struct{})}
	go func() { child.err = cmd.Wait(); close(child.done) }()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-o.ctx.Done():
			stopForwards([]*portForward{child})
			return nil, o.ctx.Err()
		case <-child.done:
			return nil, fmt.Errorf("%s port-forward exited before becoming ready: %v", forward.name, child.err)
		default:
		}
		if waitForLocalListener(forward.localPort) {
			note("%s: %s://localhost:%d", forward.name, forward.scheme, forward.localPort)
			return child, nil
		}
		if err := sleep(o.ctx, 100*time.Millisecond); err != nil {
			stopForwards([]*portForward{child})
			return nil, err
		}
	}
	stopForwards([]*portForward{child})
	return nil, fmt.Errorf("%s port-forward did not become ready on host port %d", forward.name, forward.localPort)
}

func stopForwards(children []*portForward) {
	for _, child := range children {
		_ = child.cmd.Process.Signal(syscall.SIGTERM)
	}
	// A shared grace period bounds cleanup even if several children hang.
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for _, child := range children {
		select {
		case <-child.done:
		case <-deadline.C:
			for _, remaining := range children {
				_ = remaining.cmd.Process.Kill()
			}
			for _, remaining := range children {
				<-remaining.done
			}
			return
		}
	}
}

func (o *operator) superviseForwards(forwards []forwardSpec, tolerateFailures bool) error {
	if len(forwards) == 0 {
		note("No Kubernetes port-forwards configured; deployment is ready.")
		return nil
	}
	if err := o.checkContext(); err != nil {
		return err
	}
	if err := o.checkGatewayOrigin(forwards); err != nil {
		return err
	}
	var children []*portForward
	defer func() { stopForwards(children) }()
	for _, forward := range forwards {
		err := assertPortAvailable(forward.localPort)
		if err == nil {
			var child *portForward
			child, err = o.startForward(forward)
			if err == nil {
				children = append(children, child)
			}
		}
		if err != nil {
			if !tolerateFailures || o.ctx.Err() != nil {
				return err
			}
			note("Skipping %s: %v", forward.name, err)
		}
	}
	if len(children) == 0 {
		return errors.New("no forwards could be attached")
	}
	note(
		"Kubernetes forwards are attached. Press Ctrl+C to stop the forwards; the cluster and its data remain running.",
	)
	watchCtx, stop := context.WithCancel(o.ctx)
	defer stop()
	exited := make(chan error, len(children))
	for _, child := range children {
		go func() {
			select {
			case <-watchCtx.Done():
			case <-child.done:
				exited <- fmt.Errorf("a kubectl port-forward exited unexpectedly: %v", child.err)
			}
		}()
	}
	select {
	case <-o.ctx.Done():
		return o.ctx.Err()
	case err := <-exited:
		return err
	}
}
