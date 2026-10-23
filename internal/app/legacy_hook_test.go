package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLegacyHookDetachedDelivery(t *testing.T) {
	root := testDir(t)
	home := filepath.Join(root, "home")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shipIt := filepath.Join(root, "ship-it")
	build := exec.Command("go", "build", "-work", "-o", shipIt, "./cmd/ship-it")
	build.Dir = repoRoot(t)
	build.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ship-it: %v\n%s", err, output)
	}
	baseEnv := append(os.Environ(),
		"HOME="+home,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	for _, event := range []string{"SessionStart", "Stop"} {
		t.Run("native non-repository "+event, func(t *testing.T) {
			cmd := exec.Command(shipIt)
			cmd.Dir, cmd.Env = root, baseEnv
			cmd.Stdin = strings.NewReader(fmt.Sprintf(`{"hook_event_name":%q,"cwd":%q}`, event, root))
			out, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != "{}" {
				t.Fatalf("no-op hook: %s, %v", out, err)
			}
			if logs := hookLogs(t, home); len(logs) != 0 {
				t.Fatalf("no-op queued work: %v", logs)
			}
		})
	}

	t.Run("empty pipe ships and malformed input does not", func(t *testing.T) {
		remote, work := newRepository(t, root, "empty-stdin")
		write(t, filepath.Join(work, "change.txt"), "empty pipe delivery\n")
		for _, payload := range []string{"not a hook", ""} {
			cmd := exec.Command(shipIt)
			cmd.Dir, cmd.Env, cmd.Stdin = work, baseEnv, strings.NewReader(payload)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("stdin %q: %v\n%s", payload, err, output)
			}
			probe := exec.Command("git", "-C", remote, "show", "main:change.txt")
			output, err := probe.CombinedOutput()
			if payload != "" {
				if err == nil {
					t.Fatal("malformed input published the worktree")
				}
			} else if err != nil || strings.TrimSpace(string(output)) != "empty pipe delivery" {
				t.Fatalf("empty pipe did not publish: %v\n%s", err, output)
			}
		}
	})

	for _, event := range []string{"Stop", "SessionStart"} {
		t.Run("native "+event+" survives runner cancellation", func(t *testing.T) {
			remote, work := newRepository(t, root, "native-"+event)
			write(t, filepath.Join(work, "change.txt"), "native delivery\n")
			write(t, filepath.Join(work, ".deploy-it.json"), "{}\n")
			installDeployStub(t, binDir, filepath.Join(root, "native-deploy.out"), "native live acceptance\n", 2*time.Second, 0)
			// Delay the actual pull in a controlled Git wrapper so the launcher
			// is cancelled while SessionStart still has active work.
			gitPath, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			gitWrapper := filepath.Join(binDir, "git")
			pullStarted := filepath.Join(root, "pull-started-"+event)
			write(t, gitWrapper, "#!/bin/sh\nif [ \"$3\" = pull ]; then touch "+shellQuote(pullStarted)+"; sleep 2; fi\nexec "+shellQuote(gitPath)+" \"$@\"\n")
			if err := os.Chmod(gitWrapper, 0o755); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(gitWrapper)
			payload := fmt.Sprintf(`{"hook_event_name":%q,"cwd":%q}`, event, work)
			out, elapsed, pgid := invokeBinary(t, shipIt, baseEnv, "", payload)
			var response map[string]string
			if err := json.Unmarshal([]byte(out), &response); err != nil {
				t.Fatal(err)
			}
			message := response["systemMessage"]
			idx := strings.LastIndex(message, " in ")
			if idx < 0 || elapsed >= time.Second {
				t.Fatalf("native handoff: %s (%s)", out, elapsed)
			}
			logPath := message[idx+len(" in "):]
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(pullStarted); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker never started pull")
				}
				time.Sleep(20 * time.Millisecond)
			}
			killProcessGroup(t, pgid)
			log := waitForHookState(t, logPath, "completed", 10*time.Second)
			if !strings.Contains(log, `"pid":`) {
				t.Fatalf("worker handle missing: %s", log)
			}
			_, err = exec.Command("git", "-C", remote, "show", "main:change.txt").Output()
			if event == "Stop" && err != nil {
				t.Fatal("native Stop did not push")
			}
			if event == "SessionStart" && err == nil {
				t.Fatal("SessionStart shipped unfinished work")
			}
			assertPrivateLog(t, logPath)
		})
	}

	t.Run("success survives old deadline and deploys", func(t *testing.T) {
		remote, work := newRepository(t, root, "success")
		write(t, filepath.Join(work, ".deploy-it.json"), "{}\n")
		write(t, filepath.Join(work, "change.txt"), "success\n")
		deployOutput := "fake deploy completed: success\n"
		installDeployStub(t, binDir, filepath.Join(root, "success-deploy.out"), deployOutput, 6*time.Second, 0)
		logPath, elapsed, pgid := invokeLegacy(t, shipIt, baseEnv, "hook", work)
		if elapsed >= 4*time.Second {
			t.Fatalf("legacy launcher took %s", elapsed)
		}
		waitForHookState(t, logPath, "queued", 3*time.Second)
		time.Sleep(5 * time.Second)
		killProcessGroup(t, pgid)
		log := waitForHookState(t, logPath, "completed", 15*time.Second)
		if !strings.Contains(log, deployOutput) || !strings.Contains(log, `"ship_it":"completed"`) {
			t.Fatalf("log missing completion or deploy output:\n%s", log)
		}
		if got := strings.TrimSpace(gitOutput(t, remote, "show", "main:change.txt")); got != "success" {
			t.Fatalf("push did not survive launcher termination: %q", got)
		}
		assertPrivateLog(t, logPath)
	})

	t.Run("failed deployment is recorded after queued return", func(t *testing.T) {
		remote, work := newRepository(t, root, "failure")
		write(t, filepath.Join(work, ".deploy-it.json"), "{}\n")
		write(t, filepath.Join(work, "change.txt"), "failure\n")
		deployOutput := "fake deploy failed: exit 23\n"
		installDeployStub(t, binDir, filepath.Join(root, "failure-deploy.out"), deployOutput, 6*time.Second, 23)
		logPath, elapsed, pgid := invokeLegacy(t, shipIt, baseEnv, "cursor-hook", work)
		if elapsed >= 4*time.Second {
			t.Fatalf("legacy launcher took %s", elapsed)
		}
		waitForHookState(t, logPath, "queued", 3*time.Second)
		time.Sleep(5 * time.Second)
		killProcessGroup(t, pgid)
		log := waitForHookState(t, logPath, "failed", 15*time.Second)
		if !strings.Contains(log, deployOutput) || !strings.Contains(log, `"ship_it":"failed"`) || !strings.Contains(log, "exit status 23") {
			t.Fatalf("log missing failure or deploy output:\n%s", log)
		}
		if got := strings.TrimSpace(gitOutput(t, remote, "show", "main:change.txt")); got != "failure" {
			t.Fatalf("push did not complete before deployment failure: %q", got)
		}
		assertPrivateLog(t, logPath)
	})

	t.Run("no contract skips deployment", func(t *testing.T) {
		remote, work := newRepository(t, root, "no-contract")
		write(t, filepath.Join(work, "change.txt"), "no deployment\n")
		marker := filepath.Join(root, "no-contract-deploy.out")
		installDeployStub(t, binDir, marker, "must not run\n", 0, 0)
		logPath, _, pgid := invokeLegacy(t, shipIt, baseEnv, "hook", work)
		waitForHookState(t, logPath, "completed", 5*time.Second)
		killProcessGroup(t, pgid)
		log, _ := os.ReadFile(logPath)
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("deploy-it ran without contract")
		}
		if !strings.Contains(string(log), `"ship_it":"completed"`) {
			t.Fatalf("log missing completion:\n%s", log)
		}
		if got := strings.TrimSpace(gitOutput(t, remote, "show", "main:change.txt")); got != "no deployment" {
			t.Fatalf("push missing: %q", got)
		}
	})

	for _, name := range []string{"irrelevant", "repeated stop"} {
		t.Run(name, func(t *testing.T) {
			before := hookLogs(t, home)
			payload := `{"hook_event_name":"SessionStart","composer_mode":"ask"}`
			if name == "repeated stop" {
				payload = `{"hook_event_name":"Stop","status":"completed","stop_hook_active":true}`
			}
			out, elapsed, pgid := invokeBinary(t, shipIt, baseEnv, "hook", payload)
			killProcessGroup(t, pgid)
			if elapsed >= 4*time.Second || strings.TrimSpace(out) != "{}" {
				t.Fatalf("output=%q elapsed=%s", out, elapsed)
			}
			if after := hookLogs(t, home); len(after) != len(before) {
				t.Fatalf("irrelevant hook started worker: before=%v after=%v", before, after)
			}
		})
	}
	t.Run("non-repository workspace root", func(t *testing.T) {
		testLegacyHookIgnoresNonRepositoryWorkspaceRoot(t, shipIt)
	})
}

