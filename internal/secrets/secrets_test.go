package secrets

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	file := filepath.Join(t.TempDir(), "secrets.toml")
	store, err := Load(file)
	if err != nil || len(store) != 0 {
		t.Fatalf("a missing file is an empty store: %v %v", store, err)
	}
	os.WriteFile(file, []byte("TAVILY_API_KEY = \"tvly-123: with a colon\"\nOTHER = \"x\"\n"), 0o600)
	store, err = Load(file)
	if err != nil || !reflect.DeepEqual(store, Store{"TAVILY_API_KEY": "tvly-123: with a colon", "OTHER": "x"}) {
		t.Fatalf("got %v, %v", store, err)
	}
	os.WriteFile(file, []byte("not toml at all\n"), 0o600)
	if _, err := Load(file); err == nil {
		t.Error("a malformed file should be rejected")
	}
}

func TestAmendAppendsNewNames(t *testing.T) {
	file := filepath.Join(t.TempDir(), "secrets.toml")
	original := "# keys for the servers\nOLD = \"made-up\"   # rotated in May\n"
	os.WriteFile(file, []byte(original), 0o600)
	store, _ := Load(file)
	store["NEW"] = "also-made-up"
	if err := store.Amend(file); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(file); string(got) != original+"NEW = 'also-made-up'\n" {
		t.Fatalf("got\n%s", got)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode())
	}

	store["OLD"] = "changed"
	if err := store.Amend(file); err == nil || strings.Contains(err.Error(), "changed") {
		t.Fatalf("a changed value is refused, without showing it: %v", err)
	}
	if got, _ := os.ReadFile(file); string(got) != original+"NEW = 'also-made-up'\n" {
		t.Fatalf("the file stays:\n%s", got)
	}

	missing := filepath.Join(t.TempDir(), "secrets.toml")
	if err := (Store{"A": "made-up"}).Amend(missing); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(missing); got["A"] != "made-up" {
		t.Fatalf("a missing file is written whole: %v", got)
	}
}

func TestExpand(t *testing.T) {
	store := Store{"KEY": "abc", "OTHER": "xyz"}
	got, missing := store.Expand("Bearer ${KEY} and ${OTHER}, not $KEY or ${}")
	if got != "Bearer abc and xyz, not $KEY or ${}" || missing != nil {
		t.Errorf("got %q, missing %v", got, missing)
	}
	got, missing = store.Expand("https://x/?k=${KEY}&t=${NOPE}")
	if got != "https://x/?k=abc&t=${NOPE}" || !reflect.DeepEqual(missing, []string{"NOPE"}) {
		t.Errorf("got %q, missing %v", got, missing)
	}
}
