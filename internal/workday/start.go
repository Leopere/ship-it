// Package workday remembers whether a repository has pulled today.
package workday

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Mode int

const (
	Auto Mode = iota
	Start
	Stop
)

func Run(root string, now time.Time, mode Mode, pull, ship func() error) error {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(root))
	dir := filepath.Join(home, ".local", "share", "ship-it", "workdays", hex.EncodeToString(key[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock repository: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	marker := filepath.Join(dir, now.Format("2006-01-02"))
	first := false
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		first = true
	} else if err != nil {
		return err
	}
	if first {
		if err := pull(); err != nil {
			return err
		}
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			return err
		}
	}
	if mode == Start {
		return nil
	}
	return ship()
}
