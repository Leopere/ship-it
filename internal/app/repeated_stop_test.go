package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasUnshippedWork(t *testing.T) {
	root := testDir(t)
	remote := filepath.Join(testDir(t), "remote.git")
	runGit(t, filepath.Dir(remote), "init", "--bare", "-q", remote)
	runGit(t, root, "init", "-q")
	configure(t, root)
	write(t, filepath.Join(root, "tracked.txt"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	runGit(t, root, "remote", "add", "origin", remote)
	runGit(t, root, "push", "-u", "origin", "HEAD")
	if hasUnshippedWork(hookEvent{Name: "Stop", CWD: root}) {
		t.Fatal("clean pushed repository reported unshipped work")
	}
	write(t, filepath.Join(root, "dirty.txt"), "dirty\n")
	if !hasUnshippedWork(hookEvent{Name: "Stop", CWD: root}) {
		t.Fatal("dirty repository was not reported")
	}
	os.Remove(filepath.Join(root, "dirty.txt"))
	if hasUnshippedWork(hookEvent{Name: "Stop", CWD: filepath.Join(root, "missing")}) {
		t.Fatal("missing explicit root reported unshipped work")
	}
	if hasUnshippedWork(hookEvent{Name: "Stop"}) {
		t.Fatal("payload without explicit roots used process cwd")
	}
	runGit(t, root, "commit", "--allow-empty", "-qm", "ahead")
	if !hasUnshippedWork(hookEvent{Name: "Stop", WorkspaceRoots: []string{root}}) {
		t.Fatal("local commit ahead of push was not reported")
	}
}

func TestFailedDeliveryAllowsOneCleanContinuation(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	event := hookEvent{Name: "Stop", SessionID: "s", TurnID: "t", CWD: testDir(t)}
	if err := updateDeliveryRetry(event, []string{event.CWD}); err != nil {
		t.Fatal(err)
	}
	event.StopHookActive = true
	if _, run := lifecycleMode(event); !run {
		t.Fatal("failed native delivery skipped continuation")
	}
	if err := updateDeliveryRetry(event, []string{event.CWD}); err != nil {
		t.Fatal(err)
	}
	if _, run := lifecycleMode(event); run {
		t.Fatal("failed continuation armed another retry")
	}
	event.StopHookActive = false
	if err := updateDeliveryRetry(event, []string{event.CWD}); err != nil {
		t.Fatal(err)
	}
	event.TurnID = "other"
	event.StopHookActive = true
	if _, run := lifecycleMode(event); run {
		t.Fatal("retry leaked into another turn")
	}
}

func TestFailedDeliveryRetrySurvivesIncompleteContinuationRoots(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	failedRoot := testDir(t)
	event := hookEvent{Name: "Stop", SessionID: "session", TurnID: "turn", CWD: failedRoot}
	if err := updateDeliveryRetry(event, []string{failedRoot}); err != nil {
		t.Fatal(err)
	}
	continuation := hookEvent{Name: "Stop", SessionID: event.SessionID, TurnID: event.TurnID, StopHookActive: true}
	if _, run := lifecycleMode(continuation); !run {
		t.Fatal("missing continuation roots hid the saved failed delivery")
	}
	roots := deliveryRetryRoots(continuation)
	if len(roots) != 1 || roots[0] != failedRoot {
		t.Fatalf("continuation lost the exact failed root: %v", roots)
	}
	if err := updateDeliveryRetry(continuation, roots); err != nil {
		t.Fatal(err)
	}
	if hasDeliveryRetry(event) {
		t.Fatal("continuation did not consume its one retry")
	}
}
