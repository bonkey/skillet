package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tailscale/hujson"
)

// jsonFile edits the server entries of a JSON config in place. Everything
// else in the file, comments and formatting included, stays as it is.
type jsonFile struct {
	root hujson.Value
	unit string // one level of indentation
}

func parseJSONFile(data []byte) (*jsonFile, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}\n")
	}
	root, err := hujson.Parse(data)
	if err != nil {
		return nil, err
	}
	obj, ok := root.Value.(*hujson.Object)
	if !ok {
		return nil, fmt.Errorf("the top level is not an object")
	}
	f := &jsonFile{root: root, unit: "  "}
	if len(obj.Members) > 0 {
		before := string(obj.Members[0].Name.BeforeExtra)
		if i := strings.LastIndexByte(before, '\n'); i >= 0 && strings.TrimSpace(before[i+1:]) == "" && before[i+1:] != "" {
			f.unit = before[i+1:]
		}
	}
	return f, nil
}

func (f *jsonFile) bytes() []byte { return f.root.Pack() }

// object returns the object at node, creating missing levels on request.
func (f *jsonFile) object(node []string, create bool) (*hujson.Object, error) {
	obj := f.root.Value.(*hujson.Object)
	for depth, key := range node {
		member := findMember(obj, key)
		if member == nil {
			if !create {
				return nil, nil
			}
			member = f.appendMember(obj, depth, key, &hujson.Object{})
		}
		next, ok := member.Value.Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("%q is not an object", strings.Join(node[:depth+1], "."))
		}
		obj = next
	}
	return obj, nil
}

func findMember(obj *hujson.Object, key string) *hujson.ObjectMember {
	for i := range obj.Members {
		if lit, ok := obj.Members[i].Name.Value.(hujson.Literal); ok && lit.String() == key {
			return &obj.Members[i]
		}
	}
	return nil
}

// appendMember adds a member on its own line, indented for an object depth
// levels below the top, and keeps the file's trailing-comma habit.
func (f *jsonFile) appendMember(obj *hujson.Object, depth int, key string, value hujson.ValueTrimmed) *hujson.ObjectMember {
	trailingComma := false
	if n := len(obj.Members); n > 0 {
		last := &obj.Members[n-1].Value
		trailingComma = last.AfterExtra != nil
		if !trailingComma {
			last.AfterExtra = nil
		}
	} else {
		obj.AfterExtra = hujson.Extra("\n" + strings.Repeat(f.unit, depth))
	}
	member := hujson.ObjectMember{
		Name:  hujson.Value{BeforeExtra: hujson.Extra("\n" + strings.Repeat(f.unit, depth+1)), Value: hujson.String(key)},
		Value: hujson.Value{BeforeExtra: hujson.Extra(" "), Value: value},
	}
	if trailingComma {
		member.Value.AfterExtra = hujson.Extra{}
	}
	obj.Members = append(obj.Members, member)
	return &obj.Members[len(obj.Members)-1]
}

func (f *jsonFile) names(node []string) ([]string, error) {
	obj, err := f.object(node, false)
	if err != nil || obj == nil {
		return nil, err
	}
	var names []string
	for _, member := range obj.Members {
		if lit, ok := member.Name.Value.(hujson.Literal); ok {
			names = append(names, lit.String())
		}
	}
	return names, nil
}

// get decodes one entry.
func (f *jsonFile) get(node []string, name string) (any, bool, error) {
	obj, err := f.object(node, false)
	if err != nil || obj == nil {
		return nil, false, err
	}
	member := findMember(obj, name)
	if member == nil {
		return nil, false, nil
	}
	value := member.Value.Clone()
	value.Standardize()
	var out any
	err = json.Unmarshal(value.Pack(), &out)
	return out, true, err
}

func (f *jsonFile) set(node []string, name string, entry Entry) error {
	obj, err := f.object(node, true)
	if err != nil {
		return err
	}
	indent := strings.Repeat(f.unit, len(node)+1)
	text, err := json.MarshalIndent(entry, indent, f.unit)
	if err != nil {
		return err
	}
	parsed, err := hujson.Parse(text)
	if err != nil {
		return err
	}
	if member := findMember(obj, name); member != nil {
		member.Value.Value = parsed.Value
		return nil
	}
	f.appendMember(obj, len(node), name, parsed.Value)
	return nil
}

func (f *jsonFile) remove(node []string, name string) error {
	obj, err := f.object(node, false)
	if err != nil || obj == nil {
		return err
	}
	for i := range obj.Members {
		lit, ok := obj.Members[i].Name.Value.(hujson.Literal)
		if !ok || lit.String() != name {
			continue
		}
		last := i == len(obj.Members)-1
		trailingComma := obj.Members[len(obj.Members)-1].Value.AfterExtra != nil
		obj.Members = append(obj.Members[:i], obj.Members[i+1:]...)
		if n := len(obj.Members); last && n > 0 {
			if trailingComma {
				if obj.Members[n-1].Value.AfterExtra == nil {
					obj.Members[n-1].Value.AfterExtra = hujson.Extra{}
				}
			} else {
				obj.Members[n-1].Value.AfterExtra = nil
			}
		}
		return nil
	}
	return nil
}
