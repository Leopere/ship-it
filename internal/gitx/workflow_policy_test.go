package gitx

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedRunnerFindings(t *testing.T) {
	tests := []struct {
		name     string
		workflow string
		want     string
	}{
		{
			name: "self hosted labels",
			workflow: `jobs:
  test:
    runs-on: [self-hosted, Linux, ARM64, leopere, local]
`,
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
			name: "self hosted conjunction is local",
			workflow: `jobs:
  test:
    runs-on: [self-hosted, ubuntu-latest]
`,
		},
		{
			name: "reusable workflow job has no runner",
			workflow: `jobs:
  test:
    uses: owner/repository/.github/workflows/test.yml@main
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings, err := hostedRunnerFindings([]byte(test.workflow))
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
	if err == nil || !strings.Contains(err.Error(), "must explicitly use self-hosted infrastructure") {
		t.Fatalf("expected hosted-runner rejection, got %v", err)
	}
	after := strings.TrimSpace(outputGit(t, f.work, "rev-parse", "HEAD"))
	if after != before {
		t.Fatalf("ship created a commit before policy rejection: before=%s after=%s", before, after)
	}
}
