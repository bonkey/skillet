package link

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fixture struct {
	repos, agents, claude string
	desired               map[string]string
}

func setup(t *testing.T) fixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	f := fixture{
		repos:   filepath.Join(home, ".local", "share", "skillet", "repos"),
		agents:  filepath.Join(home, ".agents", "skills"),
		claude:  filepath.Join(home, ".claude", "skills"),
		desired: map[string]string{},
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		target := filepath.Join(f.repos, "acme", "skills", "skills", name)
		mkdir(t, target)
		f.desired[name] = target
	}
	mkdir(t, f.agents)
	mkdir(t, f.claude)
	return f
}

// sync returns the operations keyed by ".agents/<name>" or ".claude/<name>".
func (f fixture) sync(t *testing.T, opt Options) map[string]string {
	t.Helper()
	opt.ReposDir = f.repos
	actions, _, err := Sync([]string{f.agents, f.claude}, f.desired, opt)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range actions {
		out[filepath.Base(filepath.Dir(a.Dir))+"/"+a.Name] = a.Op
	}
	return out
}

func TestPurgeDeletesWhatSkilletDoesNotManage(t *testing.T) {
	f := setup(t)
	mkdir(t, filepath.Join(f.claude, "hand"))
	mkdir(t, filepath.Join(f.claude, ".skillet-sessions"))
	symlink(t, "/somewhere/else", filepath.Join(f.agents, "foreign"))
	symlink(t, f.desired["beta"], filepath.Join(f.agents, "beta"))
	mkdir(t, filepath.Join(f.agents, "kept"))
	f.desired = map[string]string{"alpha": f.desired["alpha"]}
	want := map[string]string{".claude/hand": OpDelete, ".agents/foreign": OpDelete, ".agents/beta": OpUnlink,
		".claude/alpha": OpLink, ".agents/alpha": OpLink}
	if got := f.sync(t, Options{Purge: true, Keep: map[string]bool{"kept": true}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	for _, gone := range []string{filepath.Join(f.claude, "hand"), filepath.Join(f.agents, "foreign")} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be deleted", gone)
		}
	}
	for _, stays := range []string{filepath.Join(f.claude, ".skillet-sessions"), filepath.Join(f.agents, "kept")} {
		if _, err := os.Lstat(stays); err != nil {
			t.Errorf("%s should stay: %v", stays, err)
		}
	}
	if got := f.sync(t, Options{}); len(got) != 0 {
		t.Errorf("without purge nothing else changes: %v", got)
	}
}

