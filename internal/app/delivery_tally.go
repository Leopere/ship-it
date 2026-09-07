package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var tallyExecutablePath = os.Executable

type deliveryTallyResult struct {
	SystemMessage string `json:"systemMessage"`
	Decision      string `json:"decision,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

func tallyDelivery(event hookEvent, root string, deliveryErr error) ([]byte, error) {
	if event.Name != "Stop" || strings.TrimSpace(event.SessionID) == "" || strings.TrimSpace(event.TurnID) == "" {
		return nil, nil
	}
	commit := gitHead(root)
	kind := "ship"
	if hasDeployContract(root) {
		kind = "deploy"
	}
	return tallyDeliveryRevision(event, root, commit, kind, deliveryErr)
}

func tallyDeliveryRevision(event hookEvent, root, commit, kind string, deliveryErr error) ([]byte, error) {
	if event.Name != "Stop" || strings.TrimSpace(event.SessionID) == "" || strings.TrimSpace(event.TurnID) == "" {
		return nil, nil
	}
	if kind != "deploy" {
		kind = "ship"
	}
	attemptID := deliveryAttemptID()
	status := "succeeded"
	exitCode := 0
	if deliveryErr != nil {
		status, exitCode = "failed", 1
	}
	payload := map[string]any{
		"hook_event_name":     "DeliveryResult",
		"session_id":          event.SessionID,
		"turn_id":             event.TurnID,
		"cwd":                 root,
		"delivery_kind":       kind,
		"delivery_status":     status,
		"delivery_attempt_id": attemptID,
		"delivery_commit":     commit,
		"stop_hook_active":    event.StopHookActive,
		"tool_response":       map[string]int{"exit_code": exitCode},
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	executable, err := tallyExecutable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable)
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("one-shot-tally reporter timed out: %w", ctx.Err())
		}
		return nil, fmt.Errorf("one-shot-tally reporter: %w", err)
	}
	return validateTallyOutput(stdout.Bytes())
}

func tallyExecutable() (string, error) {
	if executable, err := tallyExecutablePath(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "one-shot-tally")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return exec.LookPath("one-shot-tally")
}

func gitHead(root string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func hasDeployContract(root string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", root, "ls-tree", "--name-only", "HEAD", "--", ".deploy-it.json").Output()
	return err == nil && strings.TrimSpace(string(output)) == ".deploy-it.json"
}

func deliveryAttemptID() string {
	var random [12]byte
	_, _ = rand.Read(random[:])
	return fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), hex.EncodeToString(random[:]))
}

func validateTallyOutput(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result deliveryTallyResult
	if err := decoder.Decode(&result); err != nil || strings.TrimSpace(result.SystemMessage) == "" {
		return nil, errors.New("one-shot-tally reporter returned invalid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("one-shot-tally reporter returned more than one JSON value")
	}
	if result.Decision != "" && result.Decision != "block" {
		return nil, errors.New("one-shot-tally reporter returned invalid decision")
	}
	if result.Decision == "block" && strings.TrimSpace(result.Reason) == "" {
		return nil, errors.New("one-shot-tally reporter returned block without reason")
	}
	return json.Marshal(result)
}
