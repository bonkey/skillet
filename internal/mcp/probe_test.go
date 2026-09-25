package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnswers(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"json": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if r.Method != http.MethodPost || !strings.Contains(string(body), `"initialize"`) {
				http.Error(w, "no", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		},
		"stream": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: message\ndata: {}\n\n")
		},
		"auth": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="x"`)
			w.WriteHeader(http.StatusUnauthorized)
		},
		"page": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html></html>")
		},
		"missing": http.NotFound,
	}
	want := map[string]bool{"json": true, "stream": true, "auth": true, "page": false, "missing": false}
	for name, handler := range handlers {
		server := httptest.NewServer(handler)
		if got := Answers(server.URL + "/mcp"); got != want[name] {
			t.Errorf("%s: Answers = %v", name, got)
		}
		server.Close()
	}
	if Answers("http://127.0.0.1:1/mcp") {
		t.Error("nothing listens there")
	}
}
