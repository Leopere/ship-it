package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
		if deliveredGeneration, delivered := delivered[recordedRoot]; delivered && recordedGeneration <= deliveredGeneration {
			delete(registry.Roots, recordedRoot)
			delete(registry.Attempts, recordedRoot)
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
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error {
				unlockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
				closeErr := lock.Close()
				if unlockErr != nil {
					return unlockErr
				}
				return closeErr
			}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = lock.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = lock.Close()
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
	root, ok := permittedExistingAncestor(root, 0)
	if !ok {
		return "", false
	}
	resolvedRoot := root
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	root = strings.TrimSpace(string(output))
	if root == "" || hasTrashComponent(root) {
		return "", false
	}
	canonical, ok := permittedExistingAncestor(root, 0)
	if !ok || hasTrashComponent(canonical) || canonical != resolvedRoot {
		return "", false
	}
	return canonical, true
}

func permittedExistingAncestor(path string, links int) (string, bool) {
	if links > 40 || !filepath.IsAbs(path) || hasTrashComponent(path) {
		return "", false
	}
	path = filepath.Clean(path)
	volume, rest := filepath.VolumeName(path), strings.TrimPrefix(path, filepath.VolumeName(path))
	current := volume + string(filepath.Separator)
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == filepath.Separator })
	for index, part := range parts {
		candidate := filepath.Join(current, part)
		if hasTrashComponent(candidate) {
			return "", false
		}
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return "", false
		}
		if err != nil {
			return "", false
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(candidate)
			if err != nil {
				return "", false
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(candidate), target)
			}
			if hasTrashComponent(target) {
				return "", false
			}
			return permittedExistingAncestor(filepath.Join(target, filepath.Join(parts[index+1:]...)), links+1)
		}
		current = candidate
	}
	return current, true
}

func hasTrashComponent(path string) bool {
	for _, component := range strings.FieldsFunc(filepath.Clean(path), func(r rune) bool { return r == filepath.Separator }) {
		if strings.EqualFold(component, ".trash") || strings.EqualFold(component, ".trashes") {
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
