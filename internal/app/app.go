package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Leopere/ship-it/internal/gitx"
	installer "github.com/Leopere/ship-it/internal/install"
	"github.com/Leopere/ship-it/internal/skilldoc"
	updater "github.com/Leopere/ship-it/internal/update"
)

const usage = `ship-it ships every local Git change without gates.

Usage:
  ship-it [message...]
  ship-it start [--remote origin] [--branch branch]
  ship-it install [--host aedev-mac] [--skills-only]
  ship-it update
  ship-it version
  ship-it skill

Ship options:
  -m, --message text   explicit commit message
  --remote name        remote to use (default origin)
  --branch name        destination branch (default remote HEAD)
  --no-tag             do not create a calendar tag
`

func Run(args []string, version string, out, errOut io.Writer) error {
	if os.Getenv("SHIP_IT_UPDATED") != "" {
		if err := installer.Local(false, out); err != nil {
			return fmt.Errorf("install updated skills: %w", err)
		}
	}
	if len(args) > 0 {
		switch args[0] {
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
		case "start":
			return runStart(args[1:], version, args, out, errOut)
		}
	}
	return runShip(args, version, out, errOut)
}

func runStart(args []string, version string, original []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(errOut)
	remote := fs.String("remote", "origin", "remote")
	branch := fs.String("branch", "", "destination branch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := gitx.Open(mustCwd(), *remote, out, errOut)
	if err != nil {
		return err
	}
	if err := updater.Maybe(version, original, out); err != nil {
		return err
	}
	if _, err := repo.EnsureWrapper(); err != nil {
		return err
	}
	destination, err := repo.DefaultBranch(*branch)
	if err != nil {
		return err
	}
	return repo.Start(destination)
}

func runShip(args []string, version string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("ship", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var message string
	fs.StringVar(&message, "m", "", "commit message")
	fs.StringVar(&message, "message", "", "commit message")
	remote := fs.String("remote", "origin", "remote")
	branch := fs.String("branch", "", "destination branch")
	noTag := fs.Bool("no-tag", false, "skip calendar tag")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if message == "" && len(fs.Args()) > 0 {
		message = strings.Join(fs.Args(), " ")
	}
	repo, err := gitx.Open(mustCwd(), *remote, out, errOut)
	if err != nil {
		return err
	}
	if err := updater.Maybe(version, args, out); err != nil {
		return err
	}
	if _, err := repo.EnsureWrapper(); err != nil {
		return err
	}
	destination, err := repo.DefaultBranch(*branch)
	if err != nil {
		return err
	}
	_, err = repo.Ship(gitx.ShipOptions{Branch: destination, Message: message, NoTag: *noTag})
	return err
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
