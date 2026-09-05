package skilldoc

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratedSkillMatchesRepositoryCopy(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "skill", "ship-it", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != SkillMD {
		t.Fatal("generated SKILL.md drifted; run go generate ./internal/skilldoc")
	}
	metadata, err := os.ReadFile(filepath.Join(root, "skill", "ship-it", "agents", "openai.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(metadata) != OpenAIYAML {
		t.Fatal("generated openai.yaml drifted; run go generate ./internal/skilldoc")
	}
	source, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "internal", "skilldoc", "source.go"), nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var commentSkill string
	for _, group := range source.Comments {
		for _, comment := range group.List {
			text := strings.TrimPrefix(strings.TrimSuffix(comment.Text, "*/"), "/*")
			text = strings.TrimPrefix(text, "\n")
			if strings.HasPrefix(text, sourceMarker+"\n") {
				commentSkill = strings.TrimPrefix(text, sourceMarker+"\n")
			}
		}
	}
	if commentSkill != SkillMD {
		t.Fatal("embedded skill drifted from the canonical Go comment; run go generate ./internal/skilldoc")
	}
}

func TestSkillIncludesConditionalMigrationAndSafeShippingSteering(t *testing.T) {
	for _, required := range []string{
		"SessionStart hook runs `ship-it start` once at the beginning of the local workday",
		"Agents do not run `ship-it start`",
		"Stop hook invokes `ship-it` with no arguments",
		"Agents do not run `ship-it`, poll the hook, or retry it",
		"Activate migration work only when either host appears",
		"`woodpecker.nixc.us`",
		"exact private GitHub owner and repository",
		"exact private `ghcr.io` package targets",
		"self-hosted local runner",
		"`~/dev/gh-runner`",
		"never switch a stalled or insufficient job to GitHub-hosted runners",
		"compare the checked-in scripts and launchd plists with their installed runtime copies",
		"Treat GitHub-hosted runner labels as a shipping defect",
		"[self-hosted, Linux, ARM64, leopere, local]",
		"[self-hosted, macOS, ARM64, leopere, local, native]",
		"existing local reusable workflow with `$/` or `./`",
		"Block remote, dynamic, missing, and unserviceable runner selections",
		"Recheck the final merged tree before a no-op handoff or push",
		"There is no billing-based fallback, public-runner fallback, or hosted-runner exception",
		"visible production acceptance result",
		"Each hook run includes every repository change",
		"Prompt completion, urgent checkpoints, and intermediate turns do not initiate shipping",
	} {
		if !strings.Contains(SkillMD, required) {
			t.Errorf("skill is missing %q", required)
		}
	}
	for _, required := range []string{
		"Finalize every repository cycle and deploy",
		"initialize once at the beginning of the local workday",
	} {
		if !strings.Contains(OpenAIYAML, required) {
			t.Errorf("OpenAI metadata is missing %q", required)
		}
	}
	if strings.Contains(SkillMD, "deploy-it trust") || !strings.Contains(SkillMD, "does not require a machine-local trust record") {
		t.Error("skill still requires the deleted deploy-it trust gate")
	}
}