func TestSyncReportsTheLinksItKeeps(t *testing.T) {
	f := setup(t)
	f.sync(t, Options{})
	actions, kept, err := Sync([]string{f.agents, f.claude}, f.desired, Options{ReposDir: f.repos})
	if err != nil || len(actions) != 0 || len(kept) != 6 || kept[0].Op != OpKeep {
		t.Fatalf("a second sync keeps every link: %v %v %v", actions, kept, err)
	}
	if got := kept[0].String(); !strings.HasPrefix(got, "keep     ") || !strings.Contains(got, " -> ") {
		t.Errorf("keep line: %q", got)
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func target(t *testing.T, path string) string {
	t.Helper()
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("%s is not a symlink: %v", path, err)
	}
	return got
}

func TestSyncLinksEveryDirStraightIntoTheClones(t *testing.T) {
	f := setup(t)
	want := map[string]string{}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		want[".agents/"+name], want[".claude/"+name] = OpLink, OpLink
	}
	if got := f.sync(t, Options{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("first sync: %v", got)
	}
	for _, dir := range []string{f.agents, f.claude} {
		if got := target(t, filepath.Join(dir, "alpha")); got != f.desired["alpha"] {
			t.Errorf("%s alpha -> %s", dir, got)
		}
	}
	if got := Linked([]string{f.agents, f.claude}, f.repos); !reflect.DeepEqual(got, []string{"alpha", "beta", "gamma"}) {
		t.Errorf("linked: %v", got)
	}

	if got := f.sync(t, Options{}); len(got) != 0 {
		t.Errorf("second sync should be a no-op: %v", got)
	}

	delete(f.desired, "beta")
	f.desired["gamma"] = f.desired["alpha"]
	want = map[string]string{".agents/beta": OpUnlink, ".claude/beta": OpUnlink, ".agents/gamma": OpRelink, ".claude/gamma": OpRelink}
	if got := f.sync(t, Options{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("third sync: %v", got)
	}
	for _, dir := range []string{f.agents, f.claude} {
		if _, err := os.Lstat(filepath.Join(dir, "beta")); !os.IsNotExist(err) {
			t.Errorf("beta should be gone from %s", dir)
		}
	}
}

func TestSyncKeepsNamedLinks(t *testing.T) {
	f := setup(t)
	f.sync(t, Options{})
	delete(f.desired, "beta")
	if got := f.sync(t, Options{Keep: map[string]bool{"beta": true}}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	if got := target(t, filepath.Join(f.claude, "beta")); got != filepath.Join(f.repos, "acme", "skills", "skills", "beta") {
		t.Errorf("beta -> %s", got)
	}
}

func TestSyncLeavesUnmanagedEntriesAlone(t *testing.T) {
	f := setup(t)
	elsewhere := filepath.Join(t.TempDir(), "app", "skill")
	mkdir(t, elsewhere)
	// An unmanaged folder where a skill goes, one that is not desired, and links to them.
	for _, name := range []string{"alpha", "theirs"} {
		mkdir(t, filepath.Join(f.agents, name))
		symlink(t, "../../.agents/skills/"+name, filepath.Join(f.claude, name))
	}
	symlink(t, elsewhere, filepath.Join(f.claude, "beta")) // foreign link where a skill goes
	mkdir(t, filepath.Join(f.claude, "own-dir"))

	want := map[string]string{
		".agents/alpha": OpConflict, ".claude/alpha": OpConflict,
		".agents/beta": OpLink, ".claude/beta": OpConflict,
		".agents/gamma": OpLink, ".claude/gamma": OpLink,
	}
	if got := f.sync(t, Options{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	for _, dir := range []string{filepath.Join(f.agents, "alpha"), filepath.Join(f.agents, "theirs"), filepath.Join(f.claude, "own-dir")} {
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s was touched", dir)
		}
	}
	if got := target(t, filepath.Join(f.claude, "beta")); got != elsewhere {
		t.Errorf("foreign link was touched: %s", got)
	}
	for _, name := range []string{"alpha", "theirs"} {
		if got := target(t, filepath.Join(f.claude, name)); got != "../../.agents/skills/"+name {
			t.Errorf("link %s was touched: %s", name, got)
		}
	}
}

func TestSyncReplacesUnmanagedEntriesWhenAllowed(t *testing.T) {
	f := setup(t)
	elsewhere := filepath.Join(t.TempDir(), "old-store", "alpha")
	mkdir(t, elsewhere)
	symlink(t, elsewhere, filepath.Join(f.claude, "alpha"))      // foreign link where a skill goes
	mkdir(t, filepath.Join(f.agents, "alpha", "old-content"))    // real folder where a skill goes
	mkdir(t, filepath.Join(f.agents, "beta", "old-content"))     // one that may not be replaced
	symlink(t, elsewhere, filepath.Join(f.claude, "not-wanted")) // foreign link no skill needs
	opt := Options{Replace: func(name string) bool { return name == "alpha" }}

	opt.DryRun = true
	dry := f.sync(t, opt)
	if _, err := os.Stat(filepath.Join(f.agents, "alpha", "old-content")); err != nil {
		t.Fatal("dry run deleted a folder")
	}
	opt.DryRun = false
	got := f.sync(t, opt)
	want := map[string]string{
		".agents/alpha": OpReplace, ".claude/alpha": OpReplace,
		".agents/beta": OpConflict, ".claude/beta": OpLink,
		".agents/gamma": OpLink, ".claude/gamma": OpLink,
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(dry, want) {
		t.Fatalf("got %v\ndry %v", got, dry)
	}
	for _, dir := range []string{f.agents, f.claude} {
		if got := target(t, filepath.Join(dir, "alpha")); got != f.desired["alpha"] {
			t.Errorf("%s alpha -> %s", dir, got)
		}
	}
	if _, err := os.Stat(filepath.Join(f.agents, "beta", "old-content")); err != nil {
		t.Error("beta must stay: replacing it was not allowed")
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Error("replacing a link must not delete what it pointed at")
	}
	if got := target(t, filepath.Join(f.claude, "not-wanted")); got != elsewhere {
		t.Errorf("an entry no skill needs must stay: %s", got)
	}
}

func TestSyncTouchesASharedDirOnce(t *testing.T) {
	f := setup(t)
	if err := os.Remove(f.claude); err != nil {
		t.Fatal(err)
	}
	symlink(t, "../.agents/skills", f.claude) // ~/.claude/skills -> ~/.agents/skills

	want := map[string]string{".agents/alpha": OpLink, ".agents/beta": OpLink, ".agents/gamma": OpLink}
	if got := f.sync(t, Options{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("first sync: %v", got)
	}
	if got := f.sync(t, Options{}); len(got) != 0 {
		t.Fatalf("second sync must not touch the shared directory: %v", got)
	}
	if _, err := os.Stat(filepath.Join(f.claude, "alpha")); err != nil {
		t.Errorf("the skill must be reachable through the symlinked directory: %v", err)
	}
}

func TestSyncOwnsLinksThroughAnotherDir(t *testing.T) {
	f := setup(t)
	symlink(t, f.desired["alpha"], filepath.Join(f.agents, "alpha"))
	symlink(t, "../../.agents/skills/alpha", filepath.Join(f.claude, "alpha"))       // through a managed link
	symlink(t, "../../.agents/skills/dangling", filepath.Join(f.claude, "dangling")) // at a missing entry
	symlink(t, filepath.Join(f.repos, "gone", "x"), filepath.Join(f.claude, "x"))    // into a deleted clone
	if got := Linked([]string{f.agents, f.claude}, f.repos); !reflect.DeepEqual(got, []string{"alpha", "dangling", "x"}) {
		t.Errorf("linked: %v", got)
	}
	f.desired = map[string]string{"alpha": f.desired["alpha"]}
	want := map[string]string{".claude/alpha": OpRelink, ".claude/dangling": OpUnlink, ".claude/x": OpUnlink}
	if got := f.sync(t, Options{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if got := target(t, filepath.Join(f.claude, "alpha")); got != f.desired["alpha"] {
		t.Errorf("alpha -> %s", got)
	}
}

func TestDirs(t *testing.T) {
	got, err := Dirs([]string{"claude-code", "codex", "cursor"}, "/p", true)
	if err != nil || !reflect.DeepEqual(got, []string{"/p/.claude/skills", "/p/.agents/skills"}) {
		t.Errorf("project dirs: %v %v", got, err)
	}
	got, _ = Dirs([]string{"claude-code", "codex", "pi"}, "/h", false)
	if !reflect.DeepEqual(got, []string{"/h/.claude/skills", "/h/.codex/skills", "/h/.agents/skills"}) {
		t.Errorf("global dirs: %v", got)
	}
	if got, err := Dirs([]string{"crush", "zed"}, "/h", false); err != nil || len(got) != 0 {
		t.Errorf("agents without skills have no directory: %v %v", got, err)
	}
	if _, err := Dirs([]string{"nope"}, "/h", false); err == nil {
		t.Error("expected an error for an unknown agent")
	}
}
