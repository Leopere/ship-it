package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Leopere/ship-it/internal/gitx"
	installer "github.com/Leopere/ship-it/internal/install"
	"github.com/Leopere/ship-it/internal/skilldoc"
	updater "github.com/Leopere/ship-it/internal/update"
	"github.com/Leopere/ship-it/internal/workday"
)

const usage = `ship-it pulls once each day, then ships every completed coding cycle.

Usage:
  ship-it
  ship-it install [--host aedev-mac] [--skills-only]
  ship-it update
  ship-it version
  ship-it skill
`

func Run(args []string, version string, out, errOut io.Writer) error {
	return RunInput(args, version, nil, out, errOut)
}

func RunInput(args []string, version string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		background := os.Getenv(backgroundWorkerEnv) == "1"
		if background {
			_ = os.Unsetenv(backgroundWorkerEnv)
		}
		return runCycle(in, out, errOut, background)
	}
	switch args[0] {
	case "hook", "cursor-hook":
		if in == nil {
			return nil
		}
		return runLegacyHook(in, out, errOut)
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	case "version", "--version":
		fmt.Fprintln(out, version)
		return nil
	case "skill":
		fmt.Fprint(out, skilldoc.SkillMD)
		return nil
	case "update":
		return updater.Force(version, out)
	case "install":
		return runInstall(args[1:], out)
	default:
		return fmt.Errorf("repository delivery takes no arguments (received %q); use ship-it without arguments and let the lifecycle hooks select the required work", args[0])
	}
}

func runCycle(in io.Reader, out, errOut io.Writer, background bool) error {
	if in == nil {
		return runRepositories([]string{mustCwd()}, workday.Auto, out, errOut)
	}
	response := []byte("{}")
	defer func() { fmt.Fprintln(out, string(response)) }()
	event, ok := decodeHookEvent(in)
	if !ok {
		return nil
	}
	mode, run := lifecycleMode(event)
	if !run {
		return nil
	}
	roots := hookRoots(event)
	touchedRoots := map[string]uint64(nil)
	if mode == workday.Stop {
		touchedRoots = touchedDeliveryRoots(event.SessionID)
		roots = append(roots, mapRoots(touchedRoots)...)
	}
	if mode == workday.Stop && event.StopHookActive {
		roots = continuationRoots(event)
		if err := updateDeliveryRetry(event, nil); err != nil {
			return fmt.Errorf("consume delivery retry: %w", err)
		}
	}
	// Keep command output on disk as it arrives. An outer hook timeout can
	// terminate this process before an in-memory diagnostic buffer is copied.
	var diagnostics bytes.Buffer
	var destination io.Writer = errOut
	var logPath string
	if !background {
		log, err := createHookLog()
		if err != nil {
			fmt.Fprintf(errOut, "ship-it: could not save hook diagnostics: %v\n", err)
			destination = &diagnostics
		} else {
			defer log.Close()
			logPath = log.Name()
			destination = io.MultiWriter(&diagnostics, log)
		}
	}
	writeHookRecord(destination, "started", event, nil)
	var reports []deliveryTallyResult
	var failedRoots []string
	deliveredTouchedRoots := make(map[string]uint64)
	failedTouchedRoots := make(map[string]uint64)
	allFailuresReported := true
	var observe func(string, *gitx.Repo, error)
	if mode == workday.Stop && event.SessionID != "" && event.TurnID != "" {
		observe = func(root string, repo *gitx.Repo, deliveryErr error) {
			if deliveryErr != nil {
				failedRoots = append(failedRoots, root)
				if generation, touched := touchedRoots[root]; touched {
					failedTouchedRoots[root] = generation
				}
			} else if generation, touched := touchedRoots[root]; touched && repo != nil && deliveredExactCleanRoot(root, repo.DeliveryCommit) {
				deliveredTouchedRoots[root] = generation
			}
			commit, kind := "", "ship"
			if repo != nil {
				commit = repo.DeliveryCommit
				if repo.DeploymentRequired {
					kind = "deploy"
				}
			}
			raw, reportErr := tallyDeliveryRevision(event, root, commit, kind, deliveryErr)
			var report deliveryTallyResult
			if reportErr != nil || json.Unmarshal(raw, &report) != nil {
				fmt.Fprintf(destination, "ship-it: delivery tally unavailable: %v\n", reportErr)
				message := fmt.Sprintf("Delivery evidence unavailable for %s: one-shot-tally reporting failed. Do not rerun a successful delivery only to repair its report.", root)
				if logPath != "" {
					message += " Diagnostic log: " + logPath
				} else {
					fmt.Fprintf(errOut, "ship-it: delivery tally unavailable: %v\n", reportErr)
				}
				reports = append(reports, deliveryTallyResult{SystemMessage: message})
				if deliveryErr != nil {
					allFailuresReported = false
				}
				return
			}
			if deliveryErr != nil {
				report.SystemMessage += fmt.Sprintf("\nDelivery error for %s: %v. Diagnostic log: %s", root, deliveryErr, logPath)
			}
			reports = append(reports, report)
		}
	}
	err := runRepositoriesObserved(roots, mode, destination, destination, observe)
	if mode == workday.Stop {
		if finalizeErr := finalizeTouchedRoots(event.SessionID, event.TurnID, deliveredTouchedRoots, failedTouchedRoots); finalizeErr != nil {
			fmt.Fprintf(destination, "ship-it: could not finalize touched roots: %v\n", finalizeErr)
			err = errors.Join(err, finalizeErr)
		}
		if retryErr := updateDeliveryRetry(event, failedRoots); retryErr != nil {
			fmt.Fprintf(destination, "ship-it: could not record delivery retry: %v\n", retryErr)
			err = errors.Join(err, retryErr)
			allFailuresReported = false
		}
	}
	writeHookRecord(destination, "completed", event, err)
	if len(reports) > 0 {
		combined := deliveryTallyResult{}
		for _, report := range reports {
			if combined.SystemMessage != "" {
				combined.SystemMessage += "\n\n"
			}
			combined.SystemMessage += report.SystemMessage
			if report.Decision == "block" {
				combined.Decision, combined.Reason = report.Decision, report.Reason
			}
		}
		response, _ = json.Marshal(combined)
		if err != nil && allFailuresReported && !background {
			// The hook protocol succeeded in recording a FAILED delivery and
			// reporting recovery feedback. Exit zero so Codex reads that report.
			return nil
		}
	}
	if err != nil {
		if !background {
			_, _ = io.Copy(errOut, &diagnostics)
			if logPath != "" {
				return fmt.Errorf("%w (log: %s)", err, logPath)
			}
		}
	}
	return err
}

