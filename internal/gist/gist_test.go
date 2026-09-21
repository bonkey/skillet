package gist

import "testing"

func TestID(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	for _, ref := range []string{
		id,
		"https://gist.github.com/someone/" + id,
		"https://gist.github.com/" + id + "/",
		"https://gist.github.com/someone/" + id + "#file-skillet-yaml",
		"https://gist.github.com/" + id + ".git",
		"  " + id + "\n",
	} {
		if got, err := ID(ref); err != nil || got != id {
			t.Errorf("ID(%q) = %q, %v", ref, got, err)
		}
	}
	for _, ref := range []string{"", "owner/repo", "https://example.com/not-a-gist"} {
		if _, err := ID(ref); err == nil {
			t.Errorf("ID(%q) should fail", ref)
		}
	}
}