func testLegacyHookIgnoresNonRepositoryWorkspaceRoot(t *testing.T, binary string) {
	root := testDir(t)
	remote, work := newRepository(t, root, "multi-root")
	write(t, filepath.Join(work, "change.txt"), "continued\n")
	home := filepath.Join(root, "home")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"HOME="+home,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	payload := fmt.Sprintf(`{"hook_event_name":"Stop","status":"completed","workspace_roots":[%q,%q]}`, filepath.Join(root, "outside"), work)
	if err := os.Mkdir(filepath.Join(root, "outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _, pgid := invokeBinary(t, binary, env, "hook", payload)
	var response map[string]string
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("invalid launcher JSON %q: %v", out, err)
	}
	message := response["systemMessage"]
	idx := strings.LastIndex(message, " in ")
	if idx < 0 {
		t.Fatalf("launcher response has no log path: %q", out)
	}
	logPath := strings.TrimSpace(message[idx+len(" in "):])
	waitForHookState(t, logPath, "queued", 3*time.Second)
	waitForHookState(t, logPath, "completed", 8*time.Second)
	killProcessGroup(t, pgid)
	logBytes, _ := os.ReadFile(logPath)
	log := string(logBytes)
	if strings.Contains(log, "not inside a Git repository") || strings.Contains(log, `"ship_it":"failed"`) {
		t.Fatalf("ordinary workspace produced a delivery failure:\n%s", log)
	}
	if got := strings.TrimSpace(gitOutput(t, remote, "show", "main:change.txt")); got != "continued" {
		t.Fatalf("valid root was not delivered after invalid root: %q", got)
	}
}

func invokeLegacy(t *testing.T, binary string, env []string, arg, cwd string) (string, time.Duration, int) {
	t.Helper()
	payload := fmt.Sprintf(`{"hook_event_name":"Stop","status":"completed","cwd":%q}`, cwd)
	out, elapsed, pgid := invokeBinary(t, binary, env, arg, payload)
	var response map[string]string
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("invalid launcher JSON %q: %v", out, err)
	}
	path := response["systemMessage"]
	marker := " in "
	idx := strings.LastIndex(path, marker)
	if idx < 0 {
		t.Fatalf("launcher response has no log path: %q", out)
	}
	return strings.TrimSpace(path[idx+len(marker):]), elapsed, pgid
}