type hookEvent struct {
	SessionID      string   `json:"session_id,omitempty"`
	TurnID         string   `json:"turn_id,omitempty"`
	Name           string   `json:"hook_event_name"`
	Status         string   `json:"status"`
	ComposerMode   string   `json:"composer_mode"`
	StopHookActive bool     `json:"stop_hook_active"`
	CWD            string   `json:"cwd"`
	WorkspaceRoots []string `json:"workspace_roots"`
}

func decodeHookEvent(in io.Reader) (hookEvent, bool) {
	var event hookEvent
	decoder := json.NewDecoder(in)
	if err := decoder.Decode(&event); err != nil {
		return hookEvent{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return hookEvent{}, false
	}
	return event, true
}

func lifecycleMode(event hookEvent) (workday.Mode, bool) {
	if strings.EqualFold(event.ComposerMode, "ask") {
		return workday.Auto, false
	}
	if event.StopHookActive && !hasDeliveryRetry(event) && !hasPendingTouchedDeliveryRoots(event.SessionID, event.TurnID) && !hasUnblockedExplicitWork(event) {
		return workday.Stop, false
	}
	switch event.Name {
	case "SessionStart", "sessionStart":
		return workday.Start, true
	case "Stop":
		return workday.Stop, event.Status == "" || strings.EqualFold(event.Status, "completed")
	case "stop":
		return workday.Stop, strings.EqualFold(event.Status, "completed")
	default:
		return workday.Auto, false
	}
}

func mapRoots(roots map[string]uint64) []string {
	paths := make([]string, 0, len(roots))
	for root := range roots {
		paths = append(paths, root)
	}
	return paths
}

func hookRoots(event hookEvent) []string {
	roots := make([]string, 0, 1+len(event.WorkspaceRoots))
	hadWorkspaceRoot := len(event.WorkspaceRoots) > 0
	for _, root := range event.WorkspaceRoots {
		if root = strings.TrimSpace(root); root != "" && !hasTrashComponent(root) {
			roots = append(roots, root)
		}
	}
	if len(roots) > 0 {
		return roots
	}
	if hadWorkspaceRoot {
		return nil
	}
	if cwd := strings.TrimSpace(event.CWD); cwd != "" {
		if hasTrashComponent(cwd) {
			return nil
		}
		return []string{cwd}
	}
	cwd := mustCwd()
	if hasTrashComponent(cwd) {
		return nil
	}
	return []string{cwd}
}

func runRepositories(roots []string, mode workday.Mode, out, errOut io.Writer) error {
	return runRepositoriesObserved(roots, mode, out, errOut, nil)
}

func runRepositoriesObserved(roots []string, mode workday.Mode, out, errOut io.Writer, observe func(string, *gitx.Repo, error)) error {
	seen := make(map[string]struct{}, len(roots))
	var failures []error
	for _, root := range roots {
		if hasTrashComponent(root) {
			continue
		}
		safeRoot, ok := permittedExistingAncestor(root, 0)
		if !ok {
			continue
		}
		repo, err := gitx.Open(safeRoot, out, errOut)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", root, err))
			if observe != nil {
				observe(root, nil, err)
			}
			continue
		}
		canonicalRoot, ok := permittedExistingAncestor(repo.Root, 0)
		if !ok || hasTrashComponent(canonicalRoot) {
			continue
		}
		if _, alreadyRan := seen[canonicalRoot]; alreadyRan {
			continue
		}
		seen[canonicalRoot] = struct{}{}
		fmt.Fprintf(out, "ship-it: waiting for repository lock: %s\n", canonicalRoot)
		pull := func() error {
			fmt.Fprintln(out, "ship-it: pulling upstream")
			return repo.Pull()
		}
		ship := func() error {
			fmt.Fprintln(out, "ship-it: committing and pushing completed work")
			return repo.Ship()
		}
		err = workday.Run(repo.Root, time.Now(), mode, pull, ship)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", root, err))
		}
		if observe != nil {
			observe(canonicalRoot, repo, err)
		}
	}
	return errors.Join(failures...)
}

func runInstall(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(out)
	host := fs.String("host", "", "SSH host")
	skillsOnly := fs.Bool("skills-only", false, "only install skills")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := installer.Local(!*skillsOnly, out); err != nil {
		return err
	}
	if *host != "" {
		if *skillsOnly {
			return errors.New("--host cannot be combined with --skills-only")
		}
		return installer.Remote(*host, out)
	}
	return nil
}

func mustCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return cwd
}
