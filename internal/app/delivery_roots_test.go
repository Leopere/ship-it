package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStopDeliversTouchedExternalRepositoryOnce(t *testing.T) {
	stateDir := testDir(t)
	t.Setenv("ONE_SHOT_STATE_DIR", stateDir)
	t.Setenv("HOME", testDir(t))
	externalRemote, external := newRepository(t, testDir(t), "external repo")
	write(t, filepath.Join(external, "external.txt"), "changed\n")
	alias := filepath.Join(testDir(t), "external alias")
	if err := os.Symlink(external, alias); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	writeTouchedRegistry(t, "session", map[string]uint64{canonical: 1})
	installTallyStub(t)

	payload := `{"hook_event_name":"Stop","status":"completed","session_id":"session","turn_id":"turn","cwd":` + jsonQuote(alias) + `}`
	var output, diagnostics bytes.Buffer
	if err := RunInput(nil, "test", bytes.NewBufferString(payload), &output, &diagnostics); err != nil {
		t.Fatalf("Stop: %v\n%s", err, diagnostics.String())
	}
	if got := gitOutput(t, externalRemote, "show", "main:external.txt"); got != "changed\n" {
		t.Fatalf("external repository was not delivered: %q", got)
	}
	registryPath, err := deliveryRootRegistryPath("session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("successful root remained pending: %v", err)
	}
	before := gitOutput(t, externalRemote, "rev-list", "--count", "main")
	continuation := `{"hook_event_name":"Stop","status":"completed","stop_hook_active":true,"session_id":"session","turn_id":"turn","cwd":` + jsonQuote(alias) + `}`
	output.Reset()
	diagnostics.Reset()
	if err := RunInput(nil, "test", bytes.NewBufferString(continuation), &output, &diagnostics); err != nil {
		t.Fatalf("clean continuation: %v\n%s", err, diagnostics.String())
	}
	if after := gitOutput(t, externalRemote, "rev-list", "--count", "main"); after != before {
		t.Fatalf("clean delivered root ran again: before %s after %s", before, after)
	}
}

func TestTouchedDeliveryKeepsFailedAndNewerGenerations(t *testing.T) {
	stateDir := testDir(t)
	t.Setenv("ONE_SHOT_STATE_DIR", stateDir)
	t.Setenv("HOME", testDir(t))
	failed := testDir(t)
	canonicalFailed, err := filepath.EvalSymlinks(failed)
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, failed, "init", "-q")
	configure(t, failed)
	write(t, filepath.Join(failed, "pending.txt"), "pending\n")
	writeTouchedRegistry(t, "session", map[string]uint64{canonicalFailed: 1})
	installTallyStub(t)
	payload := `{"hook_event_name":"Stop","status":"completed","session_id":"session","turn_id":"turn","cwd":` + jsonQuote(testDir(t)) + `}`
	var output, diagnostics bytes.Buffer
	_ = RunInput(nil, "test", bytes.NewBufferString(payload), &output, &diagnostics)
	registry, err := touchedRootRegistry("session")
	if err != nil || registry.Roots[canonicalFailed] != 1 {
		t.Fatalf("failed delivery was cleared: registry=%#v err=%v", registry, err)
	}

	writeTouchedRegistry(t, "session", map[string]uint64{canonicalFailed: 2})
	if err := clearDeliveredTouchedRoots("session", map[string]uint64{canonicalFailed: 1}); err != nil {
		t.Fatal(err)
	}
	registry, err = touchedRootRegistry("session")
	if err != nil || registry.Roots[canonicalFailed] != 2 {
		t.Fatalf("newer edit generation was cleared: registry=%#v err=%v", registry, err)
	}
}

func TestHookRootsRejectTrashComponentsBeforeRepositoryLookup(t *testing.T) {
	if roots := hookRoots(hookEvent{CWD: "/private/var/.Trashes/project"}); len(roots) != 0 {
		t.Fatalf("Trash cwd selected roots: %#v", roots)
	}
	if roots := hookRoots(hookEvent{WorkspaceRoots: []string{"/tmp/.Trash/project"}}); len(roots) != 0 {
		t.Fatalf("Trash workspace selected roots: %#v", roots)
	}
}

