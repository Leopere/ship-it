package install

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// installCodexHooks preserves unrelated handlers and reconciles its delivery
// and edit-observer handlers.
func installCodexHooks(home string, out io.Writer) error {
	configHome := os.Getenv("CODEX_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".codex")
	}
	path, err := codexHooksPath(configHome)
	if err != nil {
		return err
	}
	file := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &file); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	hooks, ok := file["hooks"].(map[string]any)
	if !ok && file["hooks"] != nil {
		return fmt.Errorf("invalid hooks object in %s", path)
	}
	if hooks == nil {
		hooks = map[string]any{}
		file["hooks"] = hooks
	}
	shipCommand := shellQuote(filepath.Join(home, ".local", "bin", "ship-it"))
	tallyCommand := shellQuote(filepath.Join(home, ".local", "bin", "one-shot-tally"))
	for _, spec := range []codexHookSpec{
		{event: "SessionStart", matcher: "startup|resume", command: shipCommand, timeout: 300, owned: isShipItHook},
		// Stop may use the 1,800-second deploy-it contract and needs five
		// minutes for delivery orchestration and the final hook result.
		{event: "Stop", command: shipCommand, timeout: 2100, owned: isShipItHook},
		// one-shot-tally records successful explicit edits in the shared touched
		// root registry. ship-it reads that registry at Stop to deliver edited
		// repositories outside the session's initial working directory.
		{event: "PreToolUse", matcher: "*", command: tallyCommand, timeout: 5, owned: exactCommand(tallyCommand)},
		{event: "PostToolUse", matcher: "*", command: tallyCommand, timeout: 5, owned: exactCommand(tallyCommand)},
	} {
		var kept []any
		if groups, ok := hooks[spec.event].([]any); ok {
			for _, item := range groups {
				group, ok := item.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid %s hook group", spec.event)
				}
				var handlers []any
				if existing, ok := group["hooks"].([]any); ok {
					for _, candidate := range existing {
						handler, ok := candidate.(map[string]any)
						if !ok {
							return fmt.Errorf("invalid %s hook handler", spec.event)
						}
						cmd, _ := handler["command"].(string)
						if spec.owned(cmd) {
							continue
						}
						handlers = append(handlers, handler)
					}
				}
				if len(handlers) > 0 {
					group["hooks"] = handlers
					kept = append(kept, group)
				}
			}
		} else if hooks[spec.event] != nil {
			return fmt.Errorf("invalid %s hook list", spec.event)
		}
		group := map[string]any{}
		if spec.matcher != "" {
			group["matcher"] = spec.matcher
		}
		group["hooks"] = []any{map[string]any{"type": "command", "command": spec.command, "timeout": spec.timeout}}
		hooks[spec.event] = append(kept, group)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Fprintln(out, "Installed native Codex lifecycle hooks in", path)
	return nil
}

type codexHookSpec struct {
	event   string
	matcher string
	command string
	timeout int
	owned   func(string) bool
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func isShipItHook(command string) bool { return strings.Contains(command, "ship-it") }

func exactCommand(want string) func(string) bool {
	return func(command string) bool { return strings.TrimSpace(command) == want }
}

// codexHooksPath writes through an account-specific hooks.json symlink instead
// of replacing it with an atomic rename. Codex account tooling owns that link.
func codexHooksPath(configHome string) (string, error) {
	path := filepath.Join(configHome, "hooks.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve Codex hooks link %s: %w", path, err)
	}
	return resolved, nil
}

func updateDeliveryGuidance(home string) error {
	configHome := os.Getenv("CODEX_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".codex")
	}
	path := filepath.Join(configHome, "AGENTS.md")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		data = nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	section := "# Repository delivery\n\nUse `$ship-it` for repository delivery behavior.\n\n- Native lifecycle hooks run the no-argument `ship-it` binary. The binary selects the required work from the hook event and repository state. SessionStart and Stop are hook events, not command-line subcommands.\n- Before the first delivery each local day, ship-it pulls the latest upstream code. A bare invocation stages all changes, creates an automatic commit when needed, and pushes in the same call. SessionStart only performs the daily pull. A tracked deployment command runs after a successful push.\n- Keep Git delivery mechanical: `git add .`, an automatic commit when needed, and push everything Git stages. Do not assess content, select files, or add test, review, or conflict-assessment gates to this flow. The working agent owns diagnosis and repairs.\n- Do not require a clean worktree before shipping or deployment. Native shipping stages all changes, commits when needed, and pushes everything Git stages without content selection or approval prompts. Deployment uses the exact pushed commit in a private snapshot; local worktree and index changes do not block it or enter the snapshot. Pending edits belong to the native shipping flow; they are not a deployment blocker.\n- Tests provide advisory evidence, not an automatic deployment veto. Diagnose intentional changes, defects, faulty tests, and environment failures, then repair the relevant cause and continue. Verify the actual deployment independently.\n- Agents finish their work and let the hooks run. Do not invoke ship-it, poll it, or add shipping prompts.\n- Routine Git commands belong to ship-it. Agents may use Git only to diagnose or repair a ship-it defect, with regression coverage.\n- For an authorized destination, finish the change and focused checks. Include deployment and live acceptance checks in the repository command. Fix recoverable failures within that scope. A failed attempt is not proof that the task is blocked.\n- Report a blocker only with the attempted command, observed failure, and the specific missing access, information, or external change. Do not invent approval or policy requirements.\n"
	updated := replaceSection(string(data), "# Repository delivery", section)
	if updated == string(data) {
		return nil
	}
	return writeAtomic(path, []byte(updated), 0o644)
}

func replaceSection(document, heading, replacement string) string {
	start := strings.Index(document, heading)
	if start < 0 {
		if document == "" {
			return replacement
		}
		return strings.TrimRight(document, "\n") + "\n\n" + replacement
	}
	searchFrom := start + len(heading)
	next := strings.Index(document[searchFrom:], "\n# ")
	if next < 0 {
		return strings.TrimRight(document[:start], "\n") + "\n\n" + replacement
	}
	end := searchFrom + next + 1
	return document[:start] + replacement + "\n" + document[end:]
}
