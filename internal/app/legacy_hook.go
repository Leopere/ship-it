package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Leopere/ship-it/internal/workday"
)

const backgroundWorkerEnv = "SHIP_IT_BACKGROUND_WORKER"

// Old sessions retain a five-second Stop handler. Keep its original quick
// handoff behavior while the installed no-argument command owns delivery.
func runLegacyHook(in io.Reader, out, errOut io.Writer) error {
	event, ok := decodeHookEvent(in)
	mode, run := lifecycleMode(event)
	if !ok || !run {
		fmt.Fprintln(out, "{}")
		return nil
	}
	if mode != workday.Stop {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		return runCycle(bytes.NewReader(data), out, errOut, false)
	}

	// Resolve a missing cwd before detaching. Retain only the fields used by
	// delivery, not the user's prompt or the full transcript in the hook input.
	if event.CWD == "" {
		event.CWD = mustCwd()
	}
	log, err := createHookLog()
	if err != nil {
		return err
	}
	defer log.Close()
	input, err := os.CreateTemp(filepath.Dir(log.Name()), ".event-*.json")
	if err != nil {
		return err
	}
	defer input.Close()
	defer os.Remove(input.Name())
	if err := json.NewEncoder(input).Encode(event); err != nil {
		return err
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, backgroundWorkerEnv+"=") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, backgroundWorkerEnv+"=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, log, log
	// Neither the old hook's process group nor its output pipes own this job.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	writeHookRecord(log, "queued", event, nil)
	if err := cmd.Start(); err != nil {
		writeHookRecord(log, "completed", event, err)
		return fmt.Errorf("start delivery worker (log: %s): %w", log.Name(), err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release delivery worker (log: %s): %w", log.Name(), err)
	}
	return json.NewEncoder(out).Encode(map[string]string{
		"systemMessage": "Delivery queued. Completion or failure will be recorded in " + log.Name(),
	})
}

func createHookLog() (*os.File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".local", "share", "ship-it", "hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, time.Now().UTC().Format("20060102T150405.000000000Z-")+"*.log")
}

func writeHookRecord(out io.Writer, state string, event hookEvent, err error) {
	record := map[string]any{
		"ship_it": state,
		"time":    time.Now().UTC().Format(time.RFC3339Nano),
		"event":   event.Name,
		"roots":   hookRoots(event),
	}
	if err != nil {
		record["ship_it"] = "failed"
		record["error"] = err.Error()
	}
	_ = json.NewEncoder(out).Encode(record)
}
