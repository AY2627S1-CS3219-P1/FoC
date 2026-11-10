// Command perf provides private fixtures, Kafka probes and database assertions.
// It is an operator tool; it is never mounted in an application server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type result struct {
	Requirement string `json:"requirement"`
	Status      string `json:"status"`
	Details     any    `json:"details"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(
			"usage: perf fixtures|seed|verify|sessions|workflow|contention|events|replay|assert|record [flags]",
		)
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	path := f.String("fixtures", ".local/perf/fixtures.json", "fixture manifest")
	out := f.String("out", ".local/perf", "private output directory")
	base := f.String("base", os.Getenv("PERF_BASE_URL"), "public HTTPS origin")
	seed := f.Int("seed", 3219, "deterministic fixture seed")
	users := f.Int("users", 20000, "fixture users; at least one exclusive account per worker")
	suppliers := f.Int("suppliers", 1000, "supplier Locations")
	open := f.Int("open", 10000, "open requests")
	history := f.Int("history", 100000, "historical requests")
	at := f.String("at", time.Now().UTC().Truncate(time.Hour).Format(time.RFC3339), "fixture reference time")
	challenges := f.String("challenges", "", "private JSON magic-link challenges obtained through RequestLink/email")
	sessions := f.String("sessions", ".local/perf/sessions.json", "exclusive authenticated account pool")
	rounds := f.Int("rounds", 100, "contention rounds")
	count := f.Int("count", 10000, "event count")
	corpus := f.String("corpus", "", "private JSON array of fresh valid event payloads")
	rate := f.Float64("rate", 1000, "events per minute")
	duration := f.Duration("duration", 10*time.Minute, "event publication duration")
	poll := f.Duration("poll", 100*time.Millisecond, "database observation interval")
	listen := f.String("listen", "127.0.0.1:9098", "private recording dependency listener")
	assertions := f.String("assertions", "", "private JSON read-only database assertions")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if err := os.MkdirAll(*out, 0700); err != nil {
		return err
	}

	if args[0] == "fixtures" {
		reference, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			return err
		}
		return generateFixtures(*out, fixtureSpec{*seed, *users, *suppliers, *open, *history, reference})
	}
	if args[0] == "record" {
		return recordDependency(ctx, *listen, *out)
	}
	if args[0] == "assert" {
		return assertDatabases(ctx, *assertions, *out)
	}
	m, err := readFixtures(*path)
	if err != nil {
		return err
	}
	switch args[0] {
	case "seed":
		return seedDatabases(ctx, m, filepath.Dir(*path))
	case "verify":
		return verifyDatabases(ctx, m, *out)
	case "sessions":
		return prepareSessions(ctx, *base, *challenges, *out)
	case "contention":
		return runContention(ctx, m, *base, *sessions, *rounds, *out)
	case "workflow":
		return runWorkflow(ctx, m, *base, *sessions, *out)
	case "events", "replay":
		return runEvents(ctx, m, eventOptions{args[0], *corpus, *count, *rate, *duration, *poll, *out})
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func saveJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

func readJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

func recordResult(out, name string, details any, passed bool) error {
	status := "fail"
	if passed {
		status = "pass"
	}
	if err := saveJSON(filepath.Join(out, name+".json"), result{name, status, details}); err != nil {
		return err
	}
	if !passed {
		return fmt.Errorf("%s failed; inspect private results", name)
	}
	return nil
}
