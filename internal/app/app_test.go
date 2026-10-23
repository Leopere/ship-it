package app

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Leopere/ship-it/internal/gitx"
	"github.com/Leopere/ship-it/internal/workday"
)

func TestLifecycleMode(t *testing.T) {
	tests := []struct {
		payload string
		mode    workday.Mode
		run     bool
	}{
		{`{"hook_event_name":"SessionStart"}`, workday.Start, true},
		{`{"hook_event_name":"sessionStart"}`, workday.Start, true},
		{`{"hook_event_name":"Stop"}`, workday.Stop, true},
		{`{"hook_event_name":"stop","status":"completed"}`, workday.Stop, true},
		{`{"hook_event_name":"stop","status":"aborted"}`, workday.Stop, false},
		{`{"hook_event_name":"Stop","stop_hook_active":true}`, workday.Stop, false},
		{`{"hook_event_name":"SessionStart","composer_mode":"ask"}`, workday.Auto, false},
	}
	for _, test := range tests {
		event, ok := decodeHookEvent(strings.NewReader(test.payload))
		if !ok {
			t.Fatalf("could not decode %s", test.payload)
		}
		mode, run := lifecycleMode(event)
		if mode != test.mode || run != test.run {
			t.Fatalf("%s => mode %d run %v", test.payload, mode, run)
		}
	}
}

