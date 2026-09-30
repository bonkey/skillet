package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeGitHub makes git read git@github.com:acme/<repo> from the test's
// remotes and fail on https://github.com/ without going to the network.
func fakeGitHub(t *testing.T, e env) {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	write(t, global, "[url \""+filepath.Dir(e.origin)+"/\"]\n\tinsteadOf = git@github.com:acme/\n"+
		"[url \""+filepath.Join(t.TempDir(), "no-https")+"/\"]\n\tinsteadOf = https://github.com/\n")
	if err := os.Symlink(e.origin, e.origin+".git"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func originOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestSSHSourceURL(t *testing.T) {
	e := setup(t)
	fakeGitHub(t, e)
	report, err := e.app.Add(e.app.Global(), AddRequest{Arg: "git@github.com:acme/skills.git"})
	if err != nil || !reflect.DeepEqual(report.Added, []string{"skills:skills"}) {
		t.Fatalf("add: %+v %v", report, err)
	}
	if got := read(t, e.p.ConfigFile()); !strings.Contains(got, "url = 'git@github.com:acme/skills.git'") {
		t.Fatalf("config:\n%s", got)
	}
	e.open(t, read(t, e.p.ConfigFile())+"\n[[packs]]\nname = 'acme'\ndescription = 'A'\nskills = ['skills']\n")
	if !isLink(filepath.Join(e.p.Home, ".claude", "skills", "alpha")) {
		t.Fatal("sync links the skills of the SSH source")
	}
	updates, err := e.app.Update(false)
	if err != nil || len(updates) != 1 || updates[0].Err != nil {
		t.Fatalf("update: %+v %v", updates, err)
	}
}

func TestGitProtocolClonesGitHubOverSSH(t *testing.T) {
	e := setup(t)
	fakeGitHub(t, e)
	write(t, e.p.LocalConfigFile(), "git_protocol = 'ssh'\n")
	e.open(t, "")
	report, err := e.app.Add(e.app.Global(), AddRequest{Arg: "acme/skills"})
	if err != nil || !reflect.DeepEqual(report.Added, []string{"skills:skills"}) {
		t.Fatalf("add: %+v %v", report, err)
	}
	if got := read(t, e.p.ConfigFile()); !strings.Contains(got, "url = 'https://github.com/acme/skills.git'") {
		t.Fatalf("the config keeps the URL as written:\n%s", got)
	}
	write(t, e.p.ConfigFile(), read(t, e.p.ConfigFile())+"\n[[packs]]\nname = 'acme'\ndescription = 'A'\nskills = ['skills']\n")
	e.app, err = OpenWith(e.p, e.app.Gists)
	if err != nil {
		t.Fatal(err)
	}
	if synced, err := e.app.Sync(e.app.Global(), SyncOptions{}); err != nil || synced.Notes != nil ||
		!isLink(filepath.Join(e.p.Home, ".claude", "skills", "alpha")) {
		t.Fatalf("sync clones over SSH and links the skills: %v %v", synced.Notes, err)
	}
	if got := originOf(t, e.p.RepoDir("skills")); got != "git@github.com:acme/skills.git" {
		t.Errorf("origin: %s", got)
	}

	// Back to https: update points the clone at the https URL.
	write(t, e.p.LocalConfigFile(), "git_protocol = 'https'\n")
	e.open(t, read(t, e.p.ConfigFile()))
	updates, _ := e.app.Update(true)
	if len(updates) != 1 || updates[0].Err == nil {
		t.Fatalf("the fake https remote fails: %+v", updates)
	}
	if got := originOf(t, e.p.RepoDir("skills")); got != "https://github.com/acme/skills.git" {
		t.Errorf("origin: %s", got)
	}
}

func TestAFailedCloneIsReportedOnce(t *testing.T) {
	e := setup(t)
	fakeGitHub(t, e)
	write(t, e.p.ConfigFile(), "[[skills]]\nurl = 'https://github.com/acme/skills.git'\n")
	a, err := OpenWith(e.p, e.app.Gists)
	if err != nil {
		t.Fatal(err)
	}
	e.app = a
	global, err := a.Sync(a.Global(), SyncOptions{})
	if notes := global.Notes; err != nil || len(notes) != 1 || !strings.Contains(notes[0], "skills could not be cloned") {
		t.Fatalf("notes: %v %v", notes, err)
	}
	project, err := e.app.ProjectScope()
	if err != nil {
		t.Fatal(err)
	}
	report, err := e.app.Sync(project, SyncOptions{})
	if err != nil || len(report.Notes) != 0 {
		t.Errorf("the second scope does not report the failure again: %v %v", report.Notes, err)
	}
}
