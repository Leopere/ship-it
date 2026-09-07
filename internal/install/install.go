package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Leopere/ship-it/internal/cursorhook"
	"github.com/Leopere/ship-it/internal/skilldoc"
)

func Local(copyBinary bool, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if copyBinary {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		exe, _ = filepath.EvalSymlinks(exe)
		dest := filepath.Join(home, ".local", "bin", "ship-it")
		if exe != dest {
			if err := copyAtomic(exe, dest, 0o755); err != nil {
				return err
			}
		}
		fmt.Fprintln(out, "Installed", dest)
	}
	for _, base := range []string{
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".cursor", "skills"),
	} {
		dir := filepath.Join(base, "ship-it")
		if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o755); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, "SKILL.md"), []byte(skilldoc.SkillMD), 0o644); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, "agents", "openai.yaml"), []byte(skilldoc.OpenAIYAML), 0o644); err != nil {
			return err
		}
	}
	if err := installCodexHooks(home, out); err != nil {
		return err
	}
	if err := updateDeliveryGuidance(home); err != nil {
		return err
	}
	if err := cursorhook.Install(home, out); err != nil {
		return err
	}
	fmt.Fprintln(out, "Installed the ship-it skill for Codex, Claude, and Cursor.")
	return nil
}

func Remote(host string, out io.Writer) error {
	if strings.TrimSpace(host) == "" || strings.HasPrefix(host, "-") {
		return errors.New("invalid SSH host")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	tmp := ".local/bin/ship-it.new"
	if err := exec.Command("ssh", host, "mkdir -p .local/bin").Run(); err != nil {
		return fmt.Errorf("prepare %s: %w", host, err)
	}
	cmd := exec.Command("scp", exe, host+":"+tmp)
	cmd.Stdout, cmd.Stderr = out, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	remoteCommand := "chmod 755 .local/bin/ship-it.new && mv .local/bin/ship-it.new .local/bin/ship-it && .local/bin/ship-it install --skills-only"
	cmd = exec.Command("ssh", host, remoteCommand)
	cmd.Stdout, cmd.Stderr = out, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Fprintf(out, "Installed ship-it on %s.\n", host)
	return nil
}

func copyAtomic(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".ship-it-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ship-it-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
