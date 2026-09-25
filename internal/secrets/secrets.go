// Package secrets finds the values that catalogs refer to as ${NAME}: in a
// local file that is never part of a pushed catalog, and in 1Password items.
package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"regexp"
	"slices"

	"github.com/pelletier/go-toml/v2"
)

type Store map[string]string

var placeholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func Load(file string) (Store, error) {
	store := Store{}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if err := toml.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return store, nil
}

// Save writes the store as NAME = "value" lines, readable by its owner only.
func (s Store) Save(file string) error {
	data, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o600)
}

// Amend saves the store into file and keeps the file's text: the names the
// file lacks are appended. A file that does not exist yet is written whole.
// The result goes to a temporary file first and replaces file only when it
// reads back as the store, so a changed or removed value is refused.
func (s Store) Amend(file string) error {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return s.Save(file)
	}
	if err != nil {
		return err
	}
	held := Store{}
	if err := toml.Unmarshal(data, &held); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	for _, name := range slices.Sorted(maps.Keys(s)) {
		if _, ok := held[name]; ok {
			continue
		}
		line, err := toml.Marshal(Store{name: s[name]})
		if err != nil {
			return err
		}
		data = append(data, line...)
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if got, err := Load(tmp); err != nil || !maps.Equal(got, s) {
		os.Remove(tmp)
		return fmt.Errorf("%s: the new values cannot be added without rewriting the file; it is left as it is", file)
	}
	return os.Rename(tmp, file)
}

// Expand replaces every ${NAME} in text. Names without a value stay in
// place and are returned as missing.
func (s Store) Expand(text string) (expanded string, missing []string) {
	expanded = placeholder.ReplaceAllStringFunc(text, func(match string) string {
		name := placeholder.FindStringSubmatch(match)[1]
		value, ok := s[name]
		if !ok {
			missing = append(missing, name)
			return match
		}
		return value
	})
	return expanded, missing
}
