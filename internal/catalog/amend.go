package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Amend saves the catalog into file and keeps the file's text: sources and
// servers the file lacks are appended, those c lacks are cut out (see cut),
// and a source's changed `url` and `only` list are replaced where they
// stand, so comments and layout stay. A file that does not exist yet is
// written whole. The result goes to a temporary file first
// and replaces file only when it reads back as c; a change of anything else
// is refused and leaves file as it is.
func (c *Catalog) Amend(file string) error {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return c.Save(file)
	}
	if err != nil {
		return err
	}
	out, err := c.amend(data)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	if !c.readsBack(tmp) {
		os.Remove(tmp)
		return fmt.Errorf("%s: the change cannot be written without rewriting the file; it is left as it is", file)
	}
	return os.Rename(tmp, file)
}

func (c *Catalog) readsBack(file string) bool {
	got, err := Load(file)
	if err != nil {
		return false
	}
	have, err := got.Encode()
	if err != nil {
		return false
	}
	want, err := c.Encode()
	return err == nil && bytes.Equal(have, want)
}

// amend returns data with the changes of c applied as text.
func (c *Catalog) amend(data []byte) ([]byte, error) {
	var f fileCatalog
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	sources := make([]*Source, len(f.Skills))
	for i, entry := range f.Skills {
		src, err := entry.source()
		if err != nil {
			return nil, err
		}
		sources[i] = src
	}
	names, err := sourceNames(sources)
	if err != nil {
		return nil, err
	}
	ranges, servers, err := entryRanges(data)
	if err != nil {
		return nil, err
	}
	if len(ranges) != len(sources) {
		return nil, errors.New("sources written other than as [[skills]] entries cannot be amended")
	}

	var edits []edit
	for i := range sources {
		want, ok := c.Sources[names[i]]
		if !ok {
			edits = append(edits, cut(data, ranges[i]))
			continue
		}
		old, err := renderOnly(fileSourceOf(sources[i]).Only)
		if err != nil {
			return nil, err
		}
		changed, err := renderOnly(fileSourceOf(want).Only)
		if err != nil {
			return nil, err
		}
		if want.URL != sources[i].URL {
			line, err := toml.Marshal(struct {
				URL string `toml:"url"`
			}{want.URL})
			if err != nil {
				return nil, err
			}
			r := ranges[i].url
			edits = append(edits, edit{int(r.Offset), int(r.Offset + r.Length), strings.TrimSuffix(string(line), "\n")})
		}
		only := ranges[i].only
		if start, end := int(only.Offset), int(only.Offset+only.Length); old != changed && only.Length > 0 {
			// Skill names hold no #, so one in the list starts a comment.
			if bytes.IndexByte(data[start:end], '#') >= 0 {
				edit := "change it by hand to " + changed
				if changed == "" {
					edit = "remove it by hand, so that the source takes every skill"
				}
				return nil, fmt.Errorf("the only list of %s%s holds comments, which a rewrite would lose; %s", SourcePrefix, names[i], edit)
			}
			if changed == "" {
				// A comment that trails the list keeps its line; else the line goes.
				lineEnd := len(data)
				if next := bytes.IndexByte(data[end:], '\n'); next >= 0 {
					lineEnd = end + next + 1
				}
				if rest := bytes.TrimLeft(data[end:lineEnd], " \t"); len(rest) > 0 && rest[0] == '#' {
					end += len(data[end:lineEnd]) - len(rest)
				} else {
					start, end = bytes.LastIndexByte(data[:start], '\n')+1, lineEnd
				}
			}
			edits = append(edits, edit{start, end, changed})
		}
	}
	for i, def := range f.MCPs {
		if name, err := def.name(); err != nil || c.MCPs[name] != nil {
			continue
		}
		if len(servers) != len(f.MCPs) {
			return nil, errors.New("servers written other than as [[mcps]] entries cannot be amended")
		}
		edits = append(edits, cut(data, servers[i]))
	}

	out := slices.Clone(data)
	// Going backwards keeps the offsets of the earlier edits valid.
	slices.SortFunc(edits, func(a, b edit) int { return b.start - a.start })
	for _, e := range edits {
		out = slices.Concat(out[:e.start], []byte(e.text), out[e.end:])
	}
	if len(edits) > 0 && edits[0].end == len(data) && edits[0].text == "" {
		// A cut at the end leaves the blank line that stood before the entry.
		out = bytes.TrimRight(out, "\n")
		if len(out) > 0 {
			out = append(out, '\n')
		}
	}

	known := map[string]bool{}
	for _, name := range names {
		known[name] = true
	}
	var tail []byte
	for _, name := range sortedKeys(c.Sources) {
		if !known[name] {
			block, err := toml.Marshal(struct {
				Skills []*fileSource `toml:"skills"`
			}{[]*fileSource{fileSourceOf(c.Sources[name])}})
			if err != nil {
				return nil, err
			}
			tail = append(append(tail, '\n'), tidy(block)...)
		}
	}
	for _, def := range f.MCPs {
		if name, err := def.name(); err == nil {
			known[MCPPrefix+name] = true
		}
	}
	for _, name := range c.MCPNames() {
		if !known[MCPPrefix+name] {
			block, err := toml.Marshal(struct {
				MCPs []*MCP `toml:"mcps"`
			}{[]*MCP{c.MCPs[name]}})
			if err != nil {
				return nil, err
			}
			tail = append(append(tail, '\n'), block...)
		}
	}
	if len(tail) > 0 && len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, tail...), nil
}

