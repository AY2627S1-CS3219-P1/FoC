package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Command string

const (
	Serve   Command = "serve"
	Migrate Command = "migrate"
	Live    Command = "live"
	Ready   Command = "ready"
)

func ParseCommand(args []string) (Command, error) {
	if len(args) == 0 {
		return Serve, nil
	}
	if len(args) == 1 && args[0] == "migrate" {
		return Migrate, nil
	}
	if len(args) == 2 && args[0] == "probe" {
		switch Command(args[1]) {
		case Live, Ready:
			return Command(args[1]), nil
		}
	}
	return "", errors.New("usage: server [migrate | probe live | probe ready]")
}

// ExecuteCommand handles one-shot commands before normal application config
// and dependencies are loaded. A false result means the caller should serve.
func ExecuteCommand(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	healthProcedure string,
	migrate func(context.Context, string) error,
) (bool, error) {
	command, err := ParseCommand(args)
	if err != nil {
		return true, err
	}
	if command == Serve {
		return false, nil
	}
	if command == Migrate {
		databaseURL := strings.TrimSpace(getenv("DATABASE_URL"))
		if databaseURL == "" {
			return true, errors.New("DATABASE_URL is required")
		}
		return true, migrate(ctx, databaseURL)
	}
	cfg, err := LoadConfig(getenv)
	if err != nil {
		return true, err
	}
	return true, Probe(ctx, command, cfg, healthProcedure)
}

// Probe calls the local process directly, never through a sidecar or a Service.
// Ready uses the service's Connect health RPC and checks an internal listener
// when configured. It needs no database credentials, authentication keys or mail config.
func Probe(ctx context.Context, command Command, cfg Config, healthProcedure string) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()
	return probe(ctx, client, command, cfg, healthProcedure)
}

func probe(ctx context.Context, client *http.Client, command Command, cfg Config, healthProcedure string) error {
	if command == Live {
		return probeLive(ctx, client, cfg.ExternalProbeURL())
	}
	if command != Ready {
		return errors.New("probe must be live or ready")
	}
	if cfg.InternalPort != "" {
		if err := probeLive(ctx, client, cfg.InternalProbeURL()); err != nil {
			return fmt.Errorf("internal listener: %w", err)
		}
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		cfg.ExternalProbeURL()+healthProcedure,
		strings.NewReader("{}"),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness RPC returned HTTP %d", response.StatusCode)
	}
	var health struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health); err != nil {
		return fmt.Errorf("decode readiness: %w", err)
	}
	if health.Status != "ok" && health.Status != "SERVING" {
		return errors.New("readiness RPC did not report ok")
	}
	return nil
}

func probeLive(ctx context.Context, client *http.Client, baseURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/livez", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("liveness returned HTTP %d", response.StatusCode)
	}
	return nil
}
