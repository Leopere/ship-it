package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImplicitHooksIgnoreGeneratedSourceWithoutRepository(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	t.Setenv("ONE_SHOT_STATE_DIR", testDir(t))
	snapshot := filepath.Join(testDir(t), "state", "releases", strings.Repeat("a", 64), "source")
	if err := os.MkdirAll(snapshot, 0700); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"SessionStart", "Stop"} {
		t.Run(event, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			payload := fmt.Sprintf(`{"hook_event_name":%q,"cwd":%q,"session_id":"snapshot","turn_id":"turn"}`, event, snapshot)
			if err := RunInput(nil, "test", strings.NewReader(payload), &output, &diagnostics); err != nil {
				t.Fatalf("ordinary source snapshot became delivery failure: %v", err)
			}
			if output.String() != "{}\n" || diagnostics.Len() != 0 {
				t.Fatalf("non-repository hook produced failure evidence: output=%q diagnostics=%q", output.String(), diagnostics.String())
			}
			if hasDeliveryRetry(hookEvent{SessionID: "snapshot", TurnID: "turn"}) {
				t.Fatal("non-repository hook queued another delivery attempt")
			}
		})
	}
	logs, err := filepath.Glob(filepath.Join(os.Getenv("HOME"), ".local", "share", "ship-it", "hooks", "*.log"))
	if err != nil || len(logs) != 0 {
		t.Fatalf("no-op hook created delivery logs: %v, %v", logs, err)
	}
}

func TestExplicitInvocationStillRejectsNonRepository(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(testDir(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	var output, diagnostics bytes.Buffer
	if err := Run(nil, "test", &output, &diagnostics); err == nil || !strings.Contains(err.Error(), "not inside a Git repository") {
		t.Fatalf("explicit invocation suppressed its error: %v", err)
	}
}

func TestNonRepositoryHookPreservesWorkspaceAndTouchedDelivery(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	t.Setenv("ONE_SHOT_STATE_DIR", testDir(t))
	workspaceRemote, workspace := newRepository(t, testDir(t), "workspace")
	touchedRemote, touched := newRepository(t, testDir(t), "touched")
	write(t, filepath.Join(workspace, "workspace.txt"), "workspace work\n")
	write(t, filepath.Join(touched, "touched.txt"), "touched work\n")
	nested := filepath.Join(workspace, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	canonicalTouched, err := filepath.EvalSymlinks(touched)
	if err != nil {
		t.Fatal(err)
	}
	writeTouchedRegistry(t, "mixed", map[string]uint64{canonicalTouched: 1})
	installTallyStub(t)
	outside := testDir(t)
	payload := fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"mixed","turn_id":"turn","cwd":%q,"workspace_roots":[%q,%q]}`, outside, outside, nested)
	var output, diagnostics bytes.Buffer
	if err := RunInput(nil, "test", strings.NewReader(payload), &output, &diagnostics); err != nil {
		t.Fatalf("mixed Stop: %v\n%s", err, diagnostics.String())
	}
	if got := gitOutput(t, workspaceRemote, "show", "main:workspace.txt"); got != "workspace work\n" {
		t.Fatalf("valid nested workspace was skipped: %q", got)
	}
	if got := gitOutput(t, touchedRemote, "show", "main:touched.txt"); got != "touched work\n" {
		t.Fatalf("valid touched repository was skipped: %q", got)
	}
	registry, err := touchedRootRegistry("mixed")
	if err != nil || len(registry.Roots) != 0 {
		t.Fatalf("delivered touched work remained queued: %#v, %v", registry, err)
	}
}

func TestRepositoryMarkerKeepsMissingGitFailure(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	repo := testDir(t)
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", testDir(t))
	var output, diagnostics bytes.Buffer
	payload := fmt.Sprintf(`{"hook_event_name":"SessionStart","cwd":%q}`, repo)
	if err := RunInput(nil, "test", strings.NewReader(payload), &output, &diagnostics); err == nil || !strings.Contains(err.Error(), "not inside a Git repository") {
		t.Fatalf("missing Git failure was suppressed: %v", err)
	}
}
