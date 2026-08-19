package cursorhook

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
)

const sessionContext = "Before Git edits, run `ship-it start`. When wrapping this prompt after a verified, production-safe checkpoint, run `ship-it` with no arguments. Do not use git commit/push, a pull request, or ship.sh instead, and do not ask permission."

const stopFollowup = "Uncommitted repository changes remain. Run `ship-it` now with no arguments. Do not use git commit/push or a pull request. If work is still unverified or production-unsafe, finish that first, then ship."

type event struct {
	HookEventName  string   `json:"hook_event_name"`
	Status         string   `json:"status"`
	LoopCount      int      `json:"loop_count"`
	ComposerMode   string   `json:"composer_mode"`
	WorkspaceRoots []string `json:"workspace_roots"`
}

func Handle(in io.Reader, out io.Writer) error {
	raw, err := io.ReadAll(in)
	if err != nil {
		return writeJSON(out, map[string]any{})
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return writeJSON(out, map[string]any{})
	}
	var ev event
	if err := json.Unmarshal(raw, &ev); err != nil {
		return writeJSON(out, map[string]any{})
	}
	name := ev.HookEventName
	if name == "" {
		if ev.Status != "" {
			name = "stop"
		} else {
			name = "sessionStart"
		}
	}
	switch name {
	case "sessionStart":
		if strings.EqualFold(ev.ComposerMode, "ask") {
			return writeJSON(out, map[string]any{})
		}
		return writeJSON(out, map[string]any{"additional_context": sessionContext})
	case "stop":
		if ev.Status == "aborted" || ev.Status == "error" || ev.LoopCount >= 2 {
			return writeJSON(out, map[string]any{})
		}
		if strings.EqualFold(ev.ComposerMode, "ask") || !dirty(ev.WorkspaceRoots) {
			return writeJSON(out, map[string]any{})
		}
		return writeJSON(out, map[string]any{"followup_message": stopFollowup})
	default:
		return writeJSON(out, map[string]any{})
	}
}

func dirty(roots []string) bool {
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		cmd := exec.Command("git", "-C", root, "status", "--porcelain")
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if len(bytes.TrimSpace(out)) > 0 {
			return true
		}
	}
	return false
}

func writeJSON(out io.Writer, payload map[string]any) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(payload)
}
