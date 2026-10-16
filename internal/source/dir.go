package source

import (
	"os"
	"path/filepath"
)

// IsLocalDir tells whether url is the absolute path of a directory on this
// machine. Clone copies one that git reads as no repository.
func IsLocalDir(url string) bool {
	info, err := os.Stat(url)
	return filepath.IsAbs(url) && err == nil && info.IsDir()
}

// plainDir tells whether url is a local directory that git reads as no
// repository.
func plainDir(url string) bool { return IsLocalDir(url) && !IsRepo(url) }

// fetchDir records the files below the directory src as a commit in the
// clone at dir. It returns HEAD when they are unchanged, and touches neither
// the work tree nor src. The clone's own index stays as it is.
func fetchDir(dir, src string) (string, error) {
	gitDir, err := filepath.Abs(filepath.Join(dir, ".git"))
	if err != nil {
		return "", err
	}
	index := filepath.Join(gitDir, "skillet-index")
	defer os.Remove(index)
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := run(dir, nil, env, "--git-dir", gitDir, "--work-tree", src, "add", "-A"); err != nil {
		return "", err
	}
	tree, err := run(dir, nil, env, "--git-dir", gitDir, "write-tree")
	if err != nil {
		return "", err
	}
	return commit(dir, tree, src)
}
