package gitx

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fixture struct {
	remote string
	seed   string
	work   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := testDir(t)
	f := fixture{filepath.Join(dir, "remote.git"), filepath.Join(dir, "seed"), filepath.Join(dir, "work")}
	runGit(t, dir, "init", "--bare", f.remote)
	runGit(t, dir, "init", "-b", "main", f.seed)
	configure(t, f.seed)
	write(t, filepath.Join(f.seed, "tracked.txt"), "base\n")
	runGit(t, f.seed, "add", ".")
	runGit(t, f.seed, "commit", "-m", "initial")
	runGit(t, f.seed, "remote", "add", "origin", f.remote)
	runGit(t, f.seed, "push", "-u", "origin", "main")
	runGit(t, f.remote, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, dir, "clone", f.remote, f.work)
	configure(t, f.work)
	return f
}

func TestPullGetsLatestUpstreamCode(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.seed, "remote.txt"), "latest\n")
	runGit(t, f.seed, "add", ".")
	runGit(t, f.seed, "commit", "-m", "remote")
	runGit(t, f.seed, "push")
	var output bytes.Buffer
	repo, err := Open(f.work, &output, &output)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Pull(); err != nil {
		t.Fatalf("pull: %v\n%s", err, output.String())
	}
	if got := read(t, filepath.Join(f.work, "remote.txt")); got != "latest\n" {
		t.Fatalf("remote content = %q", got)
	}
}

func TestShipStagesCommitsAndPushesEverything(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.work, "tracked.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.work, "staged.txt"), "staged\n")
	runGit(t, f.work, "add", "staged.txt")
	write(t, filepath.Join(f.work, "untracked.txt"), "untracked\n")
	var output bytes.Buffer
	repo, err := Open(f.work, &output, &output)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Ship(); err != nil {
		t.Fatalf("ship: %v\n%s", err, output.String())
	}
	for _, name := range []string{"staged.txt", "untracked.txt"} {
		if got := strings.TrimSpace(outputGit(t, f.remote, "show", "main:"+name)); got == "" {
			t.Fatalf("%s was not pushed", name)
		}
	}
	if _, err := gitCommand(f.remote, "show", "main:tracked.txt"); err == nil {
		t.Fatal("deleted file remains on remote")
	}
	if got := strings.TrimSpace(outputGit(t, f.remote, "tag", "--list")); got != "" {
		t.Fatalf("unexpected tags: %s", got)
	}
	if _, err := os.Stat(filepath.Join(f.work, "ship.sh")); !os.IsNotExist(err) {
		t.Fatal("ship created a wrapper file")
	}
}

func TestShipBypassesFailingHooksAndCommitSigning(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"pre-commit", "pre-push"} {
		path := filepath.Join(f.work, ".git", "hooks", name)
		write(t, path, "#!/bin/sh\nexit 99\n")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, f.work, "config", "commit.gpgsign", "true")
	write(t, filepath.Join(f.work, "hook-proof.txt"), "shipped\n")
	repo, _ := Open(f.work, nil, nil)
	if err := repo.Ship(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(outputGit(t, f.remote, "show", "main:hook-proof.txt")); got != "shipped" {
		t.Fatalf("remote content = %q", got)
	}
}

func TestCleanShipPushesAnExistingCommit(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.work, "ahead.txt"), "ahead\n")
	runGit(t, f.work, "add", ".")
	runGit(t, f.work, "commit", "-m", "ahead")
	repo, _ := Open(f.work, nil, nil)
	if err := repo.Ship(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(outputGit(t, f.remote, "show", "main:ahead.txt")); got != "ahead" {
		t.Fatalf("remote content = %q", got)
	}
}

