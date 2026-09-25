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
// servers the file lacks are appended, and a source's changed `only` list is
// replaced where it stands, so comments and layout stay. A file that does
// not exist yet is written whole. The result goes to a temporary file first
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
	onlys, err := onlyRanges(data)
	if err != nil {
		return nil, err
	}
	if len(onlys) != len(sources) {
		return nil, errors.New("sources written other than as [[skills]] entries cannot be amended")
	}

	out := slices.Clone(data)
	// Going backwards keeps the offsets of the earlier entries valid.
	for i := len(sources) - 1; i >= 0; i-- {
		want, ok := c.Sources[names[i]]
		if !ok {
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
		start, end := int(onlys[i].Offset), int(onlys[i].Offset+onlys[i].Length)
		if old == changed || onlys[i].Length == 0 {
			continue
		}
		// Skill names hold no #, so one in the list starts a comment.
		if bytes.IndexByte(out[start:end], '#') >= 0 {
			edit := "change it by hand to " + changed
			if changed == "" {
				edit = "remove it by hand, so that the source takes every skill"
			}
			return nil, fmt.Errorf("the only list of %s%s holds comments, which a rewrite would lose; %s", SourcePrefix, names[i], edit)
		}
		if changed == "" {
			// A comment that trails the list keeps its line; else the line goes.
			lineEnd := len(out)
			if next := bytes.IndexByte(out[end:], '\n'); next >= 0 {
				lineEnd = end + next + 1
			}
			if rest := bytes.TrimLeft(out[end:lineEnd], " \t"); len(rest) > 0 && rest[0] == '#' {
				end += len(out[end:lineEnd]) - len(rest)
			} else {
				start, end = bytes.LastIndexByte(out[:start], '\n')+1, lineEnd
			}
		}
		out = slices.Concat(out[:start], []byte(changed), out[end:])
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

// onlyRanges finds, for every [[skills]] entry in order, where its `only`
// key and value stand; the range is empty for an entry without one.
func onlyRanges(data []byte) ([]unstable.Range, error) {
	var p unstable.Parser
	p.Reset(data)
	var ranges []unstable.Range
	inSkills := false
	for p.NextExpression() {
		expr := p.Expression()
		switch expr.Kind {
		case unstable.Table, unstable.ArrayTable:
			inSkills = expr.Kind == unstable.ArrayTable && keyOf(expr) == "skills"
			if inSkills {
				ranges = append(ranges, unstable.Range{})
			}
		case unstable.KeyValue:
			if inSkills && keyOf(expr) == "only" {
				ranges[len(ranges)-1] = expr.Raw
			}
		}
	}
	return ranges, p.Error()
}

// keyOf joins the parts of a table's or a key-value's key with dots.
func keyOf(node *unstable.Node) string {
	var parts []string
	for key := node.Key(); key.Next(); {
		parts = append(parts, string(key.Node().Data))
	}
	return strings.Join(parts, ".")
}
