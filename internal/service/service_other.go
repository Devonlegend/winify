//go:build !windows

// Package service lets winify run as a native Windows service. On other
// platforms these are stubs so the rest of the program builds unchanged.
package service

import (
	"context"
	"fmt"
)

// IsWindowsService is always false off Windows.
func IsWindowsService() bool { return false }

// Run is unavailable off Windows.
func Run(name string, fn func(ctx context.Context) error) error {
	return fmt.Errorf("windows services are only supported on Windows")
}

// RelaunchElevated is unavailable off Windows.
func RelaunchElevated(exePath, configPath string) error {
	return fmt.Errorf("elevation is only supported on Windows")
}
