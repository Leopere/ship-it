package gitx

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	remote string
	seed   string
	work   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{
		remote: filepath.Join(dir, "remote.git"),
		seed:   filepath.Join(dir, "seed"),
		work:   filepath.Join(dir, "work"),
	}
	runGit(t, dir, "init", "--bare", f.remote)
	runGit(t, dir, "init", "-b", "main", f.seed)
	configure(t, f.seed)
	write(t, filepath.Join(f.seed, "base.txt"), "base\n", 0o644)
	runGit(t, f.seed, "add", "-A")
	runGit(t, f.seed, "commit", "-m", "initial")
	runGit(t, f.seed, "remote", "add", "origin", f.remote)
	runGit(t, f.seed, "push", "-u", "origin", "main")
	runGit(t, f.remote, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, dir, "clone", f.remote, f.work)
	configure(t, f.work)
	return f
}

func TestShipAllChangesBypassesHooksAndTags(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.work, "ship.sh"), "#!/bin/sh\necho old\n", 0o755)
	write(t, filepath.Join(f.work, "feature.txt"), "done\n", 0o644)
	hooks := filepath.Join(f.work, ".git", "hooks")
	write(t, filepath.Join(hooks, "pre-commit"), "#!/bin/sh\nexit 99\n", 0o755)
	write(t, filepath.Join(hooks, "pre-push"), "#!/bin/sh\nexit 99\n", 0o755)

	var out, errOut bytes.Buffer
	repo, err := Open(f.work, "origin", &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EnsureWrapper(); err != nil {
		t.Fatal(err)
	}
	branch, err := repo.DefaultBranch("")
	if err != nil || branch != "main" {
		t.Fatalf("branch=%q err=%v", branch, err)
	}
	result, err := repo.Ship(ShipOptions{Branch: branch, Now: time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("ship: %v\nstderr: %s", err, errOut.String())
	}
	if result.Tag != "v2026.08.08.1" {
		t.Fatalf("tag=%q", result.Tag)
	}
	if got := read(t, filepath.Join(f.work, "ship.sh")); got != Wrapper {
		t.Fatalf("wrapper=%q", got)
	}
	if got := read(t, filepath.Join(f.work, "ship.project.sh")); !strings.Contains(got, "echo old") {
		t.Fatalf("backup=%q", got)
	}
	if got := strings.TrimSpace(outputGit(t, f.remote, "tag", "--list")); got != result.Tag {
		t.Fatalf("remote tag=%q", got)
	}
	if got := strings.TrimSpace(outputGit(t, f.remote, "show", "main:feature.txt")); got != "done" {
		t.Fatalf("remote content=%q", got)
	}
	second, err := repo.Ship(ShipOptions{Branch: branch, Now: time.Date(2026, 8, 8, 13, 0, 0, 0, time.UTC)})
	if err != nil || !second.Noop {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestShipFeatureMergesRemoteAndEndsOnDefault(t *testing.T) {
	f := newFixture(t)
	runGit(t, f.work, "switch", "-c", "feature")
	write(t, filepath.Join(f.work, "local.txt"), "local\n", 0o644)

	other := filepath.Join(filepath.Dir(f.remote), "other")
	runGit(t, filepath.Dir(f.remote), "clone", f.remote, other)
	configure(t, other)
	write(t, filepath.Join(other, "remote.txt"), "remote\n", 0o644)
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "remote update")
	runGit(t, other, "push", "origin", "main")

	var out, errOut bytes.Buffer
	repo, _ := Open(f.work, "origin", &out, &errOut)
	_, _ = repo.EnsureWrapper()
	result, err := repo.Ship(ShipOptions{Branch: "main", Message: "local update", NoTag: true})
	if err != nil {
		t.Fatalf("ship: %v\nstderr: %s", err, errOut.String())
	}
	if result.Branch != "main" || strings.TrimSpace(outputGit(t, f.work, "branch", "--show-current")) != "main" {
		t.Fatalf("did not end on main: %+v", result)
	}
	for _, name := range []string{"local.txt", "remote.txt"} {
		if got := strings.TrimSpace(outputGit(t, f.remote, "show", "main:"+name)); got == "" {
			t.Fatalf("%s missing from remote", name)
		}
	}
}

func TestConflictCanBeResolvedAndRerun(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.work, "base.txt"), "local\n", 0o644)

	other := filepath.Join(filepath.Dir(f.remote), "other")
	runGit(t, filepath.Dir(f.remote), "clone", f.remote, other)
	configure(t, other)
	write(t, filepath.Join(other, "base.txt"), "remote\n", 0o644)
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "remote conflict")
	runGit(t, other, "push", "origin", "main")

	var out, errOut bytes.Buffer
	repo, _ := Open(f.work, "origin", &out, &errOut)
	_, _ = repo.EnsureWrapper()
	_, err := repo.Ship(ShipOptions{Branch: "main", NoTag: true})
	if err == nil || !strings.Contains(err.Error(), "resolve every conflicted file") {
		t.Fatalf("expected actionable conflict, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.work, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("merge state was not preserved: %v", err)
	}
	write(t, filepath.Join(f.work, "base.txt"), "combined\n", 0o644)
	result, err := repo.Ship(ShipOptions{Branch: "main", NoTag: true})
	if err != nil {
		t.Fatalf("rerun: %v\nstderr: %s", err, errOut.String())
	}
	if result.Noop {
		t.Fatal("resolved merge was not pushed")
	}
	if got := strings.TrimSpace(outputGit(t, f.remote, "show", "main:base.txt")); got != "combined" {
		t.Fatalf("resolved content=%q", got)
	}
}

func TestStartAutostashesAndMerges(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.work, "dirty.txt"), "dirty\n", 0o644)
	other := filepath.Join(filepath.Dir(f.remote), "other")
	runGit(t, filepath.Dir(f.remote), "clone", f.remote, other)
	configure(t, other)
	write(t, filepath.Join(other, "remote.txt"), "remote\n", 0o644)
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "remote")
	runGit(t, other, "push", "origin", "main")

	var out, errOut bytes.Buffer
	repo, _ := Open(f.work, "origin", &out, &errOut)
	_, _ = repo.EnsureWrapper()
	if err := repo.Start("main"); err != nil {
		t.Fatalf("start: %v\nstderr: %s", err, errOut.String())
	}
	if got := read(t, filepath.Join(f.work, "dirty.txt")); got != "dirty\n" {
		t.Fatalf("dirty work lost: %q", got)
	}
	if got := read(t, filepath.Join(f.work, "remote.txt")); got != "remote\n" {
		t.Fatalf("remote work absent: %q", got)
	}
}

func configure(t *testing.T, repo string) {
	t.Helper()
	runGit(t, repo, "config", "user.name", "Ship It Test")
	runGit(t, repo, "config", "user.email", "ship-it@example.test")
	runGit(t, repo, "config", "commit.gpgsign", "false")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func outputGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
