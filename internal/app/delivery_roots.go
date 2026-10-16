package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	Snapshot   string `json:"snapshot,omitempty"`
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
		if !failedRootNeedsRetry(root, turnID, generation, attempt) {
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

func finalizeTouchedRoots(sessionID, turnID string, delivered map[string]uint64, failed map[string]deliveryRootAttempt) error {
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
	for root, generation := range delivered {
		if registry.Roots[root] <= generation && registry.Attempts[root].Generation <= generation {
			delete(registry.Roots, root)
			delete(registry.Attempts, root)
		}
	}
	for root, attempt := range failed {
		if registry.Attempts[root].Generation > attempt.Generation {
			continue
		}
		attempt.TurnID = turnID
		registry.Attempts[root] = attempt
	}
	if len(registry.Roots) == 0 && len(registry.Attempts) == 0 {
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

// Missing snapshots from older registries permit one retry. An unavailable
// snapshot is explicit so read failures cannot start an endless Stop loop.
func failedRootNeedsRetry(root, turnID string, generation uint64, attempt deliveryRootAttempt) bool {
	return attempt.TurnID != turnID || generation > attempt.Generation || attempt.Snapshot == "" || deliveryWorktreeSnapshot(root) != attempt.Snapshot
}

func deliveryWorktreeSnapshot(root string) string {
	const unavailable = "unavailable"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	files, err := gitLocal(ctx, root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return unavailable
	}
	names := strings.Split(files, "\x00")
	sort.Strings(names)
	hash := sha256.New()
	last := ""
	buffer := make([]byte, 32*1024)
	for _, name := range names {
		if name == "" || name == last {
			continue
		}
		last = name
		if ctx.Err() != nil || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || hasTrashComponent(name) {
			return unavailable
		}
		path := filepath.Join(root, name)
		parent, safe := permittedExistingAncestor(filepath.Dir(path), 0)
		if !safe || (parent != root && !strings.HasPrefix(parent, root+string(filepath.Separator))) {
			return unavailable
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return unavailable
		}
		content := sha256.New()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return unavailable
			}
			_, _ = io.WriteString(content, target)
		case info.Mode().IsRegular():
			fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return unavailable
			}
			file := os.NewFile(uintptr(fd), path)
			opened, err := file.Stat()
			if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
				_ = file.Close()
				return unavailable
			}
			for {
				if ctx.Err() != nil {
					_ = file.Close()
					return unavailable
				}
				n, readErr := file.Read(buffer)
				_, _ = content.Write(buffer[:n])
				if readErr != nil {
					_ = file.Close()
					if readErr != io.EOF {
						return unavailable
					}
					break
				}
			}
		default:
			return unavailable
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%x\x00", name, info.Mode()&(os.ModeType|0o111), content.Sum(nil))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
