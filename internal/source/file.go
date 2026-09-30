package source

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// fetchFile downloads the SKILL.md at url and records it as a commit in the
// clone at dir, as <name>/SKILL.md with the name from its frontmatter. It
// returns HEAD when the file is unchanged, and never touches the work tree.
func fetchFile(dir, url string) (string, error) {
	data, err := download(url)
	if err != nil {
		return "", err
	}
	name, _ := frontmatter(string(data))
	if !validName.MatchString(name) {
		return "", fmt.Errorf("%s is no SKILL.md: it has no frontmatter with a usable name", url)
	}
	blob, err := gitInput(dir, string(data), "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	folder, err := gitInput(dir, "100644 blob "+blob+"\tSKILL.md\n", "mktree")
	if err != nil {
		return "", err
	}
	tree, err := gitInput(dir, "040000 tree "+folder+"\t"+name+"\n", "mktree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", "--no-gpg-sign", "-m", url, tree}
	if head, err := Head(dir); err == nil {
		if current, _ := git(dir, "rev-parse", "HEAD^{tree}"); current == tree {
			return head, nil
		}
		args = append(args, "-p", head)
	}
	return git(dir, args...)
}

func download(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// SetOrigin points the clone at dir to url.
func SetOrigin(dir, url string) error {
	_, err := git(dir, "remote", "set-url", "origin", url)
	return err
}

// commitEnv lets fetchFile commit without a configured git identity.
var commitEnv = []string{
	"GIT_AUTHOR_NAME=skillet", "GIT_AUTHOR_EMAIL=skillet@localhost",
	"GIT_COMMITTER_NAME=skillet", "GIT_COMMITTER_EMAIL=skillet@localhost",
}

func gitInput(dir, input string, args ...string) (string, error) {
	return run(dir, strings.NewReader(input), args...)
}
