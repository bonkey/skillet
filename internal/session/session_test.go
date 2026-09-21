package session

import (
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/bonkey/skillet/internal/catalog"
)

func TestReadSplitsSessionsAndPruneReapsDeadOnes(t *testing.T) {
	root := ProjectDir(t.TempDir())
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	mine := Session{Set: catalog.Set{Packs: []string{"ios"}}, Agent: "claude-code"}
	if err := Write(root, os.Getpid(), mine); err != nil {
		t.Fatal(err)
	}
	stale := Session{Set: catalog.Set{Skills: []string{"stale"}}, Added: []string{"stale"}}
	if err := Write(root, dead.Process.Pid, stale); err != nil {
		t.Fatal(err)
	}

	if live, gone := Read(root); !reflect.DeepEqual(live, []Session{mine}) || !reflect.DeepEqual(gone, []Session{stale}) {
		t.Fatalf("got %+v and %+v", live, gone)
	}
	Prune(root)
	if _, err := os.Stat(file(root, dead.Process.Pid)); !os.IsNotExist(err) {
		t.Error("dead session file was not reaped")
	}

	if err := Remove(root, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("empty session directory should be removed")
	}
}

func TestRunReturnsExitCode(t *testing.T) {
	if code, err := Run([]string{"sh", "-c", "exit 3"}); code != 3 || err != nil {
		t.Errorf("got %d, %v", code, err)
	}
	if code, err := Run([]string{"true"}); code != 0 || err != nil {
		t.Errorf("got %d, %v", code, err)
	}
	if _, err := Run([]string{"/no/such/command"}); err == nil {
		t.Error("expected an error")
	}
}
