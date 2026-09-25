package source

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Spec is a skill source written the way the `skills` npm CLI takes it.
type Spec struct {
	URL   string // what to clone
	Ref   string // a branch, tag or commit; empty for the default branch
	Path  string // a directory or a skill folder inside the repository
	Skill string // the skill named by owner/repo@skill
	// Repo tells that the form alone names a git repository. Otherwise the
	// URL may be a web page or an MCP server, and a local path a plain
	// directory.
	Repo bool
}

var (
	githubTree = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([^/]+)/([^/]+)/tree/([^/]+)(?:/(.*))?$`)
	githubRepo = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([^/]+)/([^/?]+)`)
	gitlabTree = regexp.MustCompile(`^(https?)://([^/]+)/(.+?)/-/tree/([^/]+)(?:/(.*))?$`)
	gitlabRepo = regexp.MustCompile(`^https?://gitlab\.com/([^/]+/.+?)(?:\.git)?/?$`)
	shortSkill = regexp.MustCompile(`^([^/:]+)/([^/@:]+)@(.+)$`)
	shortPath  = regexp.MustCompile(`^([^/:]+)/([^/:]+?)(?:/(.+?))?/?$`)
)

// ParseSpec reads a source as `npx skills add` does: owner/repo, with
// /sub/path or @skill, github: and gitlab: prefixes, GitHub and GitLab URLs
// with /tree/<ref>/<path>, git URLs, and local paths. A #ref suffix names
// the ref, and #ref@skill a skill too. Any other http(s) URL is returned as
// it is.
func ParseSpec(arg string) (Spec, error) {
	if isLocal(arg) {
		abs, err := filepath.Abs(arg)
		return Spec{URL: abs}, err
	}
	rest, fragmentRef, fragmentSkill := arg, "", ""
	if base, fragment, ok := strings.Cut(arg, "#"); ok && gitLike(base) {
		rest = base
		fragmentRef, fragmentSkill, _ = strings.Cut(fragment, "@")
	}
	if tail, ok := strings.CutPrefix(rest, "github:"); ok {
		rest = tail
	} else if tail, ok := strings.CutPrefix(rest, "gitlab:"); ok {
		rest = "https://gitlab.com/" + tail
	}
	spec := Spec{Ref: fragmentRef, Skill: fragmentSkill, Repo: true}
	var sub string
	if m := githubTree.FindStringSubmatch(rest); m != nil {
		spec.URL, spec.Ref, sub = githubURL(m[1], m[2]), m[3], m[4]
	} else if m := githubRepo.FindStringSubmatch(rest); m != nil {
		spec.URL = githubURL(m[1], m[2])
	} else if m := gitlabTree.FindStringSubmatch(rest); m != nil && m[2] != "github.com" {
		spec.URL = m[1] + "://" + m[2] + "/" + strings.TrimSuffix(m[3], ".git") + ".git"
		spec.Ref, sub = m[4], m[5]
	} else if m := gitlabRepo.FindStringSubmatch(rest); m != nil {
		spec.URL = "https://gitlab.com/" + m[1] + ".git"
	} else if m := shortSkill.FindStringSubmatch(rest); m != nil {
		spec.URL = githubURL(m[1], m[2])
		if spec.Skill == "" {
			spec.Skill = m[3]
		}
	} else if m := shortPath.FindStringSubmatch(rest); m != nil {
		spec.URL, sub = githubURL(m[1], m[2]), m[3]
	} else if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
		if !strings.HasSuffix(rest, ".git") {
			return Spec{URL: arg}, nil
		}
		spec.URL = rest
	} else if strings.Contains(rest, ":") {
		spec.URL = rest
	} else {
		return Spec{}, fmt.Errorf("%q is neither owner/repo, a URL nor a path", arg)
	}
	sub = strings.Trim(sub, "/")
	for _, segment := range strings.Split(sub, "/") {
		if sub != "" && (segment == "" || segment == "." || segment == "..") {
			return Spec{}, fmt.Errorf("%q: the path inside the repository must not hold empty, . or .. segments", arg)
		}
	}
	spec.Path = sub
	return spec, nil
}

func githubURL(owner, repo string) string {
	return "https://github.com/" + owner + "/" + strings.TrimSuffix(repo, ".git") + ".git"
}

func isLocal(arg string) bool {
	return filepath.IsAbs(arg) || arg == "." || arg == ".." ||
		strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../")
}

// gitLike tells whether a #fragment of the source is a ref: anything but a
// web URL of a host other than GitHub or GitLab that does not end in .git.
func gitLike(base string) bool {
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return true
	}
	return strings.Contains(base, "github.com/") || strings.Contains(base, "gitlab.com/") || strings.HasSuffix(base, ".git")
}

// IsRepo tells whether git reads a repository with a HEAD at url. A web
// server that answers every request satisfies git without listing one.
func IsRepo(url string) bool {
	out, err := git("", "ls-remote", "-q", url, "HEAD")
	return err == nil && out != ""
}

// SameRepo tells whether two clone URLs name one repository, whatever their
// scheme, user, case, trailing slash or .git suffix.
func SameRepo(a, b string) bool { return repoKey(a) == repoKey(b) }

func repoKey(url string) string {
	key := strings.ToLower(url)
	for _, scheme := range []string{"https://", "http://", "ssh://", "git://"} {
		key = strings.TrimPrefix(key, scheme)
	}
	if at := strings.Index(key, "@"); at >= 0 && at < strings.IndexAny(key, "/:") {
		key = key[at+1:]
	}
	key = strings.ReplaceAll(key, ":", "/")
	return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(key, "/"), ".git"), "/")
}
