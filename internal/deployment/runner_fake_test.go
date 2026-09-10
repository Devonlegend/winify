package deployment

import (
	"context"
	"errors"
	"strings"
)

// fakeRunner records commands and can fail or emit canned output per command.
type fakeRunner struct {
	commands []string
	failOn   string
	outputs  func(cmd string) string
	closed   bool
}

func (f *fakeRunner) Run(_ context.Context, cmd string) (string, error) {
	f.commands = append(f.commands, cmd)
	if f.failOn != "" && strings.Contains(cmd, f.failOn) {
		return "boom", errors.New("command failed")
	}
	if f.outputs != nil {
		return f.outputs(cmd), nil
	}
	return "", nil
}

func (f *fakeRunner) Close() error { f.closed = true; return nil }

func (f *fakeRunner) joined() string { return strings.Join(f.commands, "\n") }

func (f *fakeRunner) indexOf(sub string) int {
	for i, c := range f.commands {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}
