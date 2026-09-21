//go:build windows

// Package service lets winify run as a native Windows service (no NSSM for
// itself). The SCM starts the process; svc.Run drives the lifecycle and the
// callback runs the control center until the SCM asks it to stop.
package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/svc"
)

// IsWindowsService reports whether the process was started by the Windows SCM.
func IsWindowsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// Run runs fn as a Windows service named name. fn must block until ctx is
// cancelled. It returns when the SCM stops the service or fn exits.
func Run(name string, fn func(ctx context.Context) error) error {
	if name == "" {
		name = "winify"
	}
	return svc.Run(name, &handler{fn: fn})
}

type handler struct {
	fn func(ctx context.Context) error
}

// Execute is the SCM entry point. It reports status transitions and cancels the
// app's context on Stop/Shutdown.
func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- h.fn(ctx) }()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case err := <-errCh:
			changes <- svc.Status{State: svc.Stopped}
			if err != nil {
				return false, 1
			}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				<-errCh
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}

// RelaunchElevated starts winify again with args in a new elevated process so
// the operator can approve a UAC prompt. It is used when winify runs
// interactively without administrator rights (for example `winify serve` on a
// fresh install). It returns immediately; the elevated process does the work.
func RelaunchElevated(exePath string, args []string) error {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + escapePS(a) + "'"
	}
	script := fmt.Sprintf("Start-Process -FilePath '%s' -ArgumentList %s -Verb RunAs",
		escapePS(exePath), strings.Join(quoted, ","))
	return exec.Command("powershell", "-NoProfile", "-Command", script).Start()
}

// escapePS escapes a value for a single-quoted PowerShell string.
func escapePS(s string) string { return strings.ReplaceAll(s, "'", "''") }