// renderOnly renders the `only` key of a source, or "" without one.
func renderOnly(only []any) (string, error) {
	if len(only) == 0 {
		return "", nil
	}
	line, err := toml.Marshal(struct {
		Only []any `toml:"only,inline"`
	}{only})
	return strings.TrimSuffix(string(tidy(line)), "\n"), err
}

type edit struct {
	start, end int
	text       string
}

// entryRange tells where a [[skills]] or [[mcps]] entry stands: from the
// start of its header's line to that of the next header other than one of
// its subtables, or the end. The url and only ranges of a [[skills]] entry
// hold those keys and values; a range is empty for a missing key.
type entryRange struct {
	start, end int
	url, only  unstable.Range
}

// entryRanges finds every [[skills]] and every [[mcps]] entry, in order.
func entryRanges(data []byte) (skills, mcps []entryRange, err error) {
	var p unstable.Parser
	p.Reset(data)
	var entry *entryRange
	kind, own := "", false // own is false within a subtable of the entry
	for p.NextExpression() {
		expr := p.Expression()
		switch expr.Kind {
		case unstable.Table, unstable.ArrayTable:
			key := keyOf(expr)
			if own = false; kind != "" && strings.HasPrefix(key, kind+".") {
				break
			}
			first := expr.Key()
			first.Next()
			// A key quoted with escapes is a copy, not a part of data.
			at, name := cap(data)-cap(first.Node().Data), first.Node().Data
			if at < 0 || at+len(name) > len(data) || !bytes.Equal(data[at:at+len(name)], name) {
				return nil, nil, fmt.Errorf("the header of [%s] cannot be amended", key)
			}
			start := lineStart(data, at)
			if entry != nil {
				entry.end = start
			}
			entry, kind = nil, ""
			if expr.Kind != unstable.ArrayTable {
				break
			}
			switch key {
			case "skills":
				skills = append(skills, entryRange{start: start})
				entry = &skills[len(skills)-1]
			case "mcps":
				mcps = append(mcps, entryRange{start: start})
				entry = &mcps[len(mcps)-1]
			default:
				continue
			}
			kind, own = key, true
		case unstable.KeyValue:
			if kind != "skills" || !own {
				break
			}
			switch keyOf(expr) {
			case "url":
				entry.url = expr.Raw
			case "only":
				entry.only = expr.Raw
			}
		}
	}
	if entry != nil {
		entry.end = len(data)
	}
	return skills, mcps, p.Error()
}

// cut deletes an entry with the comment lines right above its header. The
// blank and comment lines that end it go too, except the comment lines right
// above the next header, which belong to that one.
func cut(data []byte, r entryRange) edit {
	start, end := r.start, r.end
	for start > 0 && isComment(data[lineStart(data, start-1):start]) {
		start = lineStart(data, start-1)
	}
	if end < len(data) {
		for end > start && isComment(data[lineStart(data, end-1):end]) {
			end = lineStart(data, end-1)
		}
	}
	return edit{start, end, ""}
}

// lineStart returns the offset of the line that holds offset.
func lineStart(data []byte, offset int) int {
	return bytes.LastIndexByte(data[:offset], '\n') + 1
}

func isComment(line []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(line, " \t"), []byte("#"))
}

// keyOf joins the parts of a table's or a key-value's key with dots.
func keyOf(node *unstable.Node) string {
	var parts []string
	for key := node.Key(); key.Next(); {
		parts = append(parts, string(key.Node().Data))
	}
	return strings.Join(parts, ".")
}
