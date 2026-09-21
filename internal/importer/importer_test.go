package importer

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const lock = `{
  "version": 3,
  "skills": {
    "pr": {
      "source": "bonkey/skills",
      "sourceType": "github",
      "sourceUrl": "https://github.com/bonkey/skills.git",
      "skillPath": "skills/pr/SKILL.md",
      "skillFolderHash": "87c7c17d2988454e79c5b7a298a9e81101e82eaa",
      "installedAt": "2026-04-05T08:01:01.058Z",
      "updatedAt": "2026-09-07T13:37:03.567Z"
    },
    "herdr": {
      "source": "herdrdev/herdr",
      "sourceType": "github",
      "sourceUrl": "https://github.com/herdrdev/herdr.git",
      "ref": "stable",
      "skillPath": "SKILL.md"
    },
    "odd": { "source": "somewhere", "sourceType": "well-known", "sourceUrl": "" }
  },
  "dismissed": { "findSkillsPrompt": true },
  "lastSelectedAgents": ["claude-code"]
}`

func TestRead(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".skill-lock.json")
	if err := os.WriteFile(file, []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, skipped, err := Read(file)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Name: "herdr", Source: "herdrdev/herdr", URL: "https://github.com/herdrdev/herdr.git", Ref: "stable", Path: "."},
		{Name: "pr", Source: "bonkey/skills", URL: "https://github.com/bonkey/skills.git", Path: "skills/pr"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("got %+v", entries)
	}
	if !reflect.DeepEqual(skipped, []string{"odd"}) {
		t.Errorf("skipped %v", skipped)
	}
}

func TestPackName(t *testing.T) {
	for in, want := range map[string]string{
		"wondelai/skills":         "wondelai",
		"bonkey/skills-private":   "skills-private",
		"dietrichgebert/ponytail": "ponytail",
	} {
		if got := PackName(in); got != want {
			t.Errorf("PackName(%q) = %q", in, got)
		}
	}
}
