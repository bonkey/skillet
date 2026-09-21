package mcp

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// tomlFile edits the [table.<name>] sections of a TOML config as text. It
// owns whole sections, sub-tables included, and leaves every other line
// untouched.
type tomlFile struct {
	lines []string
	table string // "mcp_servers"
}

var (
	anyHeader = regexp.MustCompile(`^\s*\[`)
	bareKey   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

func parseTOMLFile(data []byte, table string) (*tomlFile, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	f := &tomlFile{table: table}
	if text != "" {
		f.lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	inline := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(table) + `\s*[.=]`)
	for _, line := range f.lines {
		if inline.MatchString(line) {
			return nil, fmt.Errorf("%s is defined inline; only [%s.<name>] sections can be managed", table, table)
		}
	}
	return f, nil
}

func (f *tomlFile) bytes() []byte {
	if len(f.lines) == 0 {
		return nil
	}
	return []byte(strings.Join(f.lines, "\n") + "\n")
}

// owner returns the entry name when a line is the header of one of the
// entry's tables.
func (f *tomlFile) owner(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if i := strings.Index(line, "#"); i >= 0 && !strings.Contains(line[:i], `"`) {
		line = strings.TrimSpace(line[:i])
	}
	if !strings.HasPrefix(line, "[") || strings.HasPrefix(line, "[[") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	path := strings.TrimSpace(line[1 : len(line)-1])
	rest, ok := strings.CutPrefix(path, f.table)
	rest = strings.TrimSpace(rest)
	if !ok || !strings.HasPrefix(rest, ".") {
		return "", false
	}
	rest = strings.TrimSpace(rest[1:])
	if strings.HasPrefix(rest, `"`) {
		name, err := strconv.Unquote(rest[:strings.Index(rest[1:], `"`)+2])
		return name, err == nil
	}
	name, _, _ := strings.Cut(rest, ".")
	return strings.TrimSpace(name), name != ""
}

// spans lists the line ranges [from, to) of an entry's tables.
func (f *tomlFile) spans(name string) [][2]int {
	var spans [][2]int
	for i := 0; i < len(f.lines); i++ {
		if owner, ok := f.owner(f.lines[i]); !ok || owner != name {
			continue
		}
		end := i + 1
		for end < len(f.lines) && !anyHeader.MatchString(f.lines[end]) {
			end++
		}
		for end > i+1 && strings.TrimSpace(f.lines[end-1]) == "" {
			end--
		}
		spans = append(spans, [2]int{i, end})
		i = end - 1
	}
	return spans
}

func (f *tomlFile) names() []string {
	seen := map[string]bool{}
	for _, line := range f.lines {
		if name, ok := f.owner(line); ok {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// get returns the text of an entry's tables.
func (f *tomlFile) get(name string) (string, bool) {
	var parts []string
	for _, span := range f.spans(name) {
		parts = append(parts, strings.Join(f.lines[span[0]:span[1]], "\n"))
	}
	return strings.Join(parts, "\n\n"), len(parts) > 0
}

func (f *tomlFile) remove(name string) {
	spans := f.spans(name)
	for i := len(spans) - 1; i >= 0; i-- {
		from, to := spans[i][0], spans[i][1]
		for to < len(f.lines) && strings.TrimSpace(f.lines[to]) == "" {
			to++ // the blank lines that set the section apart
		}
		f.lines = append(f.lines[:from], f.lines[to:]...)
	}
	for len(f.lines) > 0 && strings.TrimSpace(f.lines[len(f.lines)-1]) == "" {
		f.lines = f.lines[:len(f.lines)-1]
	}
}

// set replaces an entry where it stands, or appends it to the file.
func (f *tomlFile) set(name string, entry Entry) {
	text := strings.Split(f.render(name, entry), "\n")
	if spans := f.spans(name); len(spans) > 0 {
		at := spans[0][0]
		before := len(f.lines)
		f.remove(name)
		at = min(at, len(f.lines))
		if removedTail := at == len(f.lines) && before > 0; removedTail && at > 0 {
			text = append([]string{""}, text...)
		} else if at < len(f.lines) {
			text = append(text, "")
		}
		f.lines = append(f.lines[:at], append(text, f.lines[at:]...)...)
		return
	}
	if len(f.lines) > 0 {
		f.lines = append(f.lines, "")
	}
	f.lines = append(f.lines, text...)
}

// render spells an entry as TOML: scalar and list fields in the main table,
// each map field in a sub-table.
func (f *tomlFile) render(name string, entry Entry) string {
	header := f.table + "." + tomlKey(name)
	var main, tables []string
	for _, field := range entry {
		switch value := field.Value.(type) {
		case map[string]string:
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			lines := []string{"", "[" + header + "." + tomlKey(field.Key) + "]"}
			for _, key := range keys {
				lines = append(lines, tomlKey(key)+" = "+tomlString(value[key]))
			}
			tables = append(tables, strings.Join(lines, "\n"))
		case []string:
			items := make([]string, len(value))
			for i, item := range value {
				items[i] = tomlString(item)
			}
			main = append(main, tomlKey(field.Key)+" = ["+strings.Join(items, ", ")+"]")
		case string:
			main = append(main, tomlKey(field.Key)+" = "+tomlString(value))
		default:
			main = append(main, fmt.Sprintf("%s = %v", tomlKey(field.Key), value))
		}
	}
	return strings.Join(append(append([]string{"[" + header + "]"}, main...), tables...), "\n")
}

func tomlKey(key string) string {
	if bareKey.MatchString(key) {
		return key
	}
	return tomlString(key)
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
