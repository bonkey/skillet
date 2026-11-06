package mcp

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// tomlFile edits the server entries of a TOML config as text. It owns whole
// sections, sub-tables included, and leaves every other line untouched. The
// entries are [table.<name>] sections, or, when array is set, elements of a
// [[table]] array of tables that each name themselves with a name key.
type tomlFile struct {
	lines []string
	table string // "mcp_servers"
	array bool   // entries are elements of a [[mcp_servers]] array
}

var (
	anyHeader = regexp.MustCompile(`^\s*\[`)
	bareKey   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	dottedKey = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)+$`)
)

func parseTOMLFile(data []byte, table string, array bool) (*tomlFile, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	f := &tomlFile{table: table, array: array}
	if text != "" {
		f.lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	quoted := regexp.QuoteMeta(table)
	inline := regexp.MustCompile(`^\s*` + quoted + `\s*[.=]`)
	tableHeader := regexp.MustCompile(`^\s*\[` + quoted + `\s*\]`)
	arrayHeader := regexp.MustCompile(`^\s*\[\[\s*` + quoted + `\s*\]\]`)
	for _, line := range f.lines {
		switch {
		case array && (inline.MatchString(line) || tableHeader.MatchString(line)):
			return nil, fmt.Errorf("%s is defined as a table; only [[%s]] arrays can be managed", table, table)
		case !array && inline.MatchString(line):
			return nil, fmt.Errorf("%s is defined inline; only [%s.<name>] sections can be managed", table, table)
		case !array && arrayHeader.MatchString(line):
			return nil, fmt.Errorf("%s is an array of tables; only [%s.<name>] sections can be managed", table, table)
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

// trimComment drops a trailing comment that starts outside a string.
func trimComment(line string) string {
	if i := strings.Index(line, "#"); i >= 0 && !strings.Contains(line[:i], `"`) {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

// headerPath returns the dotted path of a table header line, and whether it
// opens an array of tables.
func headerPath(line string) (path string, array, ok bool) {
	line = trimComment(line)
	switch {
	case strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]"):
		return strings.TrimSpace(line[2 : len(line)-2]), true, true
	case len(line) > 2 && strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
		return strings.TrimSpace(line[1 : len(line)-1]), false, true
	}
	return "", false, false
}

// owner returns the entry name when a line is the header of one of the
// entry's tables.
func (f *tomlFile) owner(line string) (string, bool) {
	path, array, ok := headerPath(line)
	if !ok || array {
		return "", false
	}
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

type tomlElement struct {
	name     string
	from, to int // the half-open line range of the table and its sub-tables
}

// elements lists the [[table]] tables of the file, each named by the name key
// among its own lines. A table without one keeps the empty name, which no
// entry asks for and no edit touches.
func (f *tomlFile) elements() []tomlElement {
	var out []tomlElement
	for i := 0; i < len(f.lines); i++ {
		if path, array, ok := headerPath(f.lines[i]); !ok || !array || path != f.table {
			continue
		}
		to := i + 1
		for to < len(f.lines) {
			if path, _, header := headerPath(f.lines[to]); header && !strings.HasPrefix(path, f.table+".") {
				break // the next element, or the next table of the file
			}
			to++
		}
		end := to
		for end > i+1 && strings.TrimSpace(f.lines[end-1]) == "" {
			end--
		}
		var name string
		for j := i + 1; j < end; j++ {
			if _, _, header := headerPath(f.lines[j]); header {
				break // keys of a sub-table do not name the element
			}
			key, value, isKey := strings.Cut(trimComment(f.lines[j]), "=")
			if !isKey || strings.TrimSpace(key) != "name" {
				continue
			}
			if unquoted, err := strconv.Unquote(strings.TrimSpace(value)); err == nil {
				name = unquoted
				break
			}
		}
		out = append(out, tomlElement{name: name, from: i, to: end})
		i = to - 1
	}
	return out
}

// spans lists the line ranges [from, to) of an entry's tables.
func (f *tomlFile) spans(name string) [][2]int {
	if f.array {
		var spans [][2]int
		for _, el := range f.elements() {
			if el.name != "" && el.name == name {
				spans = append(spans, [2]int{el.from, el.to})
			}
		}
		return spans
	}
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
	if f.array {
		for _, el := range f.elements() {
			if el.name != "" {
				seen[el.name] = true
			}
		}
	} else {
		for _, line := range f.lines {
			if name, ok := f.owner(line); ok {
				seen[name] = true
			}
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

// render spells an entry as TOML: as one [[table]] element that names itself
// and holds each map field as an inline table, or as a [table.<name>] section
// with scalar and list fields in the main table and each map field in a
// sub-table.
func (f *tomlFile) render(name string, entry Entry) string {
	if f.array {
		lines := []string{"[[" + f.table + "]]", "name = " + tomlString(name)}
		for _, field := range entry {
			lines = append(lines, entryKey(field.Key)+" = "+tomlValue(field.Value))
		}
		return strings.Join(lines, "\n")
	}
	header := f.table + "." + tomlKey(name)
	var main, tables []string
	for _, field := range entry {
		if value, ok := field.Value.(map[string]string); ok {
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
			continue
		}
		main = append(main, tomlKey(field.Key)+" = "+tomlValue(field.Value))
	}
	return strings.Join(append(append([]string{"[" + header + "]"}, main...), tables...), "\n")
}

// tomlValue spells one field value: maps as inline tables, lists as inline
// arrays, strings quoted, and everything else the way fmt prints it.
func tomlValue(v any) string {
	switch value := v.(type) {
	case map[string]string:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, tomlKey(key)+" = "+tomlString(value[key]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case []string:
		items := make([]string, len(value))
		for i, item := range value {
			items[i] = tomlString(item)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case string:
		return tomlString(value)
	default:
		return fmt.Sprintf("%v", value)
	}
}

func tomlKey(key string) string {
	if bareKey.MatchString(key) {
		return key
	}
	return tomlString(key)
}

// entryKey spells a field key of an array element; a dotted one addresses
// the entry's sub-table, as in auth.headers.
func entryKey(key string) string {
	if bareKey.MatchString(key) || dottedKey.MatchString(key) {
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
