// Package gist stores catalogs in GitHub gists through the `gh` CLI.
package gist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// FileName is the gist file that holds a catalog.
const FileName = "skillet.toml"

type Client interface {
	Read(id string) (content string, err error)
	Create(content string, public bool) (id string, err error)
	Update(id, content string) error
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{20,}$`)

// ID extracts the gist id from an id or a gist URL.
func ID(ref string) (string, error) {
	ref, _, _ = strings.Cut(strings.TrimSpace(ref), "#")
	ref = strings.TrimSuffix(strings.TrimSuffix(ref, "/"), ".git")
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	if !idPattern.MatchString(ref) {
		return "", fmt.Errorf("%q is not a gist id or URL", ref)
	}
	return ref, nil
}

// GH talks to GitHub with the credentials of the `gh` CLI.
type GH struct{}

type payload struct {
	ID          string          `json:"id,omitempty"`
	Description string          `json:"description,omitempty"`
	Public      *bool           `json:"public,omitempty"`
	Files       map[string]file `json:"files"`
}

type file struct {
	Content string `json:"content"`
}

func (GH) api(method, path string, body *payload) (payload, error) {
	args := []string{"api", "--method", method, path}
	cmd := exec.Command("gh", args...)
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return payload{}, err
		}
		cmd.Args = append(cmd.Args, "--input", "-")
		cmd.Stdin = bytes.NewReader(data)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return payload{}, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	var out payload
	err := json.Unmarshal(stdout.Bytes(), &out)
	return out, err
}

func (g GH) Read(id string) (string, error) {
	out, err := g.api("GET", "gists/"+id, nil)
	if err != nil {
		return "", err
	}
	found, ok := out.Files[FileName]
	if !ok {
		return "", fmt.Errorf("gist %s has no file %s", id, FileName)
	}
	return found.Content, nil
}

func (g GH) Create(content string, public bool) (string, error) {
	out, err := g.api("POST", "gists", &payload{
		Description: "skillet catalog", Public: &public,
		Files: map[string]file{FileName: {Content: content}},
	})
	return out.ID, err
}

func (g GH) Update(id, content string) error {
	_, err := g.api("PATCH", "gists/"+id, &payload{Files: map[string]file{FileName: {Content: content}}})
	return err
}
