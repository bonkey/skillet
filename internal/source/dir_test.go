package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCloneOfAPlainDirectory(t *testing.T) {
	src := filepath.Join(t.TempDir(), "helm-asc")
	write(t, filepath.Join(src, "SKILL.md"), "---\nname: helm-asc\ndescription: Drives Helm\n---\n")
	write(t, filepath.Join(src, "references", "cli.md"), "v1\n")
	if !IsLocalDir(src) || IsLocalDir(filepath.Join(src, "SKILL.md")) || IsLocalDir("https://example.com/x") {
		t.Fatal("IsLocalDir tells a local directory from a file and a URL")
	}
	// An app bundle's folder is not ours to write to.
	if err := os.Chmod(src, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(src, 0o755) })

	dir := filepath.Join(t.TempDir(), "helm-asc")
	if err := Clone(src, "", dir); err != nil {
		t.Fatal(err)
	}
	skills, err := Discover(dir, "")
	if err != nil || skills["helm-asc"].Path != "." || skills["helm-asc"].Description != "Drives Helm" {
		t.Fatalf("the directory is one skill: %+v %v", skills, err)
	}
	head, _ := Head(dir)

	same, err := Fetch(dir, "")
	if err != nil || same != head {
		t.Fatalf("an unchanged directory is the same commit: %s %s %v", same, head, err)
	}

	write(t, filepath.Join(src, "references", "cli.md"), "v2\n")
	latest, err := Fetch(dir, "")
	if err != nil || latest == head {
		t.Fatalf("a changed directory is a new commit: %v", err)
	}
	if changed, err := Changed(dir, head, latest, "references"); err != nil || !changed {
		t.Fatalf("the file changed: %v %v", changed, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "references", "cli.md")); string(data) != "v1\n" {
		t.Fatalf("fetch must not touch the work tree: %q", data)
	}
	if err := Checkout(dir, latest); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "references", "cli.md")); string(data) != "v2\n" {
		t.Fatalf("checked out: %q", data)
	}
}
