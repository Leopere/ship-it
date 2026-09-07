package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	executablePath = os.Executable
	remoteNameRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type Repo struct {
	Root               string
	Out                io.Writer
	Err                io.Writer
	DeliveryCommit     string
	DeploymentRequired bool
}

func Open(cwd string, out, errOut io.Writer) (*Repo, error) {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return nil, errors.New("not inside a Git repository")
	}
	return &Repo{Root: strings.TrimSpace(stdout.String()), Out: out, Err: errOut}, nil
}

func (r *Repo) Pull() error {
	return r.run("pull", "--autostash")
}

func (r *Repo) Ship() error {
	if err := r.run("add", "."); err != nil {
		return err
	}
	changed, err := r.hasStagedChanges()
	if err != nil {
		return err
	}
	if changed {
		message, err := r.autoMessage()
		if err != nil {
			return err
		}
		if err := r.run("commit", "--no-verify", "--no-gpg-sign", "-m", message); err != nil {
			return err
		}
	}
	pushedCommit, err := r.output("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return errors.New("repository has no commit to ship")
	}
	pushedCommit = strings.TrimSpace(pushedCommit)
	r.DeliveryCommit = pushedCommit
	r.DeploymentRequired = false
	// Resolve this before pushing, so the deployment proves the exact
	// destination selected by the same bare git push. A missing contract still
	// permits the normal push; deployment reports an unsafe destination only
	// after the successful push establishes the handoff boundary.
	destination, destinationErr := r.pushDestination()
	if err := r.run("push", "--no-verify"); err != nil {
		return fmt.Errorf("Git push failed: %w", err)
	}
	// Only a tracked deployment contract opts this repository into deployment.
	cmd := exec.Command("git", "-C", r.Root, "ls-tree", "--name-only", pushedCommit, "--", ".deploy-it.json")
	contract, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("Git push succeeded; read deployment contract: %w", err)
	}
	if strings.TrimSpace(string(contract)) == "" {
		return nil
	}
	r.DeploymentRequired = true
	if destinationErr != nil {
		return fmt.Errorf("Git push succeeded; resolve deployment destination: %w", destinationErr)
	}
	deployIt, err := deployItPath()
	if err != nil {
		return fmt.Errorf("Git push succeeded; find deploy-it: %w", err)
	}
	cmd = exec.Command(deployIt, "--commit", pushedCommit, "--branch", destination.Branch, "--remote", destination.Remote)
	cmd.Dir = r.Root
	cmd.Stdout, cmd.Stderr = r.Out, r.Err
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Git push succeeded; deployment failed: %w", err)
	}
	return nil
}

type pushDestination struct {
	Remote string
	Branch string
}

// pushDestination mirrors the single-branch destinations Git can infer for a
// bare "git push". Deployments require one branch that proves the immutable
// commit, so fan-out and non-branch refspecs deliberately fail.
func (r *Repo) pushDestination() (pushDestination, error) {
	branch, err := r.output("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(branch) == "" {
		return pushDestination{}, errors.New("deployment requires HEAD checked out on a branch")
	}
	branch = strings.TrimSpace(branch)

	output, err := r.output("for-each-ref", "--format=%(push:remotename)%00%(push:remoteref)%00%(upstream:remotename)%00%(upstream:remoteref)", "refs/heads/"+branch)
	if err != nil {
		return pushDestination{}, err
	}
	parts := strings.Split(strings.TrimSuffix(output, "\n"), "\x00")
	if len(parts) != 4 || !remoteNameRE.MatchString(parts[0]) {
		return pushDestination{}, errors.New("deployment requires a configured branch push destination")
	}

	pushSpecs, err := r.configValues("remote." + parts[0] + ".push")
	if err != nil {
		return pushDestination{}, err
	}
	explicitRef := ""
	if len(pushSpecs) > 0 {
		matching := 0
		for _, spec := range pushSpecs {
			matches, supported := refspecMatchesBranch(spec, branch)
			if !supported {
				return pushDestination{}, fmt.Errorf("deployment does not support configured push refspec %q", spec)
			}
			if matches {
				matching++
				var supported bool
				explicitRef, supported = refspecDestination(spec, branch)
				if !supported {
					return pushDestination{}, fmt.Errorf("deployment does not support configured push refspec %q", spec)
				}
			}
		}
		if matching != 1 {
			return pushDestination{}, errors.New("deployment requires exactly one configured branch push destination")
		}
	}

	remoteRef := parts[1]
	if explicitRef != "" {
		remoteRef = explicitRef
	}
	if remoteRef == "" {
		remoteRef, err = r.defaultPushRef(branch, parts[0], parts[2], parts[3])
		if err != nil {
			return pushDestination{}, err
		}
	}
	if !strings.HasPrefix(remoteRef, "refs/heads/") {
		return pushDestination{}, errors.New("deployment requires the pushed destination to be a branch")
	}
	destination := strings.TrimPrefix(remoteRef, "refs/heads/")
	if _, err := r.output("check-ref-format", "--branch", destination); err != nil {
		return pushDestination{}, errors.New("deployment requires a valid pushed branch destination")
	}
	return pushDestination{Remote: parts[0], Branch: destination}, nil
}

