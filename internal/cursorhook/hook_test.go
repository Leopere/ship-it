package cursorhook

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSessionStartAddsWrapContext(t *testing.T) {
	var out bytes.Buffer
	if err := Handle(strings.NewReader(`{"hook_event_name":"sessionStart"}`), &out); err != nil {
		t.Fatal(err)
	}
	got := decode(t, out.Bytes())
	if got["additional_context"] != sessionContext {
		t.Fatalf("additional_context = %#v", got)
	}
}

func TestHandleSessionStartSkipsAskMode(t *testing.T) {
	var out bytes.Buffer
	if err := Handle(strings.NewReader(`{"hook_event_name":"sessionStart","composer_mode":"ask"}`), &out); err != nil {
		t.Fatal(err)
	}
	got := decode(t, out.Bytes())
	if _, ok := got["additional_context"]; ok {
		t.Fatalf("ask mode leaked context: %#v", got)
	}
}

func TestHandleStopFollowsUpOnDirtyCompletedWorktree(t *testing.T) {
	root := initRepo(t, true)
	payload, err := json.Marshal(event{
		HookEventName:  "stop",
		Status:         "completed",
		WorkspaceRoots: []string{root},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Handle(bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	got := decode(t, out.Bytes())
	if got["followup_message"] != stopFollowup {
		t.Fatalf("followup_message = %#v", got)
	}
}

func TestHandleStopStaysQuietWhenCleanOrAborted(t *testing.T) {
	root := initRepo(t, false)
	cases := []event{
		{HookEventName: "stop", Status: "completed", WorkspaceRoots: []string{root}},
		{HookEventName: "stop", Status: "aborted", WorkspaceRoots: []string{initRepo(t, true)}},
		{HookEventName: "stop", Status: "completed", LoopCount: 2, WorkspaceRoots: []string{initRepo(t, true)}},
	}
	for _, ev := range cases {
		payload, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Handle(bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		got := decode(t, out.Bytes())
		if _, ok := got["followup_message"]; ok {
			t.Fatalf("unexpected followup for %#v: %#v", ev, got)
		}
	}
}

func TestInstallMergesWithoutDroppingExistingHooks(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".cursor", "hooks.json")
	existing := []byte(`{
  "version": 1,
  "hooks": {
    "sessionStart": [{"command": "$HOME/.local/bin/cursor-one-shot-tally"}],
    "stop": [{"command": "$HOME/.local/bin/cursor-one-shot-tally", "loop_limit": 10}]
  }
}
`)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, existing, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Install(home, &out); err != nil {
		t.Fatal(err)
	}
	if err := Install(home, &out); err != nil {
		t.Fatal(err)
	}
	var file hooksFile
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Hooks["sessionStart"]) != 2 || len(file.Hooks["stop"]) != 2 {
		t.Fatalf("hooks = %#v", file.Hooks)
	}
	if file.Hooks["stop"][0]["loop_limit"] != float64(10) {
		t.Fatalf("tally loop_limit = %#v", file.Hooks["stop"][0]["loop_limit"])
	}
	if file.Hooks["stop"][1]["command"] != hookCommand {
		t.Fatalf("ship-it command = %#v", file.Hooks["stop"][1]["command"])
	}
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return got
}

func initRepo(t *testing.T, dirty bool) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "Ship It Test")
	runGit(t, root, "config", "user.email", "ship-it@example.test")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README")
	runGit(t, root, "commit", "-m", "initial")
	if dirty {
		if err := os.WriteFile(filepath.Join(root, "dirty"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
