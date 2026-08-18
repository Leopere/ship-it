package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPassesImmutableRevisionFromRepositoryRoot(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, "record")
	script := filepath.Join(bin, "deploy-it")
	body := "#!/bin/sh\nset -eu\nprintf '%s\\n%s' \"$PWD\" \"$*\" > \"$SHIP_IT_DEPLOY_RECORD\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("SHIP_IT_DEPLOY_RECORD", record)
	revision := ShippedRevision{
		Remote: "origin",
		Branch: "main",
		Commit: "0123456789abcdef0123456789abcdef01234567",
		Tag:    "v2026.08.10.1",
	}
	var output bytes.Buffer
	if err := Run(dir, revision, &output, &output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := "--commit " + revision.Commit + " --remote origin --branch main --tag v2026.08.10.1"
	if string(data) != dir+"\n"+wantArgs {
		t.Fatalf("handoff = %q", data)
	}
}

func TestRunReportsPostPushFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "deploy-it")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 17\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	err := Run(dir, ShippedRevision{Commit: "abcdef0123456789"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Git shipping succeeded") || !strings.Contains(err.Error(), "exit status 17") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRequiresDeployIt(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := Run(t.TempDir(), ShippedRevision{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("error = %v", err)
	}
}