func (r *Repo) defaultPushRef(branch, remote, upstreamRemote, upstreamRef string) (string, error) {
	modes, err := r.configValues("push.default")
	if err != nil {
		return "", err
	}
	mode := "simple"
	if len(modes) > 0 {
		mode = modes[len(modes)-1]
	}
	switch strings.TrimSpace(mode) {
	case "", "simple":
		// With a triangular workflow, simple pushes the current branch to the
		// selected push remote. When it uses the upstream remote, Git requires
		// the upstream branch to have the same name.
		if remote != upstreamRemote {
			return "refs/heads/" + branch, nil
		}
		if upstreamRef == "refs/heads/"+branch {
			return upstreamRef, nil
		}
	case "upstream", "tracking":
		if remote == upstreamRemote && strings.HasPrefix(upstreamRef, "refs/heads/") {
			return upstreamRef, nil
		}
	case "current", "matching":
		return "refs/heads/" + branch, nil
	case "nothing":
		return "", errors.New("deployment cannot infer a branch when push.default=nothing")
	}
	return "", errors.New("deployment requires a configured branch push destination")
}

func (r *Repo) configValues(name string) ([]string, error) {
	cmd := exec.Command("git", "-C", r.Root, "config", "--get-all", name)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("git config --get-all %s: %s: %w", name, strings.TrimSpace(stderr.String()), err)
	}
	return strings.Fields(strings.TrimSpace(stdout.String())), nil
}

func refspecMatchesBranch(spec, branch string) (matches, supported bool) {
	spec = strings.TrimPrefix(spec, "+")
	if spec == "" || strings.HasPrefix(spec, "^") || strings.HasPrefix(spec, ":") {
		return false, false
	}
	source := spec
	if colon := strings.IndexByte(spec, ':'); colon >= 0 {
		source = spec[:colon]
	}
	if source == "" {
		return false, false
	}
	fullBranch := "refs/heads/" + branch
	if source == branch || source == fullBranch {
		return true, true
	}
	if strings.Count(source, "*") > 1 {
		return false, false
	}
	if strings.Count(source, "*") == 1 {
		parts := strings.Split(source, "*")
		return strings.HasPrefix(fullBranch, parts[0]) && strings.HasSuffix(fullBranch, parts[1]), true
	}
	return false, true
}

// refspecDestination expands the ordinary shorthand accepted by git push
// (for example main:production). Wildcard destinations are already resolved
// by Git's push atom, so they intentionally leave that value in place.
func refspecDestination(spec, branch string) (string, bool) {
	spec = strings.TrimPrefix(spec, "+")
	if spec == "" || strings.HasPrefix(spec, "^") || strings.HasPrefix(spec, ":") {
		return "", false
	}
	source, destination := spec, ""
	if colon := strings.IndexByte(spec, ':'); colon >= 0 {
		source, destination = spec[:colon], spec[colon+1:]
	} else {
		destination = source
	}
	if source == "" || destination == "" {
		return "", false
	}
	if strings.Contains(destination, "*") {
		return "", true
	}
	if strings.HasPrefix(destination, "refs/") {
		return destination, true
	}
	return "refs/heads/" + destination, true
}

func deployItPath() (string, error) {
	if executable, err := executablePath(); err == nil {
		if _, statErr := os.Stat(executable); statErr == nil {
			candidate := filepath.Join(filepath.Dir(executable), "deploy-it")
			if info, candidateErr := os.Stat(candidate); candidateErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
				return candidate, nil
			}
		}
	}
	path, err := exec.LookPath("deploy-it")
	if err != nil {
		return "", err
	}
	return path, nil
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

func (r *Repo) autoMessage() (string, error) {
	stdout, err := r.output("diff", "--cached", "--name-only")
	if err != nil {
		return "", err
	}
	var paths []string
	for _, path := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	if len(paths) == 1 {
		return "Update " + paths[0], nil
	}
	return fmt.Sprintf("Update %d files", len(paths)), nil
}

func (r *Repo) output(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", r.Root}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return stdout.String(), nil
}

func (r *Repo) run(args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", r.Root}, args...)...)
	cmd.Stdout = r.Out
	cmd.Stderr = r.Err
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Run()
}