func TestTouchedRootRejectsForbiddenAliasAndMissingNestedRoot(t *testing.T) {
	repo := testDir(t)
	runGit(t, repo, "init", "-q")
	alias := filepath.Join(testDir(t), "safe-alias")
	// The target is absent. Reject its hidden component from the link text
	// without traversing that target or invoking Git for it.
	if err := os.Symlink(filepath.Join(testDir(t), ".Trash", "missing"), alias); err != nil {
		t.Fatal(err)
	}
	if _, ok := canonicalTouchedRoot(alias); ok {
		t.Fatal("forbidden alias was accepted")
	}
	if _, ok := canonicalTouchedRoot(filepath.Join(repo, "removed", "nested")); ok {
		t.Fatal("missing nested path resolved to its Git parent")
	}
}

func TestSessionStartDoesNotSelectTouchedRoots(t *testing.T) {
	stateDir := testDir(t)
	t.Setenv("ONE_SHOT_STATE_DIR", stateDir)
	t.Setenv("HOME", testDir(t))
	remote, external := newRepository(t, testDir(t), "external")
	_, readOnly := newRepository(t, testDir(t), "read-only")
	upstream := filepath.Join(testDir(t), "upstream")
	runGit(t, filepath.Dir(upstream), "clone", "-q", remote, upstream)
	configure(t, upstream)
	write(t, filepath.Join(upstream, "latest.txt"), "latest\n")
	runGit(t, upstream, "add", ".")
	runGit(t, upstream, "commit", "-qm", "latest")
	runGit(t, upstream, "push", "-q")
	writeTouchedRegistry(t, "session", map[string]uint64{external: 1})

	payload := `{"hook_event_name":"SessionStart","session_id":"session","cwd":` + jsonQuote(readOnly) + `}`
	var output, diagnostics bytes.Buffer
	if err := RunInput(nil, "test", bytes.NewBufferString(payload), &output, &diagnostics); err != nil {
		t.Fatalf("SessionStart: %v\n%s", err, diagnostics.String())
	}
	if _, err := os.Stat(filepath.Join(external, "latest.txt")); !os.IsNotExist(err) {
		t.Fatalf("SessionStart used a touched root: %v", err)
	}
}

func TestTouchedFailureAllowsOnlyOneContinuationPerGeneration(t *testing.T) {
	stateDir := testDir(t)
	t.Setenv("ONE_SHOT_STATE_DIR", stateDir)
	t.Setenv("HOME", testDir(t))
	_, repo := newRepository(t, testDir(t), "retry")
	write(t, filepath.Join(repo, "pending.txt"), "pending\n")
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	writeTouchedRegistry(t, "session", map[string]uint64{canonical: 1})
	event := hookEvent{Name: "Stop", SessionID: "session", TurnID: "turn", CWD: repo}
	if err := finalizeTouchedRoots("session", "turn", nil, map[string]deliveryRootAttempt{canonical: {Generation: 1, Snapshot: deliveryWorktreeSnapshot(canonical)}}); err != nil {
		t.Fatal(err)
	}
	if err := updateDeliveryRetry(event, []string{canonical}); err != nil {
		t.Fatal(err)
	}
	event.StopHookActive = true
	if _, run := lifecycleMode(event); !run {
		t.Fatal("first failed-delivery continuation was skipped")
	}
	if err := updateDeliveryRetry(event, nil); err != nil {
		t.Fatal(err)
	}
	if _, run := lifecycleMode(event); run {
		t.Fatal("unchanged failed generation started another continuation")
	}
	nextTurn := event
	nextTurn.TurnID = "next"
	if _, run := lifecycleMode(nextTurn); !run {
		t.Fatal("new turn did not retry pending touched root")
	}
	writeTouchedRegistry(t, "session", map[string]uint64{canonical: 2})
	if _, run := lifecycleMode(event); !run {
		t.Fatal("newer touched generation in the same turn was skipped")
	}
}

