// Command seed runs ordered local SQL fixtures through psql. Transactions and
// reset semantics belong to the SQL files, not this orchestration command.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
)

type fixture struct {
	File     string `json:"file"`
	Database string `json:"database"`
	content  []byte
}

type manifest struct {
	Version int       `json:"version"`
	Files   []fixture `json:"files"`
}

type executeFixture func(context.Context, fixture, bool) error

func main() {
	path := flag.String("manifest", "data/seeds/manifest.json", "ordered SQL fixture manifest")
	projectDir := flag.String("project-dir", ".", "Compose project directory")
	reset := flag.Bool("reset", false, "pass reset_demo=true to each SQL file")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prefix := []string{"docker", "compose", "--project-directory", *projectDir, "exec", "-T", "postgres"}
	if err := run(ctx, *path, *reset, psqlExecutor(prefix, os.Stdout, os.Stderr), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// loadManifest validates every input before any command can write to a database.
// Files are resolved relative to the manifest and cannot escape its directory.
func loadManifest(path string) ([]fixture, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m manifest
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("manifest must contain one JSON object")
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("unsupported manifest version %d", m.Version)
	}
	if len(m.Files) == 0 {
		return nil, fmt.Errorf("manifest must contain at least one SQL file")
	}
	directory, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	target := regexp.MustCompile(`^[A-Z][A-Z0-9_]*_DB$`)
	seen := make(map[string]bool, len(m.Files))
	for i := range m.Files {
		f := &m.Files[i]
		if !target.MatchString(f.Database) {
			return nil, fmt.Errorf("file %d: database must name a container environment variable ending in _DB", i+1)
		}
		if !filepath.IsLocal(f.File) || filepath.Ext(f.File) != ".sql" {
			return nil, fmt.Errorf("file %d: expected a relative .sql path within the manifest directory", i+1)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(directory, f.File))
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", f.File, err)
		}
		relative, err := filepath.Rel(directory, resolved)
		if err != nil || !filepath.IsLocal(relative) {
			return nil, fmt.Errorf("file %s escapes the manifest directory", f.File)
		}
		key := f.Database + ":" + resolved
		if seen[key] {
			return nil, fmt.Errorf("duplicate fixture %s for %s", f.File, f.Database)
		}
		seen[key] = true
		f.content, err = os.ReadFile(resolved)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.File, err)
		}
		if len(bytes.TrimSpace(f.content)) == 0 {
			return nil, fmt.Errorf("SQL fixture %s is empty", f.File)
		}
	}
	return m.Files, nil
}

func run(ctx context.Context, path string, reset bool, execute executeFixture, output io.Writer) error {
	files, err := loadManifest(path)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "Seeding %s (%s)\n", f.File, f.Database); err != nil {
			return fmt.Errorf("write seed progress: %w", err)
		}
		if err := execute(ctx, f, reset); err != nil {
			return fmt.Errorf("seed %s (%s): %w", f.File, f.Database, err)
		}
	}
	return nil
}

// The shell program is fixed. Database variable names and reset values are
// positional arguments; file contents go to stdin, never into shell code.
const psqlScript = `database_name=$(printenv "$1")
: "${database_name:?database environment variable is empty}"
exec psql -X -q -U "$POSTGRES_USER" -d "$database_name" -v ON_ERROR_STOP=1 -v reset_demo="$2"`

func psqlExecutor(prefix []string, stdout, stderr io.Writer) executeFixture {
	return func(ctx context.Context, f fixture, reset bool) error {
		args := append([]string(nil), prefix[1:]...)
		args = append(args, "sh", "-eu", "-c", psqlScript, "seed-psql", f.Database, strconv.FormatBool(reset))
		command := exec.CommandContext(ctx, prefix[0], args...)
		command.Stdin = bytes.NewReader(f.content)
		command.Stdout = stdout
		command.Stderr = stderr
		return command.Run()
	}
}
