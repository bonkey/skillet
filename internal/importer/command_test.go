package importer

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		in   string
		want Command
	}{
		{"npx skills add bonkey/skills -g --skill captains-log -y", Command{Sources: []string{"bonkey/skills"}, Skills: []string{"captains-log"}}},
		{"npx -y skills@latest a bonkey/skills wondelai/skills -s pr review --agent claude-code codex --copy",
			Command{Sources: []string{"bonkey/skills", "wondelai/skills"}, Skills: []string{"pr", "review"}}},
		{"bunx skills install bonkey/skills --all", Command{Sources: []string{"bonkey/skills"}}},
		{"pnpm dlx skills i bonkey/skills --skill * --full-depth", Command{Sources: []string{"bonkey/skills"}}},
		{"skills add ./local --metadata {} --json", Command{Sources: []string{"./local"}}},
		{"/usr/local/bin/npx skills add bonkey/skills --skill a --skill b", Command{Sources: []string{"bonkey/skills"}, Skills: []string{"a", "b"}}},
	}
	for _, tt := range tests {
		got, ok, err := ParseCommand(strings.Fields(tt.in))
		if err != nil || !ok || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseCommand(%q) = %+v, %v, %v; want %+v", tt.in, got, ok, err, tt.want)
		}
	}
}

func TestParseCommandLeavesOtherCommands(t *testing.T) {
	for _, in := range []string{"npx @mobilenext/mobile-mcp@latest", "npx -y skills-mcp", "uvx mcp-server-fetch", "docker run -i --rm mcp/fetch"} {
		if _, ok, err := ParseCommand(strings.Fields(in)); ok || err != nil {
			t.Errorf("ParseCommand(%q) = %v, %v; want a command that is not `skills`", in, ok, err)
		}
	}
}

func TestParseCommandRefuses(t *testing.T) {
	for in, want := range map[string]string{
		"npx skills find react":               "only `skills add`",
		"npx skills add bonkey/skills -l":     "lists",
		"npx skills add bonkey/skills --what": "--what",
		"npx skills add -g":                   "no source",
	} {
		_, ok, err := ParseCommand(strings.Fields(in))
		if !ok || err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseCommand(%q) = %v, %v; want an error with %q", in, ok, err, want)
		}
	}
}
