package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// A failed native Stop permits one delivery retry during its continuation,
// including external repairs that leave Git clean. Further retries require new
// work or a new turn; they cannot create an automatic Stop loop.
func retryMarker(event hookEvent) string {
	if event.SessionID == "" || event.TurnID == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	key := sha256.Sum256([]byte(event.SessionID + "\x00" + event.TurnID + "\x00" + strings.Join(hookRoots(event), "\x00")))
	return filepath.Join(home, ".local", "share", "ship-it", "hook-retries", fmt.Sprintf("%x", key[:16]))
}

func hasDeliveryRetry(event hookEvent) bool {
	return len(deliveryRetryRoots(event)) > 0
}

func deliveryRetryRoots(event hookEvent) []string {
	path := retryMarker(event)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	var roots []string
	if err != nil || json.Unmarshal(data, &roots) != nil {
		return nil
	}
	return roots
}

func updateDeliveryRetry(event hookEvent, failedRoots []string) error {
	path := retryMarker(event)
	if path == "" {
		return nil
	}
	if event.StopHookActive || len(failedRoots) == 0 {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(failedRoots)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func continuationRoots(event hookEvent) []string {
	failed := make(map[string]bool)
	var roots []string
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" || hasTrashComponent(root) {
			return
		}
		canonical := canonicalRetryRoot(root)
		if failed[canonical] {
			return
		}
		failed[canonical] = true
		roots = append(roots, root)
	}
	for _, root := range deliveryRetryRoots(event) {
		add(root)
	}
	for root := range touchedDeliveryRootsForTurn(event.SessionID, event.TurnID) {
		add(root)
	}
	for _, root := range hookRoots(event) {
		if explicitRootNeedsDelivery(event, root) {
			add(root)
		}
	}
	return roots
}

func hasUnblockedExplicitWork(event hookEvent) bool {
	for _, root := range hookRoots(event) {
		if explicitRootNeedsDelivery(event, root) {
			return true
		}
	}
	return false
}

func explicitRootNeedsDelivery(event hookEvent, root string) bool {
	if !hasUnshippedWork(hookEvent{CWD: root}) {
		return false
	}
	registry, err := touchedRootRegistry(event.SessionID)
	if err != nil {
		return true
	}
	canonical := canonicalRetryRoot(root)
	generation := canonicalRegistryRoots(registry)[canonical]
	attempt := registry.Attempts[canonical]
	return attempt.TurnID != event.TurnID || generation > attempt.Generation
}

func canonicalRetryRoot(root string) string {
	if hasTrashComponent(root) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if output, err := gitLocal(ctx, root, "rev-parse", "--show-toplevel"); err == nil {
		root = strings.TrimSpace(output)
	}
	if hasTrashComponent(root) {
		return ""
	}
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		return canonical
	}
	return filepath.Clean(root)
}

// hasUnshippedWork checks only roots supplied by the hook. It never falls
// back to the process cwd because a repeated Stop payload may be incomplete.
func hasUnshippedWork(event hookEvent) bool {
	roots := make([]string, 0, 1+len(event.WorkspaceRoots))
	seen := make(map[string]struct{})
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" || hasTrashComponent(root) {
			return
		}
		if _, ok := seen[root]; ok {
			return
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	add(event.CWD)
	for _, root := range event.WorkspaceRoots {
		add(root)
	}
	if len(roots) == 0 {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, root := range roots {
		if ctx.Err() != nil {
			return false
		}
		if output, err := gitLocal(ctx, root, "status", "--porcelain", "--untracked-files=all"); err == nil && strings.TrimSpace(output) != "" {
			return true
		}
		head, headErr := gitLocal(ctx, root, "rev-parse", "--verify", "HEAD")
		pushed, pushedErr := gitLocal(ctx, root, "rev-parse", "--verify", "@{push}")
		if headErr == nil && pushedErr == nil && strings.TrimSpace(head) != strings.TrimSpace(pushed) {
			return true
		}
	}
	return false
}

func gitLocal(ctx context.Context, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := command.Output()
	return string(output), err
}
