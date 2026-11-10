package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func stringValue(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}

func encodeJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return nil, errors.New("cannot encode configuration")
	}
	return out.Bytes(), nil
}

func readObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read private input file")
	}
	return decodeObject(data)
}

func decodeObject(data []byte) (map[string]any, error) {
	var value map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil || value == nil {
		return nil, errors.New("expected a JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected trailing JSON data")
	}
	return value, nil
}

// Resolve existing ancestors too, so a symlink cannot redirect private output
// outside this checkout before the destination itself has been created.
func resolvedPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent := absolute
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		suffix = append(suffix, filepath.Base(parent))
		parent = next
	}
}

func privatePath(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	resolved, err := resolvedPath(path)
	if err != nil {
		return "", errors.New("cannot resolve private path")
	}
	base, err := resolvedPath(filepath.Join(root, ".local"))
	if err != nil {
		return "", errors.New("cannot resolve private directory")
	}
	relative, err := filepath.Rel(base, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return "", errors.New("generated files must stay inside this checkout's ignored .local directory")
	}
	return resolved, nil
}

func savePrivate(root, path string, data []byte) error {
	path, err := privatePath(root, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("cannot create private directory")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".aws-*")
	if err != nil {
		return errors.New("cannot create private file")
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("cannot write private file")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("cannot replace private file")
	}
	return nil
}

func saveJSON(root, path string, value any) error {
	data, err := encodeJSON(value)
	if err != nil {
		return err
	}
	return savePrivate(root, path, data)
}

func object(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func array(value any) []any {
	result, _ := value.([]any)
	return result
}
