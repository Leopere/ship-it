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
		"main and final delivery path for every repository work cycle",
		"the cycle is not complete until `ship-it` resolves the repository",
		"Activate migration work only when that host appears",
		"private repository",
		"`ghcr.io` image targets",
		"visible production acceptance result",
		"Each run must include every repository change",
		"verified, production-safe checkpoint",
		"In Cursor, wrapping a prompt",
	} {
		if !strings.Contains(SkillMD, required) {
			t.Errorf("skill is missing %q", required)
		}
	}
	for _, required := range []string{
		"Finalize every repository cycle and deploy",
		"required start and finalizer for this repository cycle",
	} {
		if !strings.Contains(OpenAIYAML, required) {
			t.Errorf("OpenAI metadata is missing %q", required)
		}
	}
}
