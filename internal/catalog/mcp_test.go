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
	c.MCPs["simctl"] = &MCP{Type: "local", Command: []string{"npx", "-y", "simctl-mcp"}}
	c.MCPs["tavily"] = &MCP{Type: "remote", URL: "https://mcp.tavily.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${TAVILY_API_KEY}"}}
	c.Packs["acme"].MCPs = []string{"simctl", "sosumi"}
	return c
}

func TestMCPRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	c := withMCPs()
	c.Enabled = Set{Packs: []string{"acme"}, MCPs: []string{"tavily"}, Except: []string{"mcp:simctl"}}
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
		"no type":          {},
		"local no command": {Type: "local"},
		"remote no url":    {Type: "remote"},
		"odd transport":    {Type: "remote", URL: "https://x", Transport: "carrier-pigeon"},
		"local with url":   {Type: "local", Command: []string{"x"}, URL: "https://x"},
	}
	for name, def := range bad {
		if err := def.Validate(name); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	for name, def := range withMCPs().MCPs {
		if err := def.Validate(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
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

	if err := c.Disable(&s, "mcp:simctl"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Except, []string{"mcp:simctl"}) || !reflect.DeepEqual(c.ResolveMCPs(s), []string{"sosumi", "tavily"}) {
		t.Fatalf("a pack's server is switched off with an exception: %+v", s)
	}
	if got := c.Resolve(s); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("a server exception must not touch skills: %v", got)
	}
	if err := c.Enable(&s, "mcp:simctl"); err != nil || len(s.Except) != 0 || len(s.MCPs) != 1 {
		t.Fatalf("re-enable drops the exception: %+v %v", s, err)
	}

	if err := c.Disable(&s, "@acme"); err != nil {
		t.Fatal(err)
	}
	if got := c.ResolveMCPs(s); !reflect.DeepEqual(got, []string{"tavily"}) || len(s.Except) != 0 {
		t.Fatalf("after disabling the pack: %+v -> %v", s, got)
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
	if got := c.Packs["mixed"]; !reflect.DeepEqual(got.MCPs, []string{"tavily"}) || !reflect.DeepEqual(got.Skills, []string{"a@acme/skills", "b", "pr"}) {
		t.Fatalf("after add: %+v", got)
	}
	if err := c.PackAdd("mixed", []string{"mcp:ghost"}); err == nil {
		t.Error("an unknown server should be rejected")
	}
	if err := c.PackRemove("mixed", []string{"mcp:tavily", "a"}); err != nil || len(c.Packs["mixed"].MCPs) != 0 {
		t.Fatalf("after remove: %+v %v", c.Packs["mixed"], err)
	}

	c.Enabled = Set{MCPs: []string{"simctl"}, Except: []string{"mcp:simctl"}}
	c.RemoveMCP("simctl")
	if _, ok := c.MCPs["simctl"]; ok || len(c.Enabled.MCPs)+len(c.Enabled.Except) != 0 || !reflect.DeepEqual(c.Packs["acme"].MCPs, []string{"sosumi"}) {
		t.Errorf("RemoveMCP must clean everything: %+v %+v", c.Enabled, c.Packs["acme"])
	}
}

func TestMergeIncludesServers(t *testing.T) {
	local := withMCPs()
	inc := New()
	inc.MCPs["tavily"] = &MCP{Type: "remote", URL: "https://elsewhere"}
	inc.MCPs["exa"] = &MCP{Type: "remote", URL: "https://mcp.exa.ai/mcp"}
	inc.Packs["research"] = &Pack{Description: "Research", MCPs: []string{"exa", "tavily"}}
	inc.Enabled = Set{Packs: []string{"research"}, Except: []string{"mcp:tavily"}}

	merged, err := Merge(local, []string{"g1"}, []*Catalog{inc})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(merged.MCPs["tavily"].URL, "tavily.com") || merged.MCPOrigin["exa"] != "g1" || merged.MCPOrigin["tavily"] != "" {
		t.Errorf("the local definition wins: %+v %v", merged.MCPs["tavily"], merged.MCPOrigin)
	}
	if got := merged.ResolveMCPs(merged.Enabled); !reflect.DeepEqual(got, []string{"exa"}) {
		t.Errorf("included gists enable their servers: %v", got)
	}
	if len(local.MCPs) != 3 {
		t.Error("merging must not change the local catalog")
	}
}
