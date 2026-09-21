package bootstrap

import (
	"context"
	"strings"

	"github.com/Devonlegend/winify/internal/deployment"
)

// IsElevated reports whether the runner can make machine-level changes
// (enable WinRM, create services). Bootstrap refuses to apply without it.
func IsElevated(ctx context.Context, r deployment.Runner) (bool, error) {
	out, err := r.Run(ctx, "([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)")
	if err != nil {
		return false, err
	}
	return strings.EqualFold(lastLine(out), "True"), nil
}
