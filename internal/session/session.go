// Package session tracks skills that are enabled in a project only while a
// launched command runs.
package session

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/pelletier/go-toml/v2"

	"github.com/bonkey/skillet/internal/catalog"
)

// ProjectDir is where a project keeps its sessions. A directory holds one
// file per running session, named after the launcher's pid.
func ProjectDir(root string) string {
	return filepath.Join(root, ".claude", "skills", ".skillet-sessions")
}

func file(dir string, pid int) string {
	return filepath.Join(dir, strconv.Itoa(pid)+".toml")
}

// Session is what one `skillet run` enables while its command runs.
type Session struct {
	catalog.Set
	// Agent limits the session's MCP servers to the agent that the command
	// starts. Empty means every configured agent.
	Agent string `toml:"agent,omitempty"`
	// Added lists the skills and servers ("mcp:name") of the set that were
	// not enabled when the session started. They go away with the session.
	Added []string `toml:"added,omitempty"`
}

func Write(dir string, pid int, s Session) error {
	data, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(file(dir, pid), data, 0o644)
}

func Remove(dir string, pid int) error {
	err := os.Remove(file(dir, pid))
	if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		os.Remove(dir)
	}
	return err
}

// Read returns the sessions in dir, split by whether their launcher still
// runs.
func Read(dir string) (live, dead []Session) {
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		pid, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".toml"))
		if err != nil {
			continue
		}
		var s Session
		if data, err := os.ReadFile(file(dir, pid)); err != nil || toml.Unmarshal(data, &s) != nil {
			continue
		}
		if alive(pid) {
			live = append(live, s)
		} else {
			dead = append(dead, s)
		}
	}
	return live, dead
}

// Prune deletes the files of dead launchers.
func Prune(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if pid, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".toml")); err == nil && !alive(pid) {
			Remove(dir, pid)
		}
	}
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Run executes a command with inherited stdio, forwards termination signals
// to it and returns its exit code.
func Run(argv []string) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return 1, err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	go func() {
		for sig := range signals {
			cmd.Process.Signal(sig)
		}
	}()
	err := cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
