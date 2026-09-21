package link

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fixture struct {
	repos, canonical, claude string
	desired                  map[string]string
}

func setup(t *testing.T) fixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	f := fixture{
		repos:     filepath.Join(home, ".local", "share", "skillet", "repos"),
		canonical: Canonical(home),
		claude:    filepath.Join(home, ".claude", "skills"),
		desired:   map[string]string{},
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		target := filepath.Join(f.repos, "acme", "skills", "skills", name)
		mkdir(t, target)
		f.desired[name] = target
	}
	mkdir(t, f.canonical)
	mkdir(t, f.claude)
	return f
}

// sync returns the operations keyed by ".agents/<name>" or ".claude/<name>".
func (f fixture) sync(t *testing.T, opt Options) map[string]string {
	t.Helper()
	opt.ReposDir = f.repos
	actions, err := Sync(f.canonical, []string{f.claude}, f.desired, opt)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range actions {
		out[filepath.Base(filepath.Dir(a.Dir))+"/"+a.Name] = a.Op
	}
	return out
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

func TestSyncBuildsTwoLevels(t *testing.T) {
	f := setup(t)
	want := map[string]string{}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		want[".agents/"+name], want[".claude/"+name] = OpLink, OpLink
	}
	if got := f.sync(t, Options{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("first sync: %v", got)
	}
	if got := target(t, filepath.Join(f.canonical, "alpha")); got != f.desired["alpha"] {
		t.Errorf("canonical alpha -> %s", got)
	}
	if got := target(t, filepath.Join(f.claude, "alpha")); got != "../../.agents/skills/alpha" {
		t.Errorf("claude alpha -> %s", got)
	}
	if _, err := os.Stat(filepath.Join(f.claude, "alpha")); err != nil {
		t.Errorf("the chain does not resolve: %v", err)
	}

	if got := f.sync(t, Options{}); len(got) != 0 {
		t.Errorf("second sync should be a no-op: %v", got)
	}

	delete(f.desired, "beta")
	f.desired["gamma"] = f.desired["alpha"]
	want = map[string]string{".agents/beta": OpUnlink, ".claude/beta": OpUnlink, ".agents/gamma": OpRelink}
	if got := f.sync(t, Options{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("third sync: %v", got)
	}
	for _, dir := range []string{f.canonical, f.claude} {
		if _, err := os.Lstat(filepath.Join(dir, "beta")); !os.IsNotExist(err) {
			t.Errorf("beta should be gone from %s", dir)
		}
	}
}

func TestSyncLeavesUnmanagedEntriesAlone(t *testing.T) {
	f := setup(t)
	elsewhere := filepath.Join(t.TempDir(), "app", "skill")
	mkdir(t, elsewhere)
	// An unmanaged folder where a skill goes, one that is not desired, and their agent links.
	for _, name := range []string{"alpha", "theirs"} {
		mkdir(t, filepath.Join(f.canonical, name))
		symlink(t, "../../.agents/skills/"+name, filepath.Join(f.claude, name))
	}
	symlink(t, elsewhere, filepath.Join(f.claude, "beta")) // foreign link where a skill goes
	mkdir(t, filepath.Join(f.claude, "own-dir"))
	symlink(t, "../../.agents/skills/dangling", filepath.Join(f.claude, "dangling"))

	want := map[string]string{
		".agents/alpha": OpConflict,
		".agents/beta":  OpLink, ".claude/beta": OpConflict,
		".agents/gamma": OpLink, ".claude/gamma": OpLink,
		".claude/dangling": OpUnlink,
	}
	if got := f.sync(t, Options{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	for _, dir := range []string{filepath.Join(f.canonical, "alpha"), filepath.Join(f.canonical, "theirs"), filepath.Join(f.claude, "own-dir")} {
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s was touched", dir)
		}
	}
	if got := target(t, filepath.Join(f.claude, "beta")); got != elsewhere {
		t.Errorf("foreign link was touched: %s", got)
	}
	for _, name := range []string{"alpha", "theirs"} {
		if got := target(t, filepath.Join(f.claude, name)); got != "../../.agents/skills/"+name {
			t.Errorf("agent link %s was touched: %s", name, got)
		}
	}
}

func TestSyncReplacesUnmanagedFoldersWhenAllowed(t *testing.T) {
	f := setup(t)
	for _, name := range []string{"alpha", "beta"} {
		mkdir(t, filepath.Join(f.canonical, name, "old-content"))
		symlink(t, "../../.agents/skills/"+name, filepath.Join(f.claude, name))
	}
	opt := Options{Replace: func(name string) bool { return name == "alpha" }}

	opt.DryRun = true
	dry := f.sync(t, opt)
	if _, err := os.Stat(filepath.Join(f.canonical, "alpha", "old-content")); err != nil {
		t.Fatal("dry run deleted a folder")
	}
	opt.DryRun = false
	got := f.sync(t, opt)
	want := map[string]string{
		".agents/alpha": OpReplace, // its agent link already points at the right place
		".agents/beta":  OpConflict,
		".agents/gamma": OpLink, ".claude/gamma": OpLink,
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(dry, want) {
		t.Fatalf("got %v\ndry %v", got, dry)
	}
	if got := target(t, filepath.Join(f.canonical, "alpha")); got != f.desired["alpha"] {
		t.Errorf("alpha -> %s", got)
	}
	if _, err := os.Stat(filepath.Join(f.canonical, "beta", "old-content")); err != nil {
		t.Error("beta must stay: replacing it was not allowed")
	}
}

func TestSyncReplacesConflictsInAgentDirsWhenAllowed(t *testing.T) {
	f := setup(t)
	elsewhere := filepath.Join(t.TempDir(), "old-store", "alpha")
	mkdir(t, elsewhere)
	symlink(t, elsewhere, filepath.Join(f.claude, "alpha"))      // foreign link where a skill goes
	mkdir(t, filepath.Join(f.claude, "beta", "content"))         // real folder where a skill goes
	symlink(t, elsewhere, filepath.Join(f.claude, "not-wanted")) // foreign link no skill needs
	force := Options{Replace: func(string) bool { return true }}

	if got := f.sync(t, Options{}); got[".claude/alpha"] != OpConflict || got[".claude/beta"] != OpConflict {
		t.Fatalf("without force: %v", got)
	}
	got := f.sync(t, force)
	if got[".claude/alpha"] != OpReplace || got[".claude/beta"] != OpReplace || len(got) != 2 {
		t.Fatalf("with force: %v", got)
	}
	for _, name := range []string{"alpha", "beta"} {
		if got := target(t, filepath.Join(f.claude, name)); got != "../../.agents/skills/"+name {
			t.Errorf("%s -> %s", name, got)
		}
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Error("replacing a link must not delete what it pointed at")
	}
	if got := target(t, filepath.Join(f.claude, "not-wanted")); got != elsewhere {
		t.Errorf("an entry no skill needs must stay, even with force: %s", got)
	}
}

func TestSyncSkipsAnAgentDirThatIsTheCanonicalDir(t *testing.T) {
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
	if got := target(t, filepath.Join(f.canonical, "alpha")); got != f.desired["alpha"] {
		t.Errorf("alpha -> %s", got)
	}
	if _, err := os.Stat(filepath.Join(f.claude, "alpha")); err != nil {
		t.Errorf("the skill must be reachable through the symlinked directory: %v", err)
	}
}

func TestSyncMigratesDirectAgentLinks(t *testing.T) {
	f := setup(t)
	symlink(t, f.desired["alpha"], filepath.Join(f.claude, "alpha")) // straight into the clone
	symlink(t, filepath.Join(f.repos, "gone", "x"), filepath.Join(f.claude, "x"))
	got := f.sync(t, Options{})
	if got[".claude/alpha"] != OpRelink || got[".claude/x"] != OpUnlink {
		t.Fatalf("got %v", got)
	}
}

func TestDirs(t *testing.T) {
	got, err := Dirs([]string{"claude-code", "codex", "cursor"}, "/p", true)
	if err != nil || !reflect.DeepEqual(got, []string{"/p/.claude/skills"}) {
		t.Errorf("project dirs: %v %v", got, err)
	}
	got, _ = Dirs([]string{"claude-code", "codex"}, "/h", false)
	if !reflect.DeepEqual(got, []string{"/h/.claude/skills", "/h/.codex/skills"}) {
		t.Errorf("global dirs: %v", got)
	}
	for _, project := range []bool{false, true} {
		if got, err := Dirs([]string{"pi"}, "/h", project); err != nil || len(got) != 0 {
			t.Errorf("pi reads the canonical directory itself and needs no links of its own: %v %v", got, err)
		}
	}
	if _, err := Dirs([]string{"nope"}, "/h", false); err == nil {
		t.Error("expected an error for an unknown agent")
	}
}
