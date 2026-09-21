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

	"github.com/bonkey/skillet/internal/catalog"
)

// Dir holds one file per running session, named after the launcher's pid.
func Dir(root string) string {
	return filepath.Join(root, ".claude", "skills", ".skillet-sessions")
}

func file(root string, pid int) string {
	return filepath.Join(Dir(root), strconv.Itoa(pid)+".yaml")
}

func Write(root string, pid int, set catalog.Set) error { return set.Save(file(root, pid)) }

func Remove(root string, pid int) error {
	err := os.Remove(file(root, pid))
	if entries, _ := os.ReadDir(Dir(root)); len(entries) == 0 {
		os.Remove(Dir(root))
	}
	return err
}

// Live returns the sets of sessions whose launcher still runs. Files of
// dead launchers are deleted.
func Live(root string) []catalog.Set {
	entries, _ := os.ReadDir(Dir(root))
	var sets []catalog.Set
	for _, entry := range entries {
		pid, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".yaml"))
		if err != nil {
			continue
		}
		if !alive(pid) {
			Remove(root, pid)
			continue
		}
		if set, err := catalog.LoadSet(file(root, pid)); err == nil {
			sets = append(sets, set)
		}
	}
	return sets
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
