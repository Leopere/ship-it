package deploy

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
)

type ShippedRevision struct {
	Remote string
	Branch string
	Commit string
	Tag    string
}

func Run(root string, revision ShippedRevision, out, errOut io.Writer) error {
	path, err := exec.LookPath("deploy-it")
	if err != nil {
		return errors.New("Git shipping succeeded, but deploy-it is not installed or not on PATH")
	}
	args := []string{
		"--commit", revision.Commit,
		"--remote", revision.Remote,
		"--branch", revision.Branch,
		"--tag", revision.Tag,
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = root
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Git shipping succeeded at %s, but deploy-it failed: %w", short(revision.Commit), err)
	}
	return nil
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
