package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Leopere/ship-it/internal/skilldoc"
)

func TestLocalInstallsSkillsAndBareLifecycleHooks(t *testing.T) {
	home := testDir(t)
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	var out bytes.Buffer
	if err := Local(false, &out); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{".codex", ".claude", ".cursor"} {
		path := filepath.Join(home, agent, "skills", "ship-it", "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != skilldoc.SkillMD {
			t.Fatalf("%s skill differs", agent)
		}
	}
	hooks, err := os.ReadFile(filepath.Join(home, ".cursor", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hooks), "$HOME/.local/bin/ship-it") || strings.Contains(string(hooks), "cursor-hook") {
		t.Fatalf("hooks.json does not use bare ship-it: %s", hooks)
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
