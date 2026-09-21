// Package importer reads the global lock file of the `skills` npm CLI.
package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/bonkey/skillet/internal/source"
)

type Entry struct {
	Name   string // installed folder name
	Source string // "owner/repo"
	URL    string
	Ref    string
	Path   string // skill folder inside the repository
}

// Read parses a version 3 `.skill-lock.json`. Entries that do not come from
// a git repository are skipped and reported by name.
func Read(file string) (entries []Entry, skipped []string, err error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, err
	}
	var lock struct {
		Skills map[string]struct {
			SourceURL string `json:"sourceUrl"`
			Ref       string `json:"ref"`
			SkillPath string `json:"skillPath"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	for name, skill := range lock.Skills {
		sourceName, url, err := source.ParseName(skill.SourceURL)
		if err != nil {
			skipped = append(skipped, name)
			continue
		}
		dir := path.Dir(strings.TrimPrefix(skill.SkillPath, "/"))
		entries = append(entries, Entry{Name: name, Source: sourceName, URL: url, Ref: skill.Ref, Path: dir})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	sort.Strings(skipped)
	return entries, skipped, nil
}

// PackName derives a pack name from a source: the owner for repositories
// simply called "skills", the repository name otherwise.
func PackName(sourceName string) string {
	owner, repo, _ := strings.Cut(sourceName, "/")
	if repo == "skills" || repo == "" {
		return owner
	}
	return repo
}
