package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// deliveryRootRegistry mirrors one-shot-tally's private session registry. The
// generation is a compare-and-clear token: a new native edit observed while a
// Stop is delivering cannot be erased by that older Stop.
type deliveryRootRegistry struct {
	Version   int                            `json:"version"`
	SessionID string                         `json:"session_id"`
	Roots     map[string]uint64              `json:"roots"`
	Attempts  map[string]deliveryRootAttempt `json:"attempts,omitempty"`
}

type deliveryRootAttempt struct {
	TurnID     string `json:"turn_id"`
	Generation uint64 `json:"generation"`
}

func touchedDeliveryRoots(sessionID string) map[string]uint64 {
	registry, err := touchedRootRegistry(sessionID)
	if err != nil {
		return nil
	}
	return canonicalRegistryRoots(registry)
}

func touchedDeliveryRootsForTurn(sessionID, turnID string) map[string]uint64 {
	registry, err := touchedRootRegistry(sessionID)
	if err != nil {
		return nil
	}
	roots := canonicalRegistryRoots(registry)
	for root, generation := range roots {
		attempt := registry.Attempts[root]
		if attempt.TurnID == turnID && generation <= attempt.Generation {
			delete(roots, root)
		}
	}
	return roots
}

func touchedRootRegistry(sessionID string) (deliveryRootRegistry, error) {
	path, err := deliveryRootRegistryPath(sessionID)
	if err != nil {
		return deliveryRootRegistry{}, err
	}
	return loadDeliveryRootRegistry(path, sessionID)
}

func canonicalRegistryRoots(registry deliveryRootRegistry) map[string]uint64 {
	roots := make(map[string]uint64, len(registry.Roots))
	for root, generation := range registry.Roots {
		if canonical, ok := canonicalTouchedRoot(root); ok && generation > roots[canonical] {
			roots[canonical] = generation
		}
	}
	return roots
}

func hasPendingTouchedDeliveryRoots(sessionID, turnID string) bool {
	return len(touchedDeliveryRootsForTurn(sessionID, turnID)) > 0
}

func deliveryRootRegistryPath(sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("touched root registry requires a session ID")
	}
	dir := os.Getenv("ONE_SHOT_STATE_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".codex", "state", "one-shot-delivery")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(dir, "touched-roots-"+hex.EncodeToString(sum[:16])+".json"), nil
}

func loadDeliveryRootRegistry(path, sessionID string) (deliveryRootRegistry, error) {
	registry := deliveryRootRegistry{Version: 1, SessionID: sessionID, Roots: map[string]uint64{}, Attempts: map[string]deliveryRootAttempt{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return deliveryRootRegistry{}, err
	}
	if err := json.Unmarshal(data, &registry); err != nil || registry.Version != 1 || registry.SessionID != sessionID {
		return deliveryRootRegistry{}, errors.New("invalid touched root registry")
	}
	if registry.Roots == nil {
		registry.Roots = map[string]uint64{}
	}
	if registry.Attempts == nil {
		registry.Attempts = map[string]deliveryRootAttempt{}
	}
	return registry, nil
}

func clearDeliveredTouchedRoots(sessionID string, delivered map[string]uint64) error {
	return finalizeTouchedRoots(sessionID, "", delivered, nil)
}

func finalizeTouchedRoots(sessionID, turnID string, delivered, failed map[string]uint64) error {
	if len(delivered) == 0 && len(failed) == 0 {
		return nil
	}
	path, err := deliveryRootRegistryPath(sessionID)
	if err != nil {
		return err
	}
	unlock, err := acquireDeliveryRootLock(path)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	registry, err := loadDeliveryRootRegistry(path, sessionID)
	if err != nil {
		return err
	}
	for recordedRoot, recordedGeneration := range registry.Roots {
		canonical, ok := canonicalTouchedRoot(recordedRoot)
		if !ok {
			continue
		}
		if deliveredGeneration, delivered := delivered[canonical]; delivered && recordedGeneration <= deliveredGeneration {
			delete(registry.Roots, recordedRoot)
			delete(registry.Attempts, canonical)
		}
	}
	for root, generation := range failed {
		if generation == 0 {
			continue
		}
		registry.Attempts[root] = deliveryRootAttempt{TurnID: turnID, Generation: generation}
	}
	if len(registry.Roots) == 0 {
		if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
			return nil
		} else {
			return err
		}
	}
	data, err := json.Marshal(registry)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func acquireDeliveryRootLock(path string) (func() error, error) {
	lockPath := path + ".lock"
	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			token := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
			if _, writeErr := lock.WriteString(token); writeErr != nil {
				_ = lock.Close()
				_ = os.Remove(lockPath)
				return nil, writeErr
			}
			if closeErr := lock.Close(); closeErr != nil {
				_ = os.Remove(lockPath)
				return nil, closeErr
			}
			return func() error {
				data, readErr := os.ReadFile(lockPath)
				if errors.Is(readErr, os.ErrNotExist) {
					return nil
				}
				if readErr != nil || string(data) != token {
					return readErr
				}
				return os.Remove(lockPath)
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, statErr := os.Stat(lockPath)
		if statErr == nil && time.Since(info.ModTime()) > time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for touched root registry")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func canonicalTouchedRoot(root string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" || hasTrashComponent(root) {
		return "", false
	}
	output, err := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	root = strings.TrimSpace(string(output))
	if root == "" || hasTrashComponent(root) {
		return "", false
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || hasTrashComponent(canonical) {
		return "", false
	}
	return canonical, true
}

func hasTrashComponent(path string) bool {
	for _, component := range strings.FieldsFunc(filepath.Clean(path), func(r rune) bool { return r == filepath.Separator }) {
		component = strings.TrimPrefix(component, ".")
		if strings.EqualFold(component, "trash") || strings.EqualFold(component, "trashes") {
			return true
		}
	}
	return false
}

func deliveredExactCleanRoot(root string, repoCommit string) bool {
	if strings.TrimSpace(repoCommit) == "" || gitHead(root) != strings.TrimSpace(repoCommit) {
		return false
	}
	output, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=all").Output()
	return err == nil && strings.TrimSpace(string(output)) == ""
}
