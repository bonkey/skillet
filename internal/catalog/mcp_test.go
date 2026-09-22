package catalog

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func withMCPs() *Catalog {
	c := sample()
	c.MCPs["sosumi"] = &MCP{Type: "remote", URL: "https://sosumi.ai/mcp"}
	c.MCPs["simctl"] = &MCP{Name: "simctl", Type: "local", Command: []string{"npx", "-y", "simctl-mcp"}}
	c.MCPs["tavily"] = &MCP{Type: "remote", URL: "https://mcp.tavily.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${TAVILY_API_KEY}"}}
	c.Packs["acme"].MCPs = []string{"simctl", "sosumi"}
	return c
}

func TestMCPRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	c := withMCPs()
	c.MCPs["tavily"].Enabled = flag(false)
	if err := c.Save(file); err != nil {
		t.Fatal(err)
	}
	got, err := Load(file)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("round trip: %v\n got %+v\nwant %+v", err, got, c)
	}
}

func TestValidateMCP(t *testing.T) {
	bad := map[string]*MCP{
		"neither command nor url": {},
		"odd transport":           {URL: "https://x", Transport: "carrier-pigeon"},
		"command and url":         {Command: []string{"x"}, URL: "https://x"},
	}
	for name, def := range bad {
		if err := def.Validate(name); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	c := withMCPs()
	for name, def := range c.MCPs {
		def.Type = ""
		if err := def.Validate(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if c.MCPs["simctl"].Type != "local" || c.MCPs["tavily"].Type != "remote" {
		t.Error("a command makes a local server, a url a remote one")
	}
}

func TestEnableDisableMCPs(t *testing.T) {
	c := withMCPs()
	var s Set

	if err := c.Enable(&s, "@acme", "mcp:tavily"); err != nil {
		t.Fatal(err)
	}
	if got := c.ResolveMCPs(s); !reflect.DeepEqual(got, []string{"simctl", "sosumi", "tavily"}) {
		t.Fatalf("a pack enables its servers too: %v", got)
	}
	if got := c.Resolve(s); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("skills: %v", got)
	}

	// The links and entries of a scope make a plain set.
	s = Set{Skills: []string{"a", "b"}, MCPs: []string{"simctl", "sosumi", "tavily"}}
	if err := c.Disable(&s, "mcp:simctl"); err != nil || !reflect.DeepEqual(c.ResolveMCPs(s), []string{"sosumi", "tavily"}) {
		t.Fatalf("after disabling simctl: %+v %v", s, err)
	}
	if got := c.Resolve(s); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("a server must not touch skills: %v", got)
	}
	if err := c.Enable(&s, "mcp:simctl"); err != nil || len(s.MCPs) != 3 {
		t.Fatalf("re-enable: %+v %v", s, err)
	}

	if err := c.Disable(&s, "@acme"); err != nil {
		t.Fatal(err)
	}
	if got := c.ResolveMCPs(s); !reflect.DeepEqual(got, []string{"tavily"}) || len(s.Skills) != 0 {
		t.Fatalf("disabling a pack switches off its servers and skills: %+v -> %v", s, got)
	}
	if err := c.Enable(&s, "mcp:ghost"); err == nil {
		t.Error("an unknown server should be rejected")
	}
}

func TestPacksHoldServers(t *testing.T) {
	c := withMCPs()
	if err := c.PackAdd("mixed", []string{"a", "mcp:tavily"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Packs["mixed"]; !reflect.DeepEqual(got.MCPs, []string{"tavily"}) || !reflect.DeepEqual(got.Skills, []string{"a@skills", "b", "pr"}) {
		t.Fatalf("after add: %+v", got)
	}
	if err := c.PackAdd("mixed", []string{"mcp:ghost"}); err == nil {
		t.Error("an unknown server should be rejected")
	}
}

func TestMergeIncludesServers(t *testing.T) {
	local := withMCPs()
	inc := New()
	inc.MCPs["tavily"] = &MCP{Type: "remote", URL: "https://elsewhere"}
	inc.MCPs["exa"] = &MCP{Type: "remote", URL: "https://mcp.exa.ai/mcp"}
	inc.Packs["research"] = &Pack{Description: "Research", MCPs: []string{"exa", "tavily"}}

	merged, err := Merge(local, []string{"g1"}, []*Catalog{inc})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(merged.MCPs["tavily"].URL, "tavily.com") || merged.MCPOrigin["exa"] != "g1" || merged.MCPOrigin["tavily"] != "" {
		t.Errorf("the local definition wins: %+v %v", merged.MCPs["tavily"], merged.MCPOrigin)
	}
	all := func(string) bool { return true }
	if got := merged.Declared(all).MCPs; !reflect.DeepEqual(got, []string{"exa", "simctl", "sosumi", "tavily"}) {
		t.Errorf("included servers are on by default: %v", got)
	}
	inc.Packs["research"].Enabled = flag(false)
	merged, _ = Merge(local, []string{"g1"}, []*Catalog{inc})
	if got := merged.Declared(all).MCPs; !reflect.DeepEqual(got, []string{"simctl", "sosumi"}) {
		t.Errorf("a pack switched off takes its servers with it, wherever they are defined: %v", got)
	}
	if len(local.MCPs) != 3 {
		t.Error("merging must not change the local catalog")
	}
}
