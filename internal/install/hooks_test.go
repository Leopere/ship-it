package install

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexHooksPreserveOtherHandlersAndInstallIdempotently(t *testing.T) {
	home := testDir(t)
	t.Setenv("CODEX_HOME", "")
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	initial := `{"description":"keep me","custom":true,"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"audit"}]}],"Stop":[{"hooks":[{"type":"command","command":"tally"},{"type":"command","command":"/old/ship-it hook","timeout":100}]}]}}`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installCodexHooks(home, io.Discard); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if err := installCodexHooks(home, io.Discard); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) {
		t.Fatal("reinstall changed hook configuration")
	}
	var file map[string]any
	if err := json.Unmarshal(first, &file); err != nil {
		t.Fatal(err)
	}
	if file["description"] != "keep me" || file["custom"] != true {
		t.Fatal("top-level settings were lost")
	}
	hooks := file["hooks"].(map[string]any)
	if len(hooks["PreToolUse"].([]any)) != 1 || len(hooks["Stop"].([]any)) != 2 {
		t.Fatal("unrelated hooks were lost or owned hooks duplicated")
	}
	for event, want := range map[string]float64{"SessionStart": 300, "Stop": 600} {
		groups := hooks[event].([]any)
		group := groups[len(groups)-1].(map[string]any)
		handler := group["hooks"].([]any)[0].(map[string]any)
		if handler["timeout"] != want {
			t.Fatalf("%s timeout = %v", event, handler["timeout"])
		}
	}
	start := hooks["SessionStart"].([]any)[0].(map[string]any)
	if start["matcher"] != "startup|resume" {
		t.Fatal("startup matcher includes mid-turn baseline resets")
	}
	if strings.Contains(string(first), "followup_message") {
		t.Fatal("hooks must execute, not prompt")
	}
	if strings.Contains(string(first), "ship-it hook") || strings.Contains(string(first), "ship-it start") {
		t.Fatal("hooks must invoke ship-it without arguments")
	}
}

func TestDeliveryGuidanceReplacesTheRepositoryDeliverySection(t *testing.T) {
	home := testDir(t)
	t.Setenv("CODEX_HOME", "")
	path := filepath.Join(home, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	old := "# Keep this\n\nKeep me.\n\n# Repository delivery\n\nOld complicated behavior.\n\n# After\n\nKeep this too.\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateDeliveryGuidance(home); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "# Keep this\n") || !strings.Contains(string(data), "# After") || !strings.Contains(string(data), "no-argument `ship-it`") || strings.Contains(string(data), "Old complicated") {
		t.Fatalf("unexpected guidance: %s", data)
	}
}