func TestCleanFailedDeploymentRemainsPendingForNextTurn(t *testing.T) {
	stateDir := testDir(t)
	t.Setenv("ONE_SHOT_STATE_DIR", stateDir)
	t.Setenv("HOME", testDir(t))
	_, repo := newRepository(t, testDir(t), "deployment")
	write(t, filepath.Join(repo, ".deploy-it.json"), "{}\n")
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	writeTouchedRegistry(t, "session", map[string]uint64{canonical: 1})
	installTallyStub(t)
	bin := testDir(t)
	installDeployStub(t, bin, filepath.Join(bin, "deployment.out"), "deployment failed\n", 0, 23)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	payload := `{"hook_event_name":"Stop","status":"completed","session_id":"session","turn_id":"turn","cwd":` + jsonQuote(repo) + `}`
	var output, diagnostics bytes.Buffer
	if err := RunInput(nil, "test", bytes.NewBufferString(payload), &output, &diagnostics); err != nil {
		t.Fatalf("failed deployment Stop: %v\n%s", err, diagnostics.String())
	}
	if output, err := gitCommand(repo, "status", "--porcelain"); err != nil || output != "" {
		t.Fatalf("failed deployment did not leave a clean checkout: output=%q err=%v", output, err)
	}
	registry, err := touchedRootRegistry("session")
	if err != nil || registry.Attempts[canonical].TurnID != "turn" || registry.Attempts[canonical].Generation != 1 || registry.Attempts[canonical].Snapshot == "" {
		t.Fatalf("clean failed deployment was not retained: registry=%#v err=%v", registry, err)
	}
	active := hookEvent{Name: "Stop", SessionID: "session", TurnID: "turn", CWD: repo, StopHookActive: true}
	if _, run := lifecycleMode(active); !run {
		t.Fatal("first continuation was not armed")
	}
	if err := updateDeliveryRetry(active, nil); err != nil {
		t.Fatal(err)
	}
	if _, run := lifecycleMode(active); run {
		t.Fatal("clean failed deployment armed a third Stop")
	}
	active.TurnID = "next"
	if _, run := lifecycleMode(active); !run {
		t.Fatal("clean failed deployment was not eligible in the next turn")
	}
	if err := finalizeTouchedRoots("session", "next", map[string]uint64{canonical: 1}, nil); err != nil {
		t.Fatal(err)
	}
	registryPath, err := deliveryRootRegistryPath("session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("successful recovery did not clear registry: %v", err)
	}
}

