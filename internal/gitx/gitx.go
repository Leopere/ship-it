package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const Wrapper = `#!/bin/sh
set -eu
exec "${SHIP_IT_BIN:-$HOME/.local/bin/ship-it}" "$@"
`

type Repo struct {
	Root   string
	Remote string
	Out    io.Writer
	Err    io.Writer
}

func Open(cwd, remote string, out, errOut io.Writer) (*Repo, error) {
	root, err := outputAt(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("not inside a Git repository")
	}
	if remote == "" {
		remote = "origin"
	}
	r := &Repo{Root: strings.TrimSpace(root), Remote: remote, Out: out, Err: errOut}
	if _, err := r.output("remote", "get-url", remote); err != nil {
		return nil, fmt.Errorf("remote %q is not configured", remote)
	}
	return r, nil
}

func (r *Repo) DefaultBranch(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	out, err := r.output("ls-remote", "--symref", r.Remote, "HEAD")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "ref: refs/heads/") && strings.HasSuffix(line, "\tHEAD") {
				return strings.TrimSuffix(strings.TrimPrefix(line, "ref: refs/heads/"), "\tHEAD"), nil
			}
		}
	}
	if out, localErr := r.output("symbolic-ref", "--short", "refs/remotes/"+r.Remote+"/HEAD"); localErr == nil {
		return strings.TrimPrefix(strings.TrimSpace(out), r.Remote+"/"), nil
	}
	for _, candidate := range []string{"main", "master"} {
		if out, candidateErr := r.output("ls-remote", "--heads", r.Remote, candidate); candidateErr == nil && strings.TrimSpace(out) != "" {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot determine %s's default branch", r.Remote)
}

func (r *Repo) EnsureWrapper() (string, error) {
	path := filepath.Join(r.Root, "ship.sh")
	current, err := os.ReadFile(path)
	if err == nil && string(current) == Wrapper {
		if chmodErr := os.Chmod(path, 0o755); chmodErr != nil {
			return "", chmodErr
		}
		return "", nil
	}
	var backup string
	if err == nil {
		for n := 0; ; n++ {
			name := "ship.project.sh"
			if n > 0 {
				name = fmt.Sprintf("ship.project.%d.sh", n+1)
			}
			candidate := filepath.Join(r.Root, name)
			if _, statErr := os.Lstat(candidate); os.IsNotExist(statErr) {
				if renameErr := os.Rename(path, candidate); renameErr != nil {
					return "", renameErr
				}
				backup = name
				break
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.WriteFile(path, []byte(Wrapper), 0o755); err != nil {
		return "", err
	}
	if backup != "" {
		fmt.Fprintf(r.Out, "Preserved the old ship.sh as %s\n", backup)
	}
	return backup, nil
}

func (r *Repo) ValidateNoGitHubHostedRunners() error {
	out, err := r.output("ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", ".github/workflows")
	if err != nil {
		return fmt.Errorf("inspect GitHub Actions workflows: %w", err)
	}
	var violations []string
	for _, path := range strings.Split(out, "\x00") {
		if path == "" || (filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(path)))
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return fmt.Errorf("inspect GitHub Actions workflow %s: %w", path, readErr)
		}
		findings, validateErr := hostedRunnerFindings(data)
		if validateErr != nil {
			return fmt.Errorf("validate GitHub Actions workflow %s: %w", path, validateErr)
		}
		for _, finding := range findings {
			violations = append(violations, fmt.Sprintf("%s:%d: job %s selects %s", path, finding.Line, finding.Job, finding.Selection))
		}
	}
	if len(violations) == 0 {
		return nil
	}
	sort.Strings(violations)
	return fmt.Errorf(
		"shipping blocked: every GitHub Actions job must explicitly use self-hosted infrastructure such as [self-hosted, Linux, ARM64, leopere, local]: %s",
		strings.Join(violations, ", "),
	)
}

func (r *Repo) Start(branch string) error {
	if err := r.finishMerge(); err != nil {
		return err
	}
	if err := r.fetch(branch); err != nil {
		return err
	}
	ref := r.Remote + "/" + branch
	if r.isAncestor(ref, "HEAD") {
		fmt.Fprintf(r.Out, "Already current with %s/%s.\n", r.Remote, branch)
		return nil
	}
	if err := r.run("merge", "--autostash", "--no-edit", "--no-verify", "--no-gpg-sign", ref); err != nil {
		return conflictError(err)
	}
	fmt.Fprintf(r.Out, "Merged %s/%s into the current branch.\n", r.Remote, branch)
	return nil
}

type ShipOptions struct {
	Branch  string
	Message string
	NoTag   bool
	Now     time.Time
}

type Result struct {
	Branch string
	Commit string
	Tag    string
	Noop   bool
}

func (r *Repo) Ship(opts ShipOptions) (Result, error) {
	if err := r.ValidateNoGitHubHostedRunners(); err != nil {
		return Result{}, err
	}
	if err := r.finishMerge(); err != nil {
		return Result{}, err
	}
	if err := r.fetch(opts.Branch); err != nil {
		return Result{}, err
	}
	current, err := r.currentBranch()
	if err != nil {
		return Result{}, err
	}
	if err := r.run("add", "-A"); err != nil {
		return Result{}, err
	}
	changed, err := r.hasStagedChanges()
	if err != nil {
		return Result{}, err
	}
	if changed {
		message := opts.Message
		if message == "" {
			message, err = r.autoMessage()
			if err != nil {
				return Result{}, err
			}
		}
		if err := r.run("commit", "--no-verify", "--no-gpg-sign", "-m", message); err != nil {
			return Result{}, err
		}
	}
	source, err := r.output("rev-parse", "HEAD")
	if err != nil {
		return Result{}, errors.New("repository has no commit to ship")
	}
	source = strings.TrimSpace(source)

	if current != opts.Branch {
		if r.localBranchExists(opts.Branch) {
			if err := r.run("switch", opts.Branch); err != nil {
				return Result{}, err
			}
		} else if err := r.run("switch", "--create", opts.Branch, "--track", r.Remote+"/"+opts.Branch); err != nil {
			return Result{}, err
		}
		if !r.isAncestor(source, "HEAD") {
			if err := r.merge(source); err != nil {
				return Result{}, conflictError(err)
			}
		}
	}

	if err := r.mergeRemote(opts.Branch); err != nil {
		return Result{}, err
	}
	remoteRef := r.Remote + "/" + opts.Branch
	head, err := r.output("rev-parse", "HEAD")
	if err != nil {
		return Result{}, err
	}
	head = strings.TrimSpace(head)
	remoteHead, _ := r.output("rev-parse", remoteRef)
	if head == strings.TrimSpace(remoteHead) {
		fmt.Fprintln(r.Out, "Already shipped.")
		return Result{Branch: opts.Branch, Commit: head, Noop: true}, nil
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var tag string
	for attempt := 1; attempt <= 3; attempt++ {
		if !opts.NoTag {
			tag, err = r.nextTag(now)
			if err != nil {
				return Result{}, err
			}
			if err := r.run("tag", tag); err != nil {
				return Result{}, err
			}
		}
		args := []string{"push", "--no-verify", "--atomic", r.Remote, "HEAD:refs/heads/" + opts.Branch}
		if tag != "" {
			args = append(args, "refs/tags/"+tag+":refs/tags/"+tag)
		}
		if err := r.run(args...); err == nil {
			fmt.Fprintf(r.Out, "Shipped %s", opts.Branch)
			if tag != "" {
				fmt.Fprintf(r.Out, " as %s", tag)
			}
			fmt.Fprintln(r.Out, ".")
			return Result{Branch: opts.Branch, Commit: head, Tag: tag}, nil
		} else if attempt == 3 {
			return Result{}, fmt.Errorf("push failed after %d attempts: %w", attempt, err)
		}
		if tag != "" {
			_ = r.runQuiet("tag", "-d", tag)
			tag = ""
		}
		if err := r.fetch(opts.Branch); err != nil {
			return Result{}, err
		}
		if err := r.mergeRemote(opts.Branch); err != nil {
			return Result{}, err
		}
		head, _ = r.output("rev-parse", "HEAD")
		head = strings.TrimSpace(head)
	}
	panic("unreachable")
}

func (r *Repo) fetch(branch string) error {
	return r.run("fetch", "--prune", "--tags", r.Remote, "+refs/heads/"+branch+":refs/remotes/"+r.Remote+"/"+branch)
}

func (r *Repo) mergeRemote(branch string) error {
	ref := r.Remote + "/" + branch
	if r.isAncestor(ref, "HEAD") {
		return nil
	}
	if err := r.merge(ref); err != nil {
		return conflictError(err)
	}
	return nil
}

func (r *Repo) merge(ref string) error {
	return r.run("merge", "--no-edit", "--no-verify", "--no-gpg-sign", ref)
}

func (r *Repo) finishMerge() error {
	mergeHead := filepath.Join(r.Root, ".git", "MERGE_HEAD")
	gitPath, err := r.output("rev-parse", "--git-path", "MERGE_HEAD")
	if err == nil {
		mergeHead = strings.TrimSpace(gitPath)
		if !filepath.IsAbs(mergeHead) {
			mergeHead = filepath.Join(r.Root, mergeHead)
		}
	}
	if _, err := os.Stat(mergeHead); os.IsNotExist(err) {
		return nil
	}
	if err := r.run("add", "-A"); err != nil {
		return err
	}
	unmerged, err := r.output("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return err
	}
	if strings.TrimSpace(unmerged) != "" {
		return errors.New("merge conflicts remain; resolve every conflicted file and rerun ship-it")
	}
	if err := r.run("commit", "--no-edit", "--no-verify", "--no-gpg-sign"); err != nil {
		return err
	}
	return nil
}

func (r *Repo) nextTag(now time.Time) (string, error) {
	prefix := "v" + now.UTC().Format("2006.01.02") + "."
	out, err := r.output("tag", "--list", prefix+"*")
	if err != nil {
		return "", err
	}
	max := 0
	for _, line := range strings.Fields(out) {
		n, convErr := strconv.Atoi(strings.TrimPrefix(line, prefix))
		if convErr == nil && n > max {
			max = n
		}
	}
	return prefix + strconv.Itoa(max+1), nil
}

func (r *Repo) autoMessage() (string, error) {
	out, err := r.output("diff", "--cached", "--name-only")
	if err != nil {
		return "", err
	}
	var paths []string
	for _, path := range strings.Split(strings.TrimSpace(out), "\n") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	if len(paths) == 1 {
		return "Update " + paths[0], nil
	}
	if len(paths) > 1 {
		root := strings.Split(paths[0], "/")[0]
		same := strings.Contains(paths[0], "/")
		for _, path := range paths[1:] {
			if !strings.HasPrefix(path, root+"/") {
				same = false
			}
		}
		if same {
			return "Update " + root, nil
		}
	}
	return fmt.Sprintf("Update %d files", len(paths)), nil
}

func (r *Repo) currentBranch() (string, error) {
	out, err := r.output("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", errors.New("detached HEAD is not supported; switch to a branch and rerun")
	}
	return strings.TrimSpace(out), nil
}

func (r *Repo) hasStagedChanges() (bool, error) {
	cmd := exec.Command("git", "-C", r.Root, "diff", "--cached", "--quiet")
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

func (r *Repo) localBranchExists(branch string) bool {
	return r.runQuiet("show-ref", "--verify", "--quiet", "refs/heads/"+branch) == nil
}

func (r *Repo) isAncestor(ancestor, descendant string) bool {
	return r.runQuiet("merge-base", "--is-ancestor", ancestor, descendant) == nil
}

func (r *Repo) run(args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", r.Root}, args...)...)
	cmd.Stdout = r.Out
	cmd.Stderr = r.Err
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_MERGE_AUTOEDIT=no")
	return cmd.Run()
}

func (r *Repo) runQuiet(args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", r.Root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_MERGE_AUTOEDIT=no")
	return cmd.Run()
}

func (r *Repo) output(args ...string) (string, error) {
	return outputAt(r.Root, args...)
}

func outputAt(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return stdout.String(), nil
}

func conflictError(err error) error {
	return fmt.Errorf("merge stopped; resolve every conflicted file according to the requested work, then rerun ship-it: %w", err)
}
