// Command aws prepares AWS configuration and coordinates approved operations.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

type application struct {
	ctx  context.Context
	root string
	out  io.Writer
	err  io.Writer
}

func main() {
	syscall.Umask(0077)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root, err := repositoryRoot()
	if err == nil {
		a := &application{ctx: ctx, root: root, out: os.Stdout, err: os.Stderr}
		err = a.run(os.Args[1:])
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		if ctx.Err() != nil {
			_, _ = fmt.Fprintln(
				os.Stderr,
				"ABORTED: private runtime files and any acquired locks are retained; confirm all descendants stopped before recovery",
			)
			os.Exit(130)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FAILED:", err)
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
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run aws inside the FoC repository")
		}
		dir = parent
	}
}

func (a *application) run(args []string) error {
	for key, value := range map[string]string{"AWS_PAGER": "", "AWS_CLI_AUTO_PROMPT": "off", "TF_IN_AUTOMATION": "1"} {
		if err := os.Setenv(key, value); err != nil {
			return errors.New("cannot configure noninteractive operations")
		}
	}
	if len(args) == 0 {
		return errors.New("use render-overlay or prepare-database")
	}
	switch args[0] {
	case "render-overlay":
		return a.renderCommand(args[1:])
	case "prepare-database":
		return a.databaseCommand(args[1:])
	default:
		return errors.New("unknown AWS command")
	}
}

func (a *application) renderCommand(args []string) error {
	flags := flag.NewFlagSet("render-overlay", flag.ContinueOnError)
	flags.SetOutput(a.err)
	outputs := flags.String("outputs", "", "terraform output -json deployment")
	ca := flags.String("rds-ca", "", "RDS CA PEM bundle")
	destination := flags.String("output", filepath.Join(a.root, ".local/aws/production"), "private generated overlay")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *outputs == "" || *ca == "" || flags.NArg() != 0 {
		return errors.New("render-overlay requires --outputs and --rds-ca")
	}
	config, err := readObject(*outputs)
	if err != nil {
		return err
	}
	pem, err := os.ReadFile(*ca)
	if err != nil {
		return errors.New("cannot read RDS CA bundle")
	}
	return a.renderOverlay(config, *destination, string(pem))
}

func (a *application) databaseCommand(args []string) error {
	flags := flag.NewFlagSet("prepare-database", flag.ContinueOnError)
	flags.SetOutput(a.err)
	outputs := flags.String("outputs", "", "terraform output -json deployment")
	adminPath := flags.String("admin-secret", "", "private Secrets Manager JSON")
	destination := flags.String("directory", filepath.Join(a.root, ".local/aws/db"), "private database artifacts")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *outputs == "" || *adminPath == "" || flags.NArg() != 0 {
		return errors.New("prepare-database requires --outputs and --admin-secret")
	}
	config, err := readObject(*outputs)
	if err != nil {
		return err
	}
	admin, err := readObject(*adminPath)
	if err != nil {
		return err
	}
	return a.prepareDatabase(config, admin, *destination)
}
