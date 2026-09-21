package secrets

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStoreFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "secrets.yaml")
	store, err := Load(file)
	if err != nil || len(store) != 0 {
		t.Fatalf("a missing file is an empty store: %v %v", store, err)
	}
	if err := store.Set("TAVILY_API_KEY", "tvly-123: with a colon"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("not valid", "x"); err == nil {
		t.Error("expected an error for an invalid name")
	}
	if err := store.Save(file); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	again, err := Load(file)
	if err != nil || !reflect.DeepEqual(again, store) {
		t.Fatalf("round trip: %v %v", again, err)
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
	if got := Placeholders("${A} ${B_2} $C"); !reflect.DeepEqual(got, []string{"A", "B_2"}) {
		t.Errorf("placeholders: %v", got)
	}
}
