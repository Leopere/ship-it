package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Leopere/ship-it/internal/skilldoc"
)

func TestLocalInstallsBothSkillsAndRetiresEnsureShip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeOld := filepath.Join(home, ".claude", "skills", "ensure-ship")
	if err := os.MkdirAll(claudeOld, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeOld, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	codexSkills := filepath.Join(home, ".codex", "skills")
	if err := os.MkdirAll(codexSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(claudeOld, filepath.Join(codexSkills, "ensure-ship")); err != nil {
		t.Fatal(err)
	}

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
		if _, err := os.Lstat(filepath.Join(home, agent, "skills", "ensure-ship")); !os.IsNotExist(err) {
			t.Fatalf("%s ensure-ship still exists", agent)
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, ".local", "share", "ship-it", "retired-skills"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "claude-ensure-ship-") {
		t.Fatalf("retired entries=%v", entries)
	}
	hooks, err := os.ReadFile(filepath.Join(home, ".cursor", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hooks), "ship-it cursor-hook") {
		t.Fatalf("hooks.json missing cursor-hook: %s", hooks)
	}
}
