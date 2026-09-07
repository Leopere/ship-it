package cursorhook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const hookCommand = "$HOME/.local/bin/ship-it"

func Install(home string, out io.Writer) error {
	path := filepath.Join(home, ".cursor", "hooks.json")
	file, err := readHooks(path)
	if err != nil {
		return err
	}
	if file.Version == 0 {
		file.Version = 1
	}
	if file.Hooks == nil {
		file.Hooks = map[string][]map[string]any{}
	}
	file.Hooks["sessionStart"] = ensureHook(file.Hooks["sessionStart"], map[string]any{"command": hookCommand})
	file.Hooks["stop"] = ensureHook(file.Hooks["stop"], map[string]any{"command": hookCommand})
	if err := writeHooks(path, file); err != nil {
		return err
	}
	fmt.Fprintln(out, "Installed native Cursor lifecycle hooks in", path)
	return nil
}

type hooksFile struct {
	Version int                         `json:"version"`
	Hooks   map[string][]map[string]any `json:"hooks"`
}

func readHooks(path string) (hooksFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return hooksFile{Version: 1, Hooks: map[string][]map[string]any{}}, nil
		}
		return hooksFile{}, err
	}
	var file hooksFile
	if err := json.Unmarshal(data, &file); err != nil {
		return hooksFile{}, err
	}
	return file, nil
}

func writeHooks(path string, file hooksFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ship-it-hooks-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func ensureHook(existing []map[string]any, hook map[string]any) []map[string]any {
	for i, item := range existing {
		command, _ := item["command"].(string)
		if strings.Contains(command, "ship-it") {
			existing[i] = hook
			return existing
		}
	}
	return append(existing, hook)
}
