package catalog

import (
	"reflect"
	"testing"
)

func TestSkillReferencesNameTheirSource(t *testing.T) {
	c := sample()
	var s Set

	// Bare names are accepted and recorded with their source.
	if err := c.Enable(&s, "pr", "a@skills"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Skills, []string{"a@skills", "pr@own"}) {
		t.Fatalf("recorded references: %v", s.Skills)
	}
	if got := c.Resolve(s); !reflect.DeepEqual(got, []string{"a", "pr"}) {
		t.Fatalf("resolved: %v", got)
	}
	if err := c.Enable(&s, "pr@skills"); err == nil {
		t.Error("a reference to the wrong source should be rejected")
	}

	// A reference stops resolving when the skill comes from elsewhere.
	moved := Set{Skills: []string{"pr@somewhere/else", "b"}}
	if got := c.Resolve(moved); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("a reference to another source must not resolve: %v", got)
	}

	// Disabling and enabling work on either spelling.
	if err := c.Disable(&s, "pr@own"); err != nil || !reflect.DeepEqual(s.Skills, []string{"a@skills"}) {
		t.Fatalf("after disabling pr: %v %v", s.Skills, err)
	}
	if err := c.Enable(&s, "b@skills"); err != nil || !reflect.DeepEqual(s.Skills, []string{"a@skills", "b@skills"}) {
		t.Fatalf("enable by reference: %v %v", s.Skills, err)
	}

	// Packs record references too, and find their members by either spelling.
	if err := c.PackAdd("acme", []string{"c"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Packs["acme"].Skills; !reflect.DeepEqual(got, []string{"a", "b", "c@skills"}) {
		t.Errorf("pack entries: %v", got)
	}
	if got := c.PackSkills("acme"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("pack skills are plain names: %v", got)
	}
}
