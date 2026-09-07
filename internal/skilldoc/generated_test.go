package skilldoc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratedSkillMatchesRepositoryCopies(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	for path, want := range map[string]string{
		filepath.Join(root, "skill", "ship-it", "SKILL.md"):              SkillMD,
		filepath.Join(root, "skill", "ship-it", "agents", "openai.yaml"): OpenAIYAML,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s drifted; run go generate ./internal/skilldoc", path)
		}
	}
}

func TestSkillDescribesOnlyTheSimpleDeliveryPath(t *testing.T) {
	for _, required := range []string{
		"same no-argument `ship-it` binary",
		"SessionStart and Stop are hook events, not command-line subcommands",
		"Scripts must not select a lifecycle phase",
		"including the first call of the day",
		"first invocation for a repository each local day pulls",
		"`git add .`",
		"`git push`",
		"Routine Git commands belong to ship-it",
		"only to diagnose or repair a ship-it defect",
		"commit history is the delivery record",
		"After a successful push",
		"`.deploy-it.json`",
		"Fix recoverable",
	} {
		if !strings.Contains(SkillMD, required) {
			t.Errorf("skill is missing %q", required)
		}
	}
	for _, forbidden := range []string{".ship-it.json", "calendar tag", "GitHub Actions"} {
		if strings.Contains(SkillMD, forbidden) {
			t.Errorf("skill still contains %q", forbidden)
		}
	}
}
