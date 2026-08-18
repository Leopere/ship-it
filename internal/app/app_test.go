package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Leopere/ship-it/internal/gitx"
)

func TestRunShipDeploysAlreadyShippedRevision(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	seed := filepath.Join(dir, "seed")
	work := filepath.Join(dir, "work")
	runGit(t, dir, "init", "--bare", remote)
	runGit(t, dir, "init", "-b", "main", seed)
	runGit(t, seed, "config", "user.name", "Ship It Test")
	runGit(t, seed, "config", "user.email", "ship-it@example.test")
	if err := os.WriteFile(filepath.Join(seed, "ship.sh"), []byte(gitx.Wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "-A")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-u", "origin", "main")
	runGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, dir, "clone", remote, work)

	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, "deploy-record")
	deployIt := filepath.Join(bin, "deploy-it")
	body := "#!/bin/sh\nset -eu\nprintf '%s' \"$*\" > \"$SHIP_IT_DEPLOY_RECORD\"\n"
	if err := os.WriteFile(deployIt, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHIP_IT_DEPLOY_RECORD", record)

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	var out bytes.Buffer
	if err := runShip([]string{"--no-tag"}, "test", &out, &out); err != nil {
		t.Fatalf("runShip: %v\n%s", err, out.String())
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(gitOutput(t, work, "rev-parse", "HEAD"))
	want := "--commit " + commit + " --remote origin --branch main --tag "
	if string(data) != want {
		t.Fatalf("deploy args = %q, want %q", data, want)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
