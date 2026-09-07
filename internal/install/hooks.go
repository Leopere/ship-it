package install

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// installCodexHooks preserves unrelated handlers and replaces only ship-it's handlers.
func installCodexHooks(home string, out io.Writer) error {
	configHome := os.Getenv("CODEX_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".codex")
	}
	path := filepath.Join(configHome, "hooks.json")
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
	command := "'" + strings.ReplaceAll(filepath.Join(home, ".local", "bin", "ship-it"), "'", "'\"'\"'") + "'"
	for _, event := range []string{"SessionStart", "Stop"} {
		var kept []any
		if groups, ok := hooks[event].([]any); ok {
			for _, item := range groups {
				group, ok := item.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid %s hook group", event)
				}
				var handlers []any
				if existing, ok := group["hooks"].([]any); ok {
					for _, candidate := range existing {
						handler, ok := candidate.(map[string]any)
						if !ok {
							return fmt.Errorf("invalid %s hook handler", event)
						}
						cmd, _ := handler["command"].(string)
						if strings.Contains(cmd, "ship-it") {
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
		} else if hooks[event] != nil {
			return fmt.Errorf("invalid %s hook list", event)
		}
		timeout := 600
		if event == "SessionStart" {
			timeout = 300
		}
		group := map[string]any{}
		if event == "SessionStart" {
			group["matcher"] = "startup|resume"
		}
		group["hooks"] = []any{map[string]any{"type": "command", "command": command, "timeout": timeout}}
		hooks[event] = append(kept, group)
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
	section := "# Repository delivery\n\nUse `$ship-it` for repository delivery behavior.\n\n- Native lifecycle hooks run the no-argument `ship-it` binary. The binary selects the required work from the hook event and repository state. SessionStart and Stop are hook events, not command-line subcommands.\n- Before the first delivery each local day, ship-it pulls the latest upstream code. A bare invocation stages all changes, creates an automatic commit when needed, and pushes in the same call. SessionStart only performs the daily pull. A tracked deployment command runs after a successful push.\n- Keep Git delivery mechanical: `git add .`, an automatic commit when needed, and push everything Git stages. Do not assess content, select files, or add test, review, or conflict-assessment gates to this flow. The working agent owns diagnosis and repairs.\n- Tests provide advisory evidence, not an automatic deployment veto. Diagnose intentional changes, defects, faulty tests, and environment failures, then repair the relevant cause and continue. Verify the actual deployment independently.\n- Agents finish their work and let the hooks run. Do not invoke ship-it, poll it, or add shipping prompts.\n- Routine Git commands belong to ship-it. Agents may use Git only to diagnose or repair a ship-it defect, with regression coverage.\n- For an authorized destination, finish the change and focused checks. Include deployment and live acceptance checks in the repository command. Fix recoverable failures within that scope. A failed attempt is not proof that the task is blocked.\n- Report a blocker only with the attempted command, observed failure, and the specific missing access, information, or external change. Do not invent approval or policy requirements.\n"
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
