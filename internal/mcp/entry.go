// Package mcp writes MCP server entries into the user-level config files of
// coding agents, and removes the entries it wrote.
package mcp

import (
	"bytes"
	"encoding/json"
)

// Entry is one server as an agent's config spells it: ordered fields whose
// values are strings, numbers, booleans, string lists or string maps.
type Entry []Field

type Field struct {
	Key   string
	Value any
}

func (e *Entry) add(key string, value any) { *e = append(*e, Field{key, value}) }

// addMap adds a string map when it has entries.
func (e *Entry) addMap(key string, value map[string]string) {
	if len(value) > 0 {
		e.add(key, value)
	}
}

// MarshalJSON keeps the field order.
func (e Entry) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, field := range e {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(field.Key)
		value, err := json.Marshal(field.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// plain converts an entry to the generic form a decoded config value has,
// for comparing the two.
func (e Entry) plain() any {
	data, _ := json.Marshal(e)
	var out any
	json.Unmarshal(data, &out)
	return out
}