func writeTouchedRegistry(t *testing.T, sessionID string, roots map[string]uint64) {
	t.Helper()
	path, err := deliveryRootRegistryPath(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(deliveryRootRegistry{Version: 1, SessionID: sessionID, Roots: roots})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func installTallyStub(t *testing.T) {
	t.Helper()
	bin := testDir(t)
	write(t, filepath.Join(bin, "one-shot-tally"), "#!/bin/sh\nprintf '%s' '{\"systemMessage\":\"recorded\"}'\n")
	if err := os.Chmod(filepath.Join(bin, "one-shot-tally"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := tallyExecutablePath
	t.Cleanup(func() { tallyExecutablePath = old })
	tallyExecutablePath = func() (string, error) { return filepath.Join(bin, "ship-it"), nil }
}

func jsonQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestFailedTouchedRootRetriesChangedContentsWithoutGeneration(t *testing.T) {
	for _, change := range []string{"tracked", "untracked", "untracked-content", "delete", "executable", "symlink"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("ONE_SHOT_STATE_DIR", testDir(t))
			t.Setenv("HOME", testDir(t))
			_, repo := newRepository(t, testDir(t), "retry")
			repo, _ = filepath.EvalSymlinks(repo)
			file := filepath.Join(repo, "tracked.txt")
			write(t, file, "before\n")
			runGit(t, repo, "add", ".")
			runGit(t, repo, "commit", "-qm", "before")
			if change == "tracked" {
				write(t, file, "dirty before\n")
			}
			if change == "untracked-content" {
				write(t, filepath.Join(repo, "new.txt"), "before\n")
			}
			status := gitOutput(t, repo, "status", "--porcelain")
			writeTouchedRegistry(t, "session", map[string]uint64{repo: 1})
			snapshot := deliveryWorktreeSnapshot(repo)
			if snapshot == "unavailable" || snapshot == "" {
				t.Fatal("snapshot unavailable")
			}
			attempt := map[string]deliveryRootAttempt{repo: {Generation: 1, Snapshot: snapshot}}
			if err := finalizeTouchedRoots("session", "turn", nil, attempt); err != nil {
				t.Fatal(err)
			}
			if roots := touchedDeliveryRootsForTurn("session", "turn"); len(roots) != 0 {
				t.Fatalf("unchanged attempt eligible: %v", roots)
			}
			switch change {
			case "tracked":
				write(t, file, "after\n")
			case "untracked", "untracked-content":
				write(t, filepath.Join(repo, "new.txt"), "new\n")
			case "delete":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "executable":
				if err := os.Chmod(file, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("missing-target", file); err != nil {
					t.Fatal(err)
				}
			}
			if change == "tracked" || change == "untracked-content" {
				if after := gitOutput(t, repo, "status", "--porcelain"); after != status {
					t.Fatal("fixture must retain identical Git status while changing bytes")
				}
			}
			event := hookEvent{Name: "Stop", SessionID: "session", TurnID: "turn", CWD: repo, StopHookActive: true}
			if roots := touchedDeliveryRootsForTurn("session", "turn"); roots[repo] != 1 {
				t.Fatalf("changed content skipped: %v", roots)
			}
			if !explicitRootNeedsDelivery(event, repo) {
				t.Fatal("explicit root suppressed changed content")
			}
			if _, run := lifecycleMode(event); !run {
				t.Fatal("changed content did not resume lifecycle")
			}
			attempt[repo] = deliveryRootAttempt{Generation: 1, Snapshot: deliveryWorktreeSnapshot(repo)}
			if err := finalizeTouchedRoots("session", "turn", nil, attempt); err != nil {
				t.Fatal(err)
			}
			if _, run := lifecycleMode(event); run {
				t.Fatal("same failed content caused another retry")
			}
		})
	}
}

func TestLegacyFailedTouchedRootGetsOneSnapshotRetry(t *testing.T) {
	t.Setenv("ONE_SHOT_STATE_DIR", testDir(t))
	t.Setenv("HOME", testDir(t))
	_, repo := newRepository(t, testDir(t), "legacy")
	repo, _ = filepath.EvalSymlinks(repo)
	writeTouchedRegistry(t, "session", map[string]uint64{repo: 1})
	if err := finalizeTouchedRoots("session", "turn", nil, map[string]deliveryRootAttempt{repo: {Generation: 1}}); err != nil {
		t.Fatal(err)
	}
	if roots := touchedDeliveryRootsForTurn("session", "turn"); roots[repo] != 1 {
		t.Fatal("old attempt did not get a retry")
	}
	if err := finalizeTouchedRoots("session", "turn", nil, map[string]deliveryRootAttempt{repo: {Generation: 1, Snapshot: deliveryWorktreeSnapshot(repo)}}); err != nil {
		t.Fatal(err)
	}
	if roots := touchedDeliveryRootsForTurn("session", "turn"); len(roots) != 0 {
		t.Fatal("old attempt kept retrying after snapshot recorded")
	}
}

func TestFailedSnapshotPreservesConcurrentEditAndGeneration(t *testing.T) {
	t.Setenv("ONE_SHOT_STATE_DIR", testDir(t))
	t.Setenv("HOME", testDir(t))
	_, repo := newRepository(t, testDir(t), "concurrent")
	repo, _ = filepath.EvalSymlinks(repo)
	write(t, filepath.Join(repo, ".deploy-it.json"), "{}\n")
	writeTouchedRegistry(t, "session", map[string]uint64{repo: 1})
	before := deliveryWorktreeSnapshot(repo)
	installTallyStub(t)
	bin := testDir(t)
	deploy := filepath.Join(bin, "deploy-it")
	write(t, deploy, "#!/bin/sh\nprintf 'concurrent edit\n' > concurrent.txt\nexit 23\n")
	if err := os.Chmod(deploy, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	payload := `{"hook_event_name":"Stop","status":"completed","session_id":"session","turn_id":"turn","cwd":` + jsonQuote(repo) + `}`
	var output, diagnostics bytes.Buffer
	if err := RunInput(nil, "test", bytes.NewBufferString(payload), &output, &diagnostics); err != nil {
		t.Fatalf("Stop: %v\n%s", err, diagnostics.String())
	}
	registry, err := touchedRootRegistry("session")
	if err != nil || registry.Attempts[repo].Snapshot != before {
		t.Fatalf("snapshot captured after delivery: %#v, %v", registry, err)
	}
	if roots := touchedDeliveryRootsForTurn("session", "turn"); roots[repo] != 1 {
		t.Fatal("concurrent content was suppressed")
	}
	// A later observed generation remains eligible even if its bytes match.
	registry.Roots[repo] = 2
	path, _ := deliveryRootRegistryPath("session")
	data, _ := json.Marshal(registry)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := finalizeTouchedRoots("session", "turn", nil, map[string]deliveryRootAttempt{repo: {Generation: 1, Snapshot: deliveryWorktreeSnapshot(repo)}}); err != nil {
		t.Fatal(err)
	}
	if roots := touchedDeliveryRootsForTurn("session", "turn"); roots[repo] != 2 {
		t.Fatal("older failure suppressed a newer generation")
	}
	if err := clearDeliveredTouchedRoots("session", map[string]uint64{repo: 1}); err != nil {
		t.Fatal(err)
	}
	if roots := touchedDeliveryRootsForTurn("session", "turn"); roots[repo] != 2 {
		t.Fatal("older success cleared a newer generation")
	}
}

func TestDeliverySnapshotIgnoresIndexAndCommitChurn(t *testing.T) {
	_, repo := newRepository(t, testDir(t), "snapshot")
	repo, _ = filepath.EvalSymlinks(repo)
	write(t, filepath.Join(repo, "pending.txt"), "pending\n")
	before := deliveryWorktreeSnapshot(repo)
	runGit(t, repo, "add", ".")
	if after := deliveryWorktreeSnapshot(repo); after != before {
		t.Fatal("staging changed content snapshot")
	}
	runGit(t, repo, "commit", "-qm", "publish")
	if after := deliveryWorktreeSnapshot(repo); after != before {
		t.Fatal("commit changed content snapshot")
	}
	write(t, filepath.Join(repo, ".git", "diagnostic"), "irrelevant\n")
	if after := deliveryWorktreeSnapshot(repo); after != before {
		t.Fatal("Git metadata changed content snapshot")
	}
}

func TestDeliverySnapshotFailureIsBoundedAndDoesNotFollowSymlinks(t *testing.T) {
	empty := testDir(t)
	runGit(t, empty, "init", "-q")
	if snapshot := deliveryWorktreeSnapshot(empty); snapshot == "unavailable" || snapshot == "" {
		t.Fatal("valid empty worktree mistaken for snapshot failure")
	}
	_, repo := newRepository(t, testDir(t), "safe")
	repo, _ = filepath.EvalSymlinks(repo)
	link := filepath.Join(repo, "link")
	if err := os.Symlink(filepath.Join(testDir(t), ".Trash", "missing"), link); err != nil {
		t.Fatal(err)
	}
	if snapshot := deliveryWorktreeSnapshot(repo); snapshot == "unavailable" {
		t.Fatal("snapshot followed symlink target")
	}
	missing := filepath.Join(repo, "missing")
	attempt := deliveryRootAttempt{TurnID: "turn", Generation: 1, Snapshot: deliveryWorktreeSnapshot(missing)}
	if attempt.Snapshot != "unavailable" || failedRootNeedsRetry(missing, "turn", 1, attempt) {
		t.Fatal("snapshot failure permitted repeated retry")
	}
}

func TestExplicitFailureWithoutTouchedGenerationRetriesOnlyChangedContents(t *testing.T) {
	t.Setenv("ONE_SHOT_STATE_DIR", testDir(t))
	t.Setenv("HOME", testDir(t))
	remote, repo := newRepository(t, testDir(t), "explicit")
	repo, _ = filepath.EvalSymlinks(repo)
	file := filepath.Join(repo, "pending.txt")
	write(t, file, "before\n")
	hook := filepath.Join(remote, "hooks", "pre-receive")
	trace := filepath.Join(testDir(t), "attempts")
	t.Setenv("DELIVERY_TRACE", trace)
	write(t, hook, "#!/bin/sh\nprintf 'attempt\n' >> \"$DELIVERY_TRACE\"\nexit 23\n")
	if err := os.Chmod(hook, 0755); err != nil {
		t.Fatal(err)
	}
	installTallyStub(t)
	event := hookEvent{Name: "Stop", SessionID: "session", TurnID: "turn", CWD: repo}
	run := func() {
		t.Helper()
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var output, diagnostics bytes.Buffer
		if err := RunInput(nil, "test", bytes.NewReader(payload), &output, &diagnostics); err != nil {
			t.Fatalf("Stop: %v\n%s", err, diagnostics.String())
		}
	}
	run()
	event.StopHookActive = true
	run()
	attempts, err := os.ReadFile(trace)
	if err != nil || string(attempts) != "attempt\nattempt\n" {
		t.Fatalf("expected initial and continuation attempt: %q, %v", attempts, err)
	}
	registry, err := touchedRootRegistry("session")
	if err != nil || len(registry.Roots) != 0 || registry.Attempts[repo].Generation != 0 || registry.Attempts[repo].Snapshot != deliveryWorktreeSnapshot(repo) {
		t.Fatalf("explicit failed snapshot lost or manufactured touched root: %#v, %v", registry, err)
	}
	if _, eligible := lifecycleMode(event); eligible {
		t.Fatal("unchanged explicit failure eligible after continuation")
	}
	run()
	if after, err := os.ReadFile(trace); err != nil || !bytes.Equal(after, attempts) {
		t.Fatal("unchanged explicit failure invoked publication again")
	}
	write(t, file, "after\n")
	if _, eligible := lifecycleMode(event); !eligible {
		t.Fatal("changed explicit content suppressed without generation")
	}
	run()
	attempts, err = os.ReadFile(trace)
	if err != nil || string(attempts) != "attempt\nattempt\nattempt\n" {
		t.Fatalf("changed explicit content not attempted once: %q, %v", attempts, err)
	}
	if _, eligible := lifecycleMode(event); eligible {
		t.Fatal("new failed explicit snapshot kept retrying")
	}
	// Make another real edit and remove the local failing hook to recover.
	write(t, file, "recovered\n")
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	run()
	if got := gitOutput(t, remote, "show", "main:pending.txt"); got != "recovered\n" {
		t.Fatalf("recovery did not publish expected content: %q", got)
	}
	path, _ := deliveryRootRegistryPath("session")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("successful explicit delivery retained failed attempt: %v", err)
	}
}

func TestExplicitSuccessAndLateFailurePreserveNewerTouchedGeneration(t *testing.T) {
	t.Setenv("ONE_SHOT_STATE_DIR", testDir(t))
	_, repo := newRepository(t, testDir(t), "race")
	repo, _ = filepath.EvalSymlinks(repo)
	writeTouchedRegistry(t, "session", map[string]uint64{repo: 1})
	newer := deliveryRootAttempt{Generation: 1, Snapshot: deliveryWorktreeSnapshot(repo)}
	if err := finalizeTouchedRoots("session", "turn", nil, map[string]deliveryRootAttempt{repo: newer}); err != nil {
		t.Fatal(err)
	}
	if err := finalizeTouchedRoots("session", "turn", map[string]uint64{repo: 0}, nil); err != nil {
		t.Fatal(err)
	}
	if err := finalizeTouchedRoots("session", "turn", nil, map[string]deliveryRootAttempt{repo: {Snapshot: "older"}}); err != nil {
		t.Fatal(err)
	}
	registry, err := touchedRootRegistry("session")
	if err != nil || registry.Roots[repo] != 1 || registry.Attempts[repo].Generation != 1 || registry.Attempts[repo].Snapshot != newer.Snapshot {
		t.Fatalf("explicit generation-zero outcome erased newer work: %#v, %v", registry, err)
	}
}
