package secrets

import (
	"os"
	"path/filepath"
	"reflect"
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
