package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTallyDelivery(t *testing.T) {
	root := testDir(t)
	runGit(t, root, "init", "-q")
	configure(t, root)
	write(t, filepath.Join(root, "tracked.txt"), "tracked\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "initial")
	commit := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	bin := testDir(t)
	marker := filepath.Join(bin, "stdin.json")
	write(t, filepath.Join(bin, "one-shot-tally"), "#!/bin/sh\ncat > "+shellQuote(marker)+"\nprintf '%s' '{\"systemMessage\":\"recorded\"}'\n")
	if err := os.Chmod(filepath.Join(bin, "one-shot-tally"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := tallyExecutablePath
	t.Cleanup(func() { tallyExecutablePath = old })
	tallyExecutablePath = func() (string, error) { return filepath.Join(bin, "ship-it"), nil }

	output, err := tallyDelivery(hookEvent{Name: "Stop", SessionID: "session", TurnID: "turn", CWD: "/untrusted/event/cwd"}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if result["systemMessage"] != "recorded" {
		t.Fatalf("result = %#v", result)
	}
	var payload map[string]any
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["hook_event_name"] != "DeliveryResult" || payload["session_id"] != "session" || payload["turn_id"] != "turn" || payload["cwd"] != root || payload["delivery_kind"] != "ship" || payload["delivery_status"] != "succeeded" || payload["delivery_commit"] != commit {
		t.Fatalf("payload metadata = %#v", payload)
	}
	if response, ok := payload["tool_response"].(map[string]any); !ok || response["exit_code"] != float64(0) {
		t.Fatalf("tool response = %#v", payload["tool_response"])
	}
	if _, ok := payload["error"]; ok {
		t.Fatalf("payload contains error: %#v", payload)
	}
}

func TestTallyDeliveryValidationAndScope(t *testing.T) {
	root := testDir(t)
	bin := testDir(t)
	old := tallyExecutablePath
	t.Cleanup(func() { tallyExecutablePath = old })
	tallyExecutablePath = func() (string, error) { return filepath.Join(bin, "ship-it"), nil }
	event := hookEvent{Name: "Stop", SessionID: "s", TurnID: "t"}
	for _, test := range []struct {
		name, output string
		wantErr      bool
	}{
		{"missing message", `{"decision":"block","reason":"why"}`, true},
		{"bad decision", `{"systemMessage":"ok","decision":"allow"}`, true},
		{"block missing reason", `{"systemMessage":"ok","decision":"block"}`, true},
		{"multiple values", `{"systemMessage":"ok"}{"systemMessage":"extra"}`, true},
		{"valid block", `{"systemMessage":"blocked","decision":"block","reason":"why"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			write(t, filepath.Join(bin, "one-shot-tally"), "#!/bin/sh\nprintf '%s' "+shellQuote(test.output)+"\n")
			if err := os.Chmod(filepath.Join(bin, "one-shot-tally"), 0o755); err != nil {
				t.Fatal(err)
			}
			_, err := tallyDelivery(event, root, nil)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error %v", err, test.wantErr)
			}
		})
	}
	if output, err := tallyDelivery(hookEvent{Name: "Stop", SessionID: "", TurnID: "t"}, root, nil); err != nil || output != nil {
		t.Fatalf("missing session = (%q, %v)", output, err)
	}
}

func TestTallyDeliveryTimeout(t *testing.T) {
	bin := testDir(t)
	write(t, filepath.Join(bin, "one-shot-tally"), "#!/bin/sh\nexec sleep 3\n")
	if err := os.Chmod(filepath.Join(bin, "one-shot-tally"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := tallyExecutablePath
	t.Cleanup(func() { tallyExecutablePath = old })
	tallyExecutablePath = func() (string, error) { return filepath.Join(bin, "ship-it"), nil }
	started := time.Now()
	_, err := tallyDelivery(hookEvent{Name: "Stop", SessionID: "s", TurnID: "t"}, testDir(t), errors.New("delivery failed"))
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("timeout error = %v, elapsed = %s", err, time.Since(started))
	}
}
