package gitx

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedRunnerFindings(t *testing.T) {
	tests := []struct {
		name           string
		workflow       string
		localWorkflows []string
		want           string
	}{
		{
			name: "documented local runner labels",
			workflow: `jobs:
  test:
    runs-on: [self-hosted, Linux, ARM64, leopere, local]
`,
		},
		{
			name: "documented local runner with docker access",
			workflow: `jobs:
  test:
    runs-on: [self-hosted, Linux, ARM64, leopere, local, docker]
`,
		},
		{
			name: "documented native mac runner labels",
			workflow: `jobs:
  test:
    runs-on: [self-hosted, macOS, ARM64, leopere, local, native]
`,
		},
		{
			name: "partial self hosted labels cannot target the documented local runner",
			workflow: `jobs:
  test:
    runs-on: [self-hosted, Linux]
`,
			want: "3:test:self-hosted, Linux",
		},
		{
			name: "direct hosted runner",
			workflow: `jobs:
  test:
    runs-on: ubuntu-latest
`,
			want: "3:test:ubuntu-latest",
		},
		{
			name: "arbitrary runner without self hosted proof",
			workflow: `jobs:
  test:
    runs-on: company-paid-runner
`,
			want: "3:test:company-paid-runner",
		},
		{
			name: "dynamic runner expression",
			workflow: `jobs:
  test:
    runs-on: ${{ vars.RUNNER }}
`,
			want: "3:test:${{ vars.RUNNER }}",
		},
		{
			name: "matrix cannot hide hosted runner",
			workflow: `jobs:
  test:
    strategy:
      matrix:
        runner: [ubuntu-24.04, macos-15-intel]
    runs-on: ${{ matrix.runner }}
`,
			want: "6:test:${{ matrix.runner }}",
		},
		{
			name: "unsupported extra label cannot be serviced locally",
			workflow: `jobs:
  test:
    runs-on: [self-hosted, Linux, ARM64, leopere, local, ubuntu-latest]
`,
			want: "3:test:self-hosted, Linux, ARM64, leopere, local, ubuntu-latest",
		},
		{
			name: "tracked local reusable workflow is verifiable",
			workflow: `jobs:
  test:
    uses: ./.github/workflows/test.yml
`,
			localWorkflows: []string{".github/workflows/test.yml"},
		},
		{
			name: "recommended local reusable workflow syntax is verifiable",
			workflow: `jobs:
  test:
    uses: $/.github/workflows/test.yml
`,
			localWorkflows: []string{".github/workflows/test.yml"},
		},
		{
			name: "missing local reusable workflow is rejected",
			workflow: `jobs:
  test:
    uses: ./.github/workflows/test.yml
`,
			want: "3:test:missing local reusable workflow ./.github/workflows/test.yml",
		},
		{
			name: "remote reusable workflow runner is unverified",
			workflow: `jobs:
  test:
    uses: owner/repository/.github/workflows/test.yml@main
`,
			want: "3:test:unverified reusable workflow owner/repository/.github/workflows/test.yml@main",
		},
		{
			name: "dynamic reusable workflow runner is unverified",
			workflow: `jobs:
  test:
    uses: ${{ vars.REUSABLE_WORKFLOW }}
`,
			want: "3:test:unverified reusable workflow ${{ vars.REUSABLE_WORKFLOW }}",
		},
		{
			name: "local reusable workflow expression is unverified",
			workflow: `jobs:
  test:
    uses: ./.github/workflows/${{ vars.FILE }}.yml
`,
			localWorkflows: []string{".github/workflows/test.yml"},
			want:           "3:test:unverified reusable workflow ./.github/workflows/${{ vars.FILE }}.yml",
		},
		{
			name: "whole job alias cannot hide a hosted runner",
			workflow: `jobs:
  safe:
    runs-on: [self-hosted, Linux, ARM64, leopere, local]
    strategy:
      matrix:
        include:
          - &hosted_job
            runs-on: ubuntu-latest
            steps:
              - run: "true"
  escaped: *hosted_job
`,
			want: "8:escaped:ubuntu-latest",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			localWorkflows := make(map[string]struct{}, len(test.localWorkflows))
			for _, path := range test.localWorkflows {
				localWorkflows[path] = struct{}{}
			}
			findings, err := hostedRunnerFindings([]byte(test.workflow), localWorkflows)
			if err != nil {
				t.Fatal(err)
			}
			if got := formatHostedRunnerFindings(findings); got != test.want {
				t.Fatalf("findings = %q, want %q", got, test.want)
			}
		})
	}
}

func TestShipRejectsUntrackedGitHubHostedWorkflowBeforeCommit(t *testing.T) {
	f := newFixture(t)
	workflow := `jobs:
  test:
    runs-on: ubuntu-latest
`
	write(t, filepath.Join(f.work, ".github", "workflows", "test.yaml"), workflow, 0o644)

	var out, errOut bytes.Buffer
	repo, err := Open(f.work, "origin", &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	before := strings.TrimSpace(outputGit(t, f.work, "rev-parse", "HEAD"))
	_, err = repo.Ship(ShipOptions{Branch: "main", NoTag: true})
	if err == nil || !strings.Contains(err.Error(), "must use a documented self-hosted local runner label profile") {
		t.Fatalf("expected hosted-runner rejection, got %v", err)
	}
	after := strings.TrimSpace(outputGit(t, f.work, "rev-parse", "HEAD"))
	if after != before {
		t.Fatalf("ship created a commit before policy rejection: before=%s after=%s", before, after)
	}
}

func TestShipRejectsHostedWorkflowIntroducedByRemoteMerge(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.work, "local.txt"), "local\n", 0o644)

	other := filepath.Join(filepath.Dir(f.remote), "other")
	runGit(t, filepath.Dir(f.remote), "clone", f.remote, other)
	configure(t, other)
	workflow := `jobs:
  test:
    runs-on: ubuntu-latest
`
	write(t, filepath.Join(other, ".github", "workflows", "hosted.yml"), workflow, 0o644)
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "add hosted workflow")
	runGit(t, other, "push", "origin", "main")

	var out, errOut bytes.Buffer
	repo, err := Open(f.work, "origin", &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Ship(ShipOptions{Branch: "main", NoTag: true})
	if err == nil || !strings.Contains(err.Error(), "ubuntu-latest") {
		t.Fatalf("expected post-merge hosted-runner rejection, got %v", err)
	}
	if got := strings.TrimSpace(outputGit(t, f.remote, "ls-tree", "--name-only", "main", "local.txt")); got != "" {
		t.Fatalf("local change reached remote before post-merge validation: %q", got)
	}
}
