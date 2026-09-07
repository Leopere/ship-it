package cursorhook

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPreservesOtherHooksAndUsesBareShipIt(t *testing.T) {
	home := testDir(t)
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"hooks":{"stop":[{"command":"tally"},{"command":"$HOME/.local/bin/ship-it cursor-hook"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(home, io.Discard); err != nil {
		t.Fatal(err)
	}
	file, err := readHooks(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Hooks["stop"]) != 2 || file.Hooks["stop"][0]["command"] != "tally" || file.Hooks["stop"][1]["command"] != hookCommand {
		t.Fatalf("stop hooks = %#v", file.Hooks["stop"])
	}
	if hookCommand != "$HOME/.local/bin/ship-it" {
		t.Fatalf("hook command = %q", hookCommand)
	}
}

func testDir(t *testing.T) string {
	t.Helper()
	if os.Getenv("SHIP_IT_KEEP_TEST_DIRS") == "" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("", "ship-it-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
