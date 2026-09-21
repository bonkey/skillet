package catalog

import (
	"reflect"
	"testing"
)

func TestSkillReferencesNameTheirSource(t *testing.T) {
	c := sample()
	var s Set

	// Bare names are accepted and recorded with their source.
	if err := c.Enable(&s, "pr", "a@acme/skills"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Skills, []string{"a@acme/skills", "pr@me/own"}) {
		t.Fatalf("recorded references: %v", s.Skills)
	}
	if got := c.Resolve(s); !reflect.DeepEqual(got, []string{"a", "pr"}) {
		t.Fatalf("resolved: %v", got)
	}
	if err := c.Enable(&s, "pr@acme/skills"); err == nil {
		t.Error("a reference to the wrong source should be rejected")
	}

	// A reference stops resolving when the skill comes from elsewhere.
	moved := Set{Skills: []string{"pr@somewhere/else", "b"}}
	if got := c.Resolve(moved); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("a reference to another source must not resolve: %v", got)
	}

	// Disabling works on either spelling, and exceptions are recorded with the source.
	c.Enable(&s, "@acme")
	if err := c.Disable(&s, "b"); err != nil || !reflect.DeepEqual(s.Except, []string{"b@acme/skills"}) {
		t.Fatalf("except: %v %v", s.Except, err)
	}
	if err := c.Disable(&s, "pr@me/own"); err != nil || len(s.Skills) != 1 {
		t.Fatalf("after disabling pr: %v %v", s.Skills, err)
	}
	if err := c.Enable(&s, "b@acme/skills"); err != nil || len(s.Except) != 0 {
		t.Fatalf("re-enable by reference: %v %v", s.Except, err)
	}

	// Packs record references too, and find their members by either spelling.
	if err := c.PackAdd("acme", []string{"c"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Packs["acme"].Skills; !reflect.DeepEqual(got, []string{"a", "b", "c@acme/skills"}) {
		t.Errorf("pack entries: %v", got)
	}
	if got := c.PackSkills("acme"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("pack skills are plain names: %v", got)
	}
}
