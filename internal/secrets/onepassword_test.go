package secrets

import (
	"errors"
	"reflect"
	"testing"
)

type fakeReader struct {
	items map[string]map[string]string
	calls []string
}

func (f *fakeReader) Fields(item Item) (map[string]string, error) {
	f.calls = append(f.calls, item.Item)
	if fields, ok := f.items[item.Item]; ok {
		return fields, nil
	}
	return nil, errors.New("not signed in")
}

func TestResolverIsLazyAndOrdered(t *testing.T) {
	reader := &fakeReader{items: map[string]map[string]string{
		"personal": {"TAVILY_API_KEY": "from-personal", "SHARED": "personal-wins"},
		"work":     {"JIRA_TOKEN": "from-work", "SHARED": "work-loses"},
	}}
	r := &Resolver{
		Local:  Store{"LOCAL_ONLY": "local", "TAVILY_API_KEY": "local-override"},
		Items:  []Item{{Item: "personal"}, {Item: "work"}, {Item: "locked"}},
		Reader: reader,
	}

	if got, _ := r.Expand("${LOCAL_ONLY} ${TAVILY_API_KEY} plain"); got != "local local-override plain" || len(reader.calls) != 0 {
		t.Fatalf("local values need no item: %q, calls %v", got, reader.calls)
	}
	if got, _ := r.Expand("${SHARED}"); got != "personal-wins" || !reflect.DeepEqual(reader.calls, []string{"personal"}) {
		t.Fatalf("the first item wins and later ones stay unread: %q, calls %v", got, reader.calls)
	}
	if got, _ := r.Expand("${JIRA_TOKEN} ${SHARED}"); got != "from-work personal-wins" || !reflect.DeepEqual(reader.calls, []string{"personal", "work"}) {
		t.Fatalf("each item is read once: %q, calls %v", got, reader.calls)
	}
	got, missing := r.Expand("${NOWHERE}")
	if got != "${NOWHERE}" || !reflect.DeepEqual(missing, []string{"NOWHERE"}) || len(r.Problems) != 1 {
		t.Errorf("an unreadable item is a problem, not a failure: %q %v %v", got, missing, r.Problems)
	}
	if len(reader.calls) != 3 {
		t.Errorf("calls: %v", reader.calls)
	}
}
