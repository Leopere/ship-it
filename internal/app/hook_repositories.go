package app

import (
	"errors"
	"os"
	"path/filepath"
)

// repositoryHookRoots omits only paths known to have no worktree marker. Do
// not use a failed Git command as the test: a missing Git executable, corrupt
// repository, or inaccessible marker must retain its normal error path.
func repositoryHookRoots(roots []string) []string {
	var selected []string
	for _, root := range roots {
		canonical, safe := permittedExistingAncestor(root, 0)
		if !safe {
			// The normal repository runner owns its existing path restrictions.
			selected = append(selected, root)
			continue
		}
		for path := canonical; ; path = filepath.Dir(path) {
			_, err := os.Lstat(filepath.Join(path, ".git"))
			if err == nil || !errors.Is(err, os.ErrNotExist) {
				selected = append(selected, root)
				break
			}
			if filepath.Dir(path) == path {
				break
			}
		}
	}
	return selected
}
