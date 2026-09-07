package workday

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunsOncePerCanonicalRepositoryAndLocalDay(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	root := testDir(t)
	alias := filepath.Join(testDir(t), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 9, 6, 23, 30, 0, 0, time.FixedZone("local", -4*60*60))
	calls := 0
	pull := func() error { calls++; return nil }
	ships := 0
	ship := func() error { ships++; return nil }
	for _, path := range []string{root, alias, root} {
		if err := Run(path, day, Start, pull, ship); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("same-day pulls = %d", calls)
	}
	if err := Run(root, day.Add(time.Hour), Start, pull, ship); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("next-day pulls = %d", calls)
	}
	if ships != 0 {
		t.Fatalf("startup shipments = %d", ships)
	}
}

func TestFailedPullCanRetry(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	root := testDir(t)
	calls := 0
	pull := func() error {
		calls++
		if calls == 1 {
			return errors.New("offline")
		}
		return nil
	}
	if err := Run(root, time.Now(), Auto, pull, func() error { return nil }); err == nil {
		t.Fatal("first pull should fail")
	}
	if err := Run(root, time.Now(), Auto, pull, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("pull attempts = %d", calls)
	}
}

func TestStopPullsAndShipsWhenItIsFirstInvocation(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	var actions []string
	err := Run(testDir(t), time.Now(), Stop, func() error {
		actions = append(actions, "pull")
		return nil
	}, func() error {
		actions = append(actions, "ship")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(actions, ","); got != "pull,ship" {
		t.Fatalf("actions = %s", got)
	}
}

func TestBareInvocationPullsAndShipsOnFirstCall(t *testing.T) {
	t.Setenv("HOME", testDir(t))
	root := testDir(t)
	var actions []string
	pull := func() error { actions = append(actions, "pull"); return nil }
	ship := func() error { actions = append(actions, "ship"); return nil }
	for range 2 {
		if err := Run(root, time.Now(), Auto, pull, ship); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(actions, ","); got != "pull,ship,ship" {
		t.Fatalf("bare delivery actions = %s", got)
	}
}

func testDir(t *testing.T) string {
	t.Helper()
	if os.Getenv("SHIP_IT_KEEP_TEST_DIRS") == "" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("", "ship-it-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
