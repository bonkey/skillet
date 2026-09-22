// Package source clones skill repositories and finds the skills inside them.
package source

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bonkey/skillet/internal/catalog"
	"gopkg.in/yaml.v3"
)

// Skill is what a source knows about one of its skills.
type Skill struct {
	Path        string `json:"path"` // folder relative to the clone root
	Description string `json:"description,omitempty"`
}

var (
	shorthand = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)
	validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// ParseName turns "owner/repo" or a git URL into "owner/repo" and a clone URL.
func ParseName(arg string) (name, url string, err error) {
	if shorthand.MatchString(arg) {
		return arg, "https://github.com/" + arg + ".git", nil
	}
	if strings.Contains(arg, ":") || filepath.IsAbs(arg) {
		if owner, repo, ok := catalog.RepoParts(arg); ok {
			return owner + "/" + repo, arg, nil
		}
	}
	return "", "", fmt.Errorf("%q is neither owner/repo nor a git URL", arg)
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Clone makes a shallow clone of url at ref: a branch, a tag, a full commit
// hash, or the default branch when empty.
func Clone(url, ref, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if ref == "" {
		_, err := git("", "clone", "-q", "--depth", "1", url, dir)
		return err
	}
	// A fetch accepts a commit hash; `clone --branch` takes only branches and tags.
	err := func() error {
		if _, err := git("", "init", "-q", dir); err != nil {
			return err
		}
		if _, err := git(dir, "remote", "add", "origin", url); err != nil {
			return err
		}
		commit, err := Fetch(dir, ref)
		if err != nil {
			return err
		}
		return Checkout(dir, commit)
	}()
	if err != nil {
		os.RemoveAll(dir)
	}
	return err
}

func Head(dir string) (string, error) { return git(dir, "rev-parse", "HEAD") }

// Fetch downloads the latest upstream commit without touching the work tree
// and returns its hash.
func Fetch(dir, ref string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	if _, err := git(dir, "fetch", "-q", "--depth", "1", "origin", ref); err != nil {
		return "", err
	}
	return git(dir, "rev-parse", "FETCH_HEAD")
}

// Changed reports whether path differs between two commits.
func Changed(dir, from, to, path string) (bool, error) {
	_, err := git(dir, "diff", "--quiet", from, to, "--", path)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

// Checkout moves the clone to a fetched commit.
func Checkout(dir, commit string) error {
	_, err := git(dir, "reset", "-q", "--hard", commit)
	return err
}

// Discover finds the skills in a clone, keyed by name, searching below sub
// when it is set; paths stay relative to the clone root. A SKILL.md at the
// searched root makes the whole tree one skill. Folders below a skill are
// not searched. When a name occurs twice, the shallowest folder wins. A sub
// that does not exist holds no skills.
func Discover(root, sub string) (map[string]Skill, error) {
	skills := map[string]Skill{}
	start := filepath.Join(root, filepath.FromSlash(sub))
	if info, err := os.Stat(start); err != nil || !info.IsDir() {
		return skills, nil
	}
	if _, err := os.Stat(filepath.Join(start, "SKILL.md")); err == nil {
		rel, _ := filepath.Rel(root, start)
		name, skill := read(root, filepath.ToSlash(rel))
		skills[name] = skill
		return skills, nil
	}
	err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if d.Name() == ".git" || d.Name() == "node_modules" {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		name, skill := read(root, filepath.ToSlash(rel))
		if old, ok := skills[name]; !ok || depth(skill.Path) < depth(old.Path) {
			skills[name] = skill
		}
		return filepath.SkipDir
	})
	return skills, err
}

func depth(path string) int { return strings.Count(path, "/") }

// read parses the SKILL.md of one skill folder. The folder name stands in
// for a missing or unusable frontmatter name.
func read(root, rel string) (string, Skill) {
	dir := filepath.Join(root, filepath.FromSlash(rel))
	data, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	name, description := frontmatter(string(data))
	if !validName.MatchString(name) {
		name = filepath.Base(dir)
	}
	return name, Skill{Path: rel, Description: strings.Join(strings.Fields(description), " ")}
}

var fieldLine = regexp.MustCompile(`(?m)^(name|description):[ \t]*(.*)$`)

func frontmatter(text string) (name, description string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", ""
	}
	block, _, found := strings.Cut(text[4:], "\n---")
	if !found {
		return "", ""
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if yaml.Unmarshal([]byte(block), &fm) == nil {
		return fm.Name, fm.Description
	}
	// Unquoted colons make many real frontmatters invalid YAML.
	for _, m := range fieldLine.FindAllStringSubmatch(block, -1) {
		value := strings.Trim(strings.TrimSpace(m[2]), `"'`)
		if m[1] == "name" {
			name = value
		} else {
			description = value
		}
	}
	return name, description
}