func invokeBinary(t *testing.T, binary string, env []string, arg, payload string) (string, time.Duration, int) {
	t.Helper()
	root := testDir(t)
	outPath := filepath.Join(root, "launcher.out")
	donePath := filepath.Join(root, "launcher.done")
	wrapper := filepath.Join(root, "launcher.sh")
	argument := ""
	if arg != "" {
		argument = " " + shellQuote(arg)
	}
	write(t, wrapper, "#!/bin/sh\n"+shellQuote(binary)+argument+" >"+shellQuote(outPath)+" 2>&1\nprintf done >"+shellQuote(donePath)+"\nsleep 30\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wrapper)
	cmd.Env, cmd.Stdin = env, strings.NewReader(payload)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: %v", arg, err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		killProcessGroup(t, pid)
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, err := os.Stat(donePath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("launcher did not return promptly")
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), time.Since(start), pid
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func killProcessGroup(t *testing.T, pgid int) {
	t.Helper()
	if runtime.GOOS != "windows" {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

func installDeployStub(t *testing.T, dir, marker, output string, delay time.Duration, exitCode int) {
	t.Helper()
	path := filepath.Join(dir, "deploy-it")
	script := "#!/bin/sh\nprintf '%s' " + shellQuote(output) + "\nprintf '%s' " + shellQuote(output) + " >> " + shellQuote(marker) + "\nsleep " + strconv.Itoa(int(delay/time.Second)) + "\nexit " + strconv.Itoa(exitCode) + "\n"
	write(t, path, script)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func waitForHookState(t *testing.T, path, state string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), `"ship_it":"`+state+`"`) {
			return string(data)
		}
		time.Sleep(100 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("timed out waiting for %s in %s:\n%s", state, path, data)
	return ""
}

func assertPrivateLog(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %o, want 600", info.Mode().Perm())
	}
}

func hookLogs(t *testing.T, home string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(home, ".local", "share", "ship-it", "hooks"))
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".event-") {
			paths = append(paths, entry.Name())
		}
	}
	return paths
}
