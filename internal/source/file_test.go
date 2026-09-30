package source

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// skillServer serves the SKILL.md held in the returned setter at /pkg/SKILL.md.
func skillServer(t *testing.T) (url string, set func(string)) {
	t.Helper()
	var mu sync.Mutex
	body := "---\nname: pen-design\ndescription: Designs things\n---\nv1\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/pkg/SKILL.md" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/pkg/SKILL.md", func(s string) { mu.Lock(); body = s; mu.Unlock() }
}

func TestCloneOfASkillFile(t *testing.T) {
	url, set := skillServer(t)
	dir := filepath.Join(t.TempDir(), "pen-design")
	if err := Clone(url, "", dir); err != nil {
		t.Fatal(err)
	}
	skills, err := Discover(dir, "")
	if err != nil || skills["pen-design"].Path != "pen-design" || skills["pen-design"].Description != "Designs things" {
		t.Fatalf("the file is one skill in a folder of its name: %+v %v", skills, err)
	}
	head, _ := Head(dir)

	same, err := Fetch(dir, "")
	if err != nil || same != head {
		t.Fatalf("an unchanged file is the same commit: %s %s %v", same, head, err)
	}

	set("---\nname: pen-design\ndescription: Designs things\n---\nv2\n")
	latest, err := Fetch(dir, "")
	if err != nil || latest == head {
		t.Fatalf("a changed file is a new commit: %v", err)
	}
	if changed, err := Changed(dir, head, latest, "pen-design"); err != nil || !changed {
		t.Fatalf("the skill changed: %v %v", changed, err)
	}
	if err := Checkout(dir, latest); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "pen-design", "SKILL.md")); !strings.HasSuffix(string(data), "v2\n") {
		t.Fatalf("checked out: %q", data)
	}
}

func TestCloneOfASkillFileRefusesOtherContent(t *testing.T) {
	url, set := skillServer(t)
	set("<html>not found</html>")
	err := Clone(url, "", filepath.Join(t.TempDir(), "x"))
	if err == nil || !strings.Contains(err.Error(), "no SKILL.md") {
		t.Fatalf("err: %v", err)
	}
	err = Clone(strings.Replace(url, "/pkg/", "/other/", 1), "", filepath.Join(t.TempDir(), "x"))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err: %v", err)
	}
}
