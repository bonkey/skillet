package mcp

import (
	"net/http"
	"strings"
	"time"
)

const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
	`"capabilities":{},"clientInfo":{"name":"skillet","version":"0"}}}`

// Answers tells whether an MCP server answers at url over streamable HTTP:
// it takes an initialize request with JSON or an event stream, or asks for
// authorization first.
func Answers(url string) bool {
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(initialize))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return true
	}
	kind := resp.Header.Get("Content-Type")
	return resp.StatusCode/100 == 2 &&
		(strings.HasPrefix(kind, "application/json") || strings.HasPrefix(kind, "text/event-stream"))
}