func TestHookTargetsPayloadRepositoriesAndSkipsBeforeGit(t *testing.T) {
	root := testDir(t)
	firstRemote, firstWork := newRepository(t, root, "first")
	secondRemote, secondWork := newRepository(t, root, "second")
	write(t, filepath.Join(firstWork, "first.txt"), "first\n")
	write(t, filepath.Join(secondWork, "second.txt"), "second\n")
	if err := os.Mkdir(filepath.Join(firstWork, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", testDir(t))
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	var codexOut, codexErr bytes.Buffer
	codexPayload := fmt.Sprintf(`{"hook_event_name":"Stop","status":"completed","cwd":%q}`, firstWork)
	if err := RunInput(nil, "test", strings.NewReader(codexPayload), &codexOut, &codexErr); err != nil {
		t.Fatalf("Codex Stop: %v\n%s", err, codexErr.String())
	}
	if codexOut.String() != "{}\n" || codexErr.String() != "" {
		t.Fatalf("Codex output = %q, stderr = %q", codexOut.String(), codexErr.String())
	}
	if got := strings.TrimSpace(gitOutput(t, firstRemote, "show", "main:first.txt")); got != "first" {
		t.Fatalf("Codex target = %q", got)
	}

	var cursorOut, cursorErr bytes.Buffer
	cursorPayload := fmt.Sprintf(`{"hook_event_name":"stop","status":"completed","cwd":%q,"workspace_roots":[%q,%q,%q]}`,
		outside, firstWork, filepath.Join(firstWork, "nested"), secondWork)
	if err := RunInput(nil, "test", strings.NewReader(cursorPayload), &cursorOut, &cursorErr); err != nil {
		t.Fatalf("Cursor Stop: %v\n%s", err, cursorErr.String())
	}
	if cursorOut.String() != "{}\n" || cursorErr.String() != "" {
		t.Fatalf("Cursor output = %q, stderr = %q", cursorOut.String(), cursorErr.String())
	}
	if got := strings.TrimSpace(gitOutput(t, secondRemote, "show", "main:second.txt")); got != "second" {
		t.Fatalf("Cursor target = %q", got)
	}
}

func TestHookSkipsIrrelevantAndMalformedPayloadBeforeGit(t *testing.T) {
	outside := filepath.Join(testDir(t), "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	for _, payload := range []string{
		`{"hook_event_name":"SessionStart","composer_mode":"ask"}`,
		`{"hook_event_name":"Stop","status":"aborted"}`,
		`{"hook_event_name":"Stop","stop_hook_active":true}`,
		`{"hook_event_name":"unknown"}`,
		`{"hook_event_name":"Stop"`,
	} {
		var out, errOut bytes.Buffer
		if err := RunInput(nil, "test", strings.NewReader(payload), &out, &errOut); err != nil {
			t.Fatalf("%s: %v", payload, err)
		}
		if out.String() != "{}\n" || errOut.String() != "" {
			t.Fatalf("%s: stdout = %q, stderr = %q", payload, out.String(), errOut.String())
		}
	}
}

func TestHookWritesGitFailureToStderr(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	outside := filepath.Join(testDir(t), "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	// A broken worktree marker is a real repository failure. Ordinary folders
	// without a marker are not implicit hook delivery targets.
	write(t, filepath.Join(outside, ".git"), "gitdir: missing-worktree\n")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(testDir(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	var out, errOut bytes.Buffer
	payload := fmt.Sprintf(`{"hook_event_name":"Stop","cwd":%q}`, outside)
	err = RunInput(nil, "test", strings.NewReader(payload), &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "not inside a Git repository") {
		t.Fatalf("error = %v", err)
	}
	if out.String() != "{}\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	want := "fatal: not a git repository:"
	if !strings.Contains(errOut.String(), want) || !strings.Contains(errOut.String(), `"ship_it":"failed"`) {
		t.Fatalf("stderr = %q, want %q", errOut.String(), want)
	}
}

func TestRepositoryDeliveryRejectsArguments(t *testing.T) {
	// Argument errors must steer callers before opening or mutating a repository.
	for _, argument := range []string{"start", "stop", "push", "deploy", "--start"} {
		var output bytes.Buffer
		err := Run([]string{argument}, "test", &output, &output)
		if err == nil || !strings.Contains(err.Error(), "takes no arguments") || !strings.Contains(err.Error(), "use ship-it without arguments") {
			t.Fatalf("%s error = %v", argument, err)
		}
		if output.Len() != 0 {
			t.Fatalf("%s unexpectedly ran delivery: %s", argument, output.String())
		}
	}
}

func TestBareLifecyclePullsThenShipsCompletedCycles(t *testing.T) {
	root := testDir(t)
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	work := filepath.Join(root, "work")
	runGit(t, root, "init", "--bare", remote)
	runGit(t, root, "init", "-b", "main", seed)
	configure(t, seed)
	write(t, filepath.Join(seed, "base.txt"), "base\n")
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-u", "origin", "main")
	runGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, root, "clone", remote, work)
	configure(t, work)

	write(t, filepath.Join(seed, "latest.txt"), "latest\n")
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-m", "latest")
	runGit(t, seed, "push")
	t.Setenv("HOME", testDir(t))
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	// A bare delivery call must ship existing edits even before the daily marker exists.
	write(t, filepath.Join(work, "first-cycle.txt"), "first delivery\n")
	var output bytes.Buffer
	if err := Run(nil, "test", &output, &output); err != nil {
		t.Fatalf("first run: %v\n%s", err, output.String())
	}
	if _, err := os.Stat(filepath.Join(work, "latest.txt")); err != nil {
		t.Fatal("first run did not pull latest code")
	}

	if got := strings.TrimSpace(gitOutput(t, remote, "show", "main:first-cycle.txt")); got != "first delivery" {
		t.Fatalf("first bare call did not ship: %q", got)
	}

	write(t, filepath.Join(work, "cycle.txt"), "done\n")
	if err := Run(nil, "test", &output, &output); err != nil {
		t.Fatalf("second run: %v\n%s", err, output.String())
	}
	if got := strings.TrimSpace(gitOutput(t, remote, "show", "main:cycle.txt")); got != "done" {
		t.Fatalf("remote cycle = %q", got)
	}

	write(t, filepath.Join(work, "next.txt"), "unfinished\n")
	var hookOutput bytes.Buffer
	if err := RunInput(nil, "test", strings.NewReader(`{"hook_event_name":"SessionStart"}`), &hookOutput, &hookOutput); err != nil {
		t.Fatal(err)
	}
	if hookOutput.String() != "{}\n" {
		t.Fatalf("SessionStart output = %q", hookOutput.String())
	}
	if _, err := gitCommand(remote, "show", "main:next.txt"); err == nil {
		t.Fatal("SessionStart shipped unfinished work")
	}
	hookOutput.Reset()
	if err := RunInput(nil, "test", strings.NewReader(`{"hook_event_name":"Stop"}`), &hookOutput, &hookOutput); err != nil {
		t.Fatalf("Stop: %v\n%s", err, output.String())
	}
	if hookOutput.String() != "{}\n" {
		t.Fatalf("Stop output = %q", hookOutput.String())
	}
	if got := strings.TrimSpace(gitOutput(t, remote, "show", "main:next.txt")); got != "unfinished" {
		t.Fatalf("remote next cycle = %q", got)
	}
}

func newRepository(t *testing.T, root, name string) (remote, work string) {
	t.Helper()
	remote = filepath.Join(root, name+".git")
	seed := filepath.Join(root, name+"-seed")
	work = filepath.Join(root, name+"-work")
	runGit(t, root, "init", "--bare", remote)
	runGit(t, root, "init", "-b", "main", seed)
	configure(t, seed)
	write(t, filepath.Join(seed, "base.txt"), "base\n")
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-u", "origin", "main")
	runGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, root, "clone", remote, work)
	configure(t, work)
	return remote, work
}

func testDir(t *testing.T) string {
	t.Helper()
	if os.Getenv("SHIP_IT_KEEP_TEST_DIRS") == "" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("", "ship-it-app-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
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

func gitOutput(t *testing.T, dir string, args ...string) string {
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

func TestPublishAllRepositoriesBeforeDeployment(t *testing.T) {
	for _, scenario := range []string{"success", "push failure", "deployment failure"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("HOME", testDir(t))
			root, err := filepath.EvalSymlinks(testDir(t))
			if err != nil {
				t.Fatal(err)
			}
			firstRemote, first := newRepository(t, root, "first")
			secondRemote, second := newRepository(t, root, "second")
			trace := filepath.Join(root, "trace")
			t.Setenv("DELIVERY_TRACE", trace)
			t.Setenv("SECOND_REMOTE", secondRemote)
			t.Setenv("FIRST_WORK", first)
			t.Setenv("FAIL_DEPLOYMENT", scenario)
			for _, pair := range [][2]string{{firstRemote, first}, {secondRemote, second}} {
				write(t, filepath.Join(pair[1], ".deploy-it.json"), "{}\n")
				hook := filepath.Join(pair[0], "hooks", "post-receive")
				write(t, hook, "#!/bin/sh\nprintf 'push %s\\n' \"$PWD\" >> \"$DELIVERY_TRACE\"\n")
				if err := os.Chmod(hook, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "push failure" {
				runGit(t, first, "remote", "set-url", "--push", "origin", filepath.Join(root, "missing.git"))
			}
			deploy := filepath.Join(root, "deploy-it")
			write(t, deploy, `#!/bin/sh
set -eu
# Every selected publication must finish before even the first observer starts.
git -C "$SECOND_REMOTE" cat-file -e main:.deploy-it.json
printf 'deploy %s\n' "$PWD" >> "$DELIVERY_TRACE"
if [ "$PWD" = "$FIRST_WORK" ] && [ "$FAIL_DEPLOYMENT" = 'deployment failure' ]; then exit 23; fi
`)
			if err := os.Chmod(deploy, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
			var output bytes.Buffer
			observed := make(map[string]error)
			err = runRepositoriesObserved([]string{first, second, first}, workday.Stop, &output, &output, nil, func(root string, repo *gitx.Repo, err error) {
				if _, duplicate := observed[root]; duplicate {
					t.Fatalf("duplicate final result for %s", root)
				}
				observed[root] = err
				if err == nil || repo.DeploymentRequired {
					data, readErr := os.ReadFile(trace)
					if readErr != nil || !strings.Contains(string(data), "deploy "+root+"\n") {
						t.Fatalf("observed final result before deployment: %s, %v, %s", root, readErr, data)
					}
				}
			})
			if (err != nil) != (scenario != "success") {
				t.Fatalf("result = %v\n%s", err, output.String())
			}
			if len(observed) != 2 || observed[second] != nil || (observed[first] != nil) != (scenario != "success") {
				t.Fatalf("final results = %v", observed)
			}
			data, readErr := os.ReadFile(trace)
			if readErr != nil {
				t.Fatal(readErr)
			}
			want := "push " + firstRemote + "\npush " + secondRemote + "\ndeploy " + first + "\ndeploy " + second + "\n"
			if scenario == "push failure" {
				want = "push " + secondRemote + "\ndeploy " + second + "\n"
			}
			if string(data) != want {
				t.Fatalf("delivery order = %q, want %q\n%s", data, want, output.String())
			}
		})
	}
}
