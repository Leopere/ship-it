package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHooksSkipLocalOnlyRepositoriesWithoutDelivery(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, event := range []string{"SessionStart", "Stop"} {
			t.Run(fmt.Sprintf("%s/committed=%t", event, committed), func(t *testing.T) {
				t.Setenv("HOME", testDir(t))
				t.Setenv("ONE_SHOT_STATE_DIR", testDir(t))
				t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
				t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
				repo := testDir(t)
				runGit(t, repo, "init", "-b", "main")
				configure(t, repo)
				write(t, filepath.Join(repo, "staged.txt"), "staged work\n")
				runGit(t, repo, "add", ".")
				if committed {
					runGit(t, repo, "commit", "-m", "existing local work")
					write(t, filepath.Join(repo, "staged.txt"), "new staged work\n")
					runGit(t, repo, "add", ".")
				}
				write(t, filepath.Join(repo, "untracked.txt"), "unstaged work\n")
				statusBefore := gitOutput(t, repo, "status", "--porcelain")
				headBefore, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
				if err != nil {
					t.Fatal(err)
				}
				var output, diagnostics bytes.Buffer
				payload := fmt.Sprintf(`{"hook_event_name":%q,"cwd":%q,"session_id":"local-only","turn_id":"turn"}`, event, repo)
				if err := RunInput(nil, "test", strings.NewReader(payload), &output, &diagnostics); err != nil {
					t.Fatalf("local-only hook failed: %v\n%s", err, diagnostics.String())
				}
				var response deliveryTallyResult
				if err := json.Unmarshal(output.Bytes(), &response); err != nil {
					t.Fatalf("invalid hook JSON: %v: %s", err, output.String())
				}
				if !strings.Contains(response.SystemMessage, "Delivery skipped") || !strings.Contains(response.SystemMessage, "no Git remote") || response.Decision != "" {
					t.Fatalf("skip was not explained gracefully: %#v", response)
				}
				if diagnostics.Len() != 0 {
					t.Fatalf("successful skip emitted stderr: %s", diagnostics.String())
				}
				if got := gitOutput(t, repo, "status", "--porcelain"); got != statusBefore {
					t.Fatalf("skip changed index/worktree: before=%q after=%q", statusBefore, got)
				}
				headAfter, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
				if err != nil || !bytes.Equal(headBefore, headAfter) {
					t.Fatalf("skip changed HEAD: %q, %v", headAfter, err)
				}
				if !committed {
					if _, err := os.Stat(filepath.Join(repo, ".git", "refs", "heads", "main")); !os.IsNotExist(err) {
						t.Fatalf("skip created a commit: %v", err)
					}
				} else if got := strings.TrimSpace(gitOutput(t, repo, "rev-list", "--count", "HEAD")); got != "1" {
					t.Fatalf("skip created a commit: count=%s", got)
				}
				if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".local", "share", "ship-it", "workdays")); !os.IsNotExist(err) {
					t.Fatalf("skip consumed daily pull or acquired delivery lock: %v", err)
				}
				if hasDeliveryRetry(hookEvent{SessionID: "local-only", TurnID: "turn"}) {
					t.Fatal("skip queued a failed delivery retry")
				}
				logs, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), ".local", "share", "ship-it", "hooks", "*.log"))
				if len(logs) != 1 {
					t.Fatalf("logs = %v", logs)
				}
				log, _ := os.ReadFile(logs[0])
				if !bytes.Contains(log, []byte(`"ship_it":"skipped"`)) || bytes.Contains(log, []byte("delivery tally")) || bytes.Contains(log, []byte("pulling upstream")) || bytes.Contains(log, []byte("committing and pushing")) {
					t.Fatalf("skip log contained delivery work or omitted skip: %s", log)
				}
			})
		}
	}
}

func TestConfiguredBrokenRemoteStillFailsHooks(t *testing.T) {
	for _, event := range []string{"SessionStart", "Stop"} {
		t.Run(event, func(t *testing.T) {
			t.Setenv("HOME", testDir(t))
			repo := testDir(t)
			runGit(t, repo, "init", "-b", "main")
			runGit(t, repo, "remote", "add", "origin", filepath.Join(repo, "missing.git"))
			var output, diagnostics bytes.Buffer
			payload := fmt.Sprintf(`{"hook_event_name":%q,"cwd":%q}`, event, repo)
			err := RunInput(nil, "test", strings.NewReader(payload), &output, &diagnostics)
			if err == nil || !strings.Contains(err.Error(), "ls-remote") {
				t.Fatalf("configured remote failure suppressed: %v", err)
			}
			if strings.Contains(output.String(), "Delivery skipped") || !strings.Contains(diagnostics.String(), `"ship_it":"failed"`) {
				t.Fatalf("broken remote treated as skip: output=%s stderr=%s", output.String(), diagnostics.String())
			}
		})
	}
}

func TestCorruptGitConfigurationStillFailsHook(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	repo := testDir(t)
	runGit(t, repo, "init", "-b", "main")
	write(t, filepath.Join(repo, ".git", "config"), "[broken\n")
	var output, diagnostics bytes.Buffer
	payload := fmt.Sprintf(`{"hook_event_name":"SessionStart","cwd":%q}`, repo)
	if err := RunInput(nil, "test", strings.NewReader(payload), &output, &diagnostics); err == nil {
		t.Fatal("corrupt configuration treated as no remote")
	}
	if strings.Contains(output.String(), "Delivery skipped") {
		t.Fatalf("corrupt configuration skipped: %s", output.String())
	}
}

func TestSkippedRootDoesNotHideOtherRepositoryFailure(t *testing.T) {
	for _, event := range []string{"SessionStart", "Stop"} {
		t.Run(event, func(t *testing.T) {
			t.Setenv("HOME", testDir(t))
			local, broken := testDir(t), testDir(t)
			for _, repo := range []string{local, broken} {
				runGit(t, repo, "init", "-b", "main")
			}
			runGit(t, broken, "remote", "add", "origin", filepath.Join(broken, "missing.git"))
			var output, diagnostics bytes.Buffer
			payload := fmt.Sprintf(`{"hook_event_name":%q,"workspace_roots":[%q,%q]}`, event, local, broken)
			if err := RunInput(nil, "test", strings.NewReader(payload), &output, &diagnostics); err == nil || !strings.Contains(err.Error(), "ls-remote") {
				t.Fatalf("skip report hid other remote failure: %v", err)
			}
			if !strings.Contains(output.String(), "Delivery skipped") || !strings.Contains(diagnostics.String(), `"ship_it":"failed"`) {
				t.Fatalf("missing skip/failure evidence: output=%s stderr=%s", output.String(), diagnostics.String())
			}
		})
	}
}
