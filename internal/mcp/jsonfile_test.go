package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

const zedSettings = `// Zed settings
{
  "theme": "One Dark", // keep me
  "context_servers": {
    "existing": {
      "command": "npx",
      "args": ["-y", "x"]
    }
  },
  "vim_mode": true
}
`

var sampleEntry = Entry{{"command", "uvx"}, {"args", []string{"a", "b"}}, {"env", map[string]string{"K": "v"}}}

func edit(t *testing.T, src string, fn func(f *jsonFile)) string {
	t.Helper()
	f, err := parseJSONFile([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	fn(f)
	return string(f.bytes())
}

func TestJSONSetKeepsCommentsAndFormatting(t *testing.T) {
	node := []string{"context_servers"}
	got := edit(t, zedSettings, func(f *jsonFile) {
		if err := f.set(node, "new-one", sampleEntry); err != nil {
			t.Fatal(err)
		}
	})
	want := strings.Replace(zedSettings, `      "args": ["-y", "x"]
    }
`, `      "args": ["-y", "x"]
    },
    "new-one": {
      "command": "uvx",
      "args": [
        "a",
        "b"
      ],
      "env": {
        "K": "v"
      }
    }
`, 1)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestJSONSetReplaceRemoveRoundTrip(t *testing.T) {
	node := []string{"context_servers"}
	got := edit(t, zedSettings, func(f *jsonFile) {
		f.set(node, "new-one", sampleEntry)
		f.set(node, "new-one", Entry{{"url", "https://x"}})
		value, ok, err := f.get(node, "new-one")
		if err != nil || !ok || !reflect.DeepEqual(value, Entry{{"url", "https://x"}}.plain()) {
			t.Errorf("get after replace: %v %v %v", value, ok, err)
		}
		names, _ := f.names(node)
		if !reflect.DeepEqual(names, []string{"existing", "new-one"}) {
			t.Errorf("names: %v", names)
		}
		f.remove(node, "new-one")
		f.remove(node, "not-there")
	})
	if got != zedSettings {
		t.Errorf("adding and removing an entry must restore the file:\n%s", got)
	}
}

func TestJSONCreatesMissingNodesInStrictJSON(t *testing.T) {
	for _, src := range []string{"", "{}", "{\n  \"a\": 1\n}\n", "{\n\t\"a\": 1,\n}\n"} {
		got := edit(t, src, func(f *jsonFile) {
			if err := f.set([]string{"mcp", "servers"}, "x", Entry{{"url", "https://x"}}); err != nil {
				t.Fatal(err)
			}
		})
		var decoded map[string]any
		standard, err := hujson.Standardize([]byte(got))
		if err != nil || json.Unmarshal(standard, &decoded) != nil {
			t.Fatalf("%q produced invalid JSON:\n%s", src, got)
		}
		url := decoded["mcp"].(map[string]any)["servers"].(map[string]any)["x"].(map[string]any)["url"]
		if url != "https://x" {
			t.Errorf("%q: %v", src, decoded)
		}
		if !strings.Contains(src, ",\n}") && json.Unmarshal([]byte(got), &decoded) != nil {
			t.Errorf("strict JSON in, strict JSON out:\n%s", got)
		}
	}
	if _, err := parseJSONFile([]byte(`[1]`)); err == nil {
		t.Error("a non-object top level should be rejected")
	}
	f, _ := parseJSONFile([]byte(`{"mcp": 1}`))
	if err := f.set([]string{"mcp"}, "x", sampleEntry); err == nil {
		t.Error("a node that is not an object should be rejected")
	}
}

func TestJSONRemoveLastKeepsValidity(t *testing.T) {
	src := "{\n  \"mcpServers\": {\n    \"a\": {\"url\": \"1\"},\n    \"b\": {\"url\": \"2\"}\n  }\n}\n"
	got := edit(t, src, func(f *jsonFile) { f.remove([]string{"mcpServers"}, "b") })
	var decoded any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("invalid after removing the last entry: %v\n%s", err, got)
	}
}

func TestJSONKeepsURLsReadable(t *testing.T) {
	got := edit(t, "{}", func(f *jsonFile) {
		f.set([]string{"mcpServers"}, "x", Entry{{"url", "https://x/mcp?a=1&b=<2>"}})
	})
	if !strings.Contains(got, `"url": "https://x/mcp?a=1&b=<2>"`) {
		t.Errorf("got:\n%s", got)
	}
}
