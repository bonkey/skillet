// Package secrets keeps the values that catalogs refer to as ${NAME}. They
// live in a local file that is never part of a pushed catalog.
package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

type Store map[string]string

var (
	placeholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	validName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

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

// Save writes the store readable by the owner only.
func (s Store) Save(file string) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func (s Store) Set(name, value string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("%q is not a valid secret name: use letters, digits and underscores", name)
	}
	s[name] = value
	return nil
}

func (s Store) Names() []string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

// Placeholders lists the secret names a text refers to.
func Placeholders(text string) []string {
	var names []string
	for _, match := range placeholder.FindAllStringSubmatch(text, -1) {
		names = append(names, match[1])
	}
	return names
}