func TestShipHandsOffOnlyAfterSuccessfulPush(t *testing.T) {
	for _, scenario := range []string{"success", "deployment failure", "push failure", "no contract"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			bin := testDir(t)
			record := filepath.Join(bin, "arguments")
			postPushEdit := filepath.Join(f.work, "post-push-edit.txt")
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$@\" > \"$DEPLOY_RECORD\"\n"
			if scenario == "deployment failure" {
				script += "exit 23\n"
			}
			write(t, filepath.Join(bin, "deploy-it"), script)
			if err := os.Chmod(filepath.Join(bin, "deploy-it"), 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(bin, "git"), "#!/bin/sh\nset -eu\n\"$REAL_GIT\" \"$@\"\nstatus=$?\nif [ \"$status\" -eq 0 ] && [ \"${3:-}\" = push ]; then\n  printf 'concurrent edit\\n' > \"$POST_PUSH_EDIT\"\nfi\nexit \"$status\"\n")
			if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("REAL_GIT", realGit)
			t.Setenv("DEPLOY_RECORD", record)
			t.Setenv("POST_PUSH_EDIT", postPushEdit)
			if scenario != "no contract" {
				write(t, filepath.Join(f.work, ".deploy-it.json"), "{}\n")
			}
			write(t, filepath.Join(f.work, "changed.txt"), "requested result\n")
			if scenario == "push failure" {
				runGit(t, f.work, "remote", "set-url", "--push", "origin", filepath.Join(bin, "missing.git"))
			}
			previousExecutablePath := executablePath
			executablePath = func() (string, error) { return filepath.Join(bin, "missing-ship-it"), nil }
			t.Cleanup(func() { executablePath = previousExecutablePath })
			repo, _ := Open(f.work, nil, nil)
			err = repo.Ship()
			switch scenario {
			case "push failure":
				if repo.DeploymentRequired {
					t.Fatal("push failure incorrectly reported as a deployment attempt")
				}
				if err == nil || !strings.Contains(err.Error(), "Git push failed") {
					t.Fatalf("error = %v", err)
				}
			case "deployment failure":
				if err == nil || !strings.Contains(err.Error(), "Git push succeeded; deployment failed") {
					t.Fatalf("error = %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			data, readErr := os.ReadFile(record)
			if scenario == "push failure" || scenario == "no contract" {
				if !os.IsNotExist(readErr) {
					t.Fatalf("unexpected deployment: %q, %v", data, readErr)
				}
				return
			}
			pushedCommit := strings.TrimSpace(outputGit(t, f.remote, "rev-parse", "main"))
			want := "--commit\n" + pushedCommit + "\n--branch\nmain\n--remote\norigin\n"
			if readErr != nil || string(data) != want {
				t.Fatalf("deployment arguments = %q, error = %v", data, readErr)
			}
			if got := read(t, postPushEdit); got != "concurrent edit\n" {
				t.Fatalf("post-push edit = %q", got)
			}
			if status := outputGit(t, f.work, "status", "--porcelain"); !strings.Contains(status, "?? post-push-edit.txt") {
				t.Fatalf("concurrent edit was not preserved: %q", status)
			}
		})
	}
}

func TestPushDestinationMatchesBareGitPush(t *testing.T) {
	for name, prepare := range map[string]func(*testing.T, fixture){
		"simple": func(t *testing.T, f fixture) {},
		"upstream": func(t *testing.T, f fixture) {
			runGit(t, f.work, "config", "push.default", "upstream")
		},
		"current": func(t *testing.T, f fixture) {
			delivery := filepath.Join(testDir(t), "delivery.git")
			runGit(t, filepath.Dir(delivery), "init", "--bare", delivery)
			runGit(t, f.work, "remote", "add", "delivery", delivery)
			runGit(t, f.work, "config", "branch.main.pushRemote", "delivery")
			runGit(t, f.work, "config", "push.default", "current")
		},
		"triangular simple": func(t *testing.T, f fixture) {
			delivery := filepath.Join(testDir(t), "delivery.git")
			runGit(t, filepath.Dir(delivery), "init", "--bare", delivery)
			runGit(t, f.work, "remote", "add", "delivery", delivery)
			runGit(t, f.work, "config", "branch.main.pushRemote", "delivery")
			runGit(t, f.work, "config", "push.default", "simple")
		},
		"matching": func(t *testing.T, f fixture) {
			runGit(t, f.work, "config", "push.default", "matching")
		},
		"explicit refspec": func(t *testing.T, f fixture) {
			delivery := filepath.Join(testDir(t), "delivery.git")
			runGit(t, filepath.Dir(delivery), "init", "--bare", delivery)
			runGit(t, f.work, "remote", "add", "delivery", delivery)
			runGit(t, f.work, "config", "branch.main.pushRemote", "delivery")
			runGit(t, f.work, "config", "remote.delivery.push", "main:production")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			prepare(t, f)
			repo, _ := Open(f.work, nil, nil)
			got, err := repo.pushDestination()
			if err != nil {
				t.Fatal(err)
			}
			want := pushDestination{Remote: "origin", Branch: "main"}
			if name == "current" || name == "triangular simple" {
				want.Remote = "delivery"
			}
			if name == "explicit refspec" {
				want = pushDestination{Remote: "delivery", Branch: "production"}
			}
			if got != want {
				t.Fatalf("destination = %#v, want %#v", got, want)
			}
		})
	}
}

func TestPushDestinationRejectsAmbiguousMappings(t *testing.T) {
	for name, prepare := range map[string]func(*testing.T, fixture){
		"multiple explicit destinations": func(t *testing.T, f fixture) {
			runGit(t, f.work, "config", "remote.origin.push", "refs/heads/main:refs/heads/production")
			runGit(t, f.work, "config", "--add", "remote.origin.push", "refs/heads/main:refs/heads/canary")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			prepare(t, f)
			repo, _ := Open(f.work, nil, nil)
			if _, err := repo.pushDestination(); err == nil {
				t.Fatal("ambiguous push destination was accepted")
			}
		})
	}
}

func TestDeployItPathPrefersSiblingThenPATH(t *testing.T) {
	bin := testDir(t)
	sibling := filepath.Join(bin, "deploy-it")
	pathFallback := filepath.Join(testDir(t), "deploy-it")
	for _, path := range []string{sibling, pathFallback} {
		write(t, path, "#!/bin/sh\nexit 0\n")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(bin, "ship-it"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", filepath.Dir(pathFallback))
	previousExecutablePath := executablePath
	t.Cleanup(func() { executablePath = previousExecutablePath })
	executablePath = func() (string, error) { return filepath.Join(bin, "ship-it"), nil }
	if got, err := deployItPath(); err != nil || got != sibling {
		t.Fatalf("sibling deploy-it = %q, error = %v", got, err)
	}
	executablePath = func() (string, error) { return filepath.Join(testDir(t), "missing-ship-it"), nil }
	if got, err := deployItPath(); err != nil || got != pathFallback {
		t.Fatalf("PATH deploy-it = %q, error = %v", got, err)
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
	out, err := gitCommand(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func outputGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCommand(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func gitCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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
