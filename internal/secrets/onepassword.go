package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Item names a 1Password item whose fields are secrets: a field labelled
// TAVILY_API_KEY is the value of ${TAVILY_API_KEY}.
type Item struct {
	Account string `json:"account"` // sign-in address or account id
	Vault   string `json:"vault"`
	Item    string `json:"item"`
}

// Reader fetches the fields of an item, keyed by label.
type Reader interface {
	Fields(item Item) (map[string]string, error)
}

// OP reads items with the 1Password CLI.
type OP struct{}

func (OP) Fields(item Item) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "op", "item", "get", item.Item,
		"--vault", item.Vault, "--account", item.Account, "--format", "json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("op item get %s: %w: %s", item.Item, err, strings.TrimSpace(stderr.String()))
	}
	var out struct {
		Fields []struct {
			Label string `json:"label"`
			Value string `json:"value"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("op item get %s: %w", item.Item, err)
	}
	fields := map[string]string{}
	for _, field := range out.Fields {
		if field.Label != "" && field.Value != "" {
			fields[field.Label] = field.Value
		}
	}
	return fields, nil
}

// Resolver finds the value of a secret name: in the local store first, then
// in the items, in order. An item is read once, and only when a name is not
// found before it, so a run that needs no secret never calls the reader.
type Resolver struct {
	Local  Store
	Items  []Item
	Reader Reader

	fetched  []map[string]string
	Problems []string // items that could not be read
}

func (r *Resolver) Lookup(name string) (string, bool) {
	if value, ok := r.Local[name]; ok {
		return value, true
	}
	return r.LookupItems(name)
}

// LookupItems skips the local store.
func (r *Resolver) LookupItems(name string) (string, bool) {
	for i, item := range r.Items {
		if i >= len(r.fetched) {
			fields, err := r.Reader.Fields(item)
			if err != nil {
				r.Problems = append(r.Problems, err.Error())
			}
			r.fetched = append(r.fetched, fields)
		}
		if value, ok := r.fetched[i][name]; ok {
			return value, true
		}
	}
	return "", false
}

// Expand replaces every ${NAME} in text. Names without a value stay in
// place and are returned as missing.
func (r *Resolver) Expand(text string) (expanded string, missing []string) {
	expanded = placeholder.ReplaceAllStringFunc(text, func(match string) string {
		name := placeholder.FindStringSubmatch(match)[1]
		value, ok := r.Lookup(name)
		if !ok {
			missing = append(missing, name)
			return match
		}
		return value
	})
	return expanded, missing
}
