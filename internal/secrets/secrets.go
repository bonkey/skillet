// Package secrets finds the values that catalogs refer to as ${NAME}: in a
// local file that is never part of a pushed catalog, and in 1Password items.
package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
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
	if err := yaml.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return store, nil
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
