// Package bootstrap provisions the machine winify is installed on (and, later,
// remote Windows servers) so it is immediately usable as a control center and
// a native-service deploy target: directories, NSSM, WinRM, the winify service
// itself and Caddy.
//
// Every step is idempotent: Check reports whether the step is already satisfied,
// Apply performs it, and the result is recorded in app_meta so a re-run skips
// what is done and repairs what failed.
//
// SECURITY: applying steps is a machine-level operation. Bootstrap must run
// elevated (an interactive user via UAC, or the winify service as LocalSystem)
// and every apply should be audited by the caller.
package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Devonlegend/winify/internal/deployment"
)

// Step is one idempotent provisioning action.
type Step interface {
	Name() string
	// Check reports whether the step is already satisfied.
	Check(ctx context.Context, r deployment.Runner) (bool, error)
	// Apply performs the step. It must be safe to run twice.
	Apply(ctx context.Context, r deployment.Runner) error
	// Privileged steps require elevation.
	Privileged() bool
}

// funcStep adapts Go closures into a Step, for steps that run in-process
// (storing a credential, creating a server) rather than over a Runner.
type funcStep struct {
	name  string
	check func(ctx context.Context) (bool, error)
	apply func(ctx context.Context) error
}

func (s funcStep) Name() string     { return s.name }
func (s funcStep) Privileged() bool { return false }

func (s funcStep) Check(ctx context.Context, _ deployment.Runner) (bool, error) {
	return s.check(ctx)
}

func (s funcStep) Apply(ctx context.Context, _ deployment.Runner) error {
	return s.apply(ctx)
}

// Paths is the on-host layout bootstrap creates and later phases use.
type Paths struct {
	Root    string
	Tools   string
	NSSM    string
	Caddy   string
	Work    string
	Apps    string
	Backups string
	Logs    string
	Data    string
}

// DefaultRoot returns the platform install root: %ProgramData%\winify on
// Windows, /var/lib/winify elsewhere.
func DefaultRoot() string {
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "winify")
		}
		return `C:\ProgramData\winify`
	}
	return "/var/lib/winify"
}

// DefaultPaths derives the full layout from a root directory.
func DefaultPaths(root string) Paths {
	return Paths{
		Root:    root,
		Tools:   filepath.Join(root, "tools"),
		NSSM:    filepath.Join(root, "tools", "nssm.exe"),
		Caddy:   filepath.Join(root, "tools", "caddy.exe"),
		Work:    filepath.Join(root, "work"),
		Apps:    filepath.Join(root, "apps"),
		Backups: filepath.Join(root, "backups"),
		Logs:    filepath.Join(root, "logs"),
		Data:    filepath.Join(root, "data"),
	}
}

// Options configures a Bootstrap run.
type Options struct {
	Paths       Paths
	NSSMSource  string // path on this host to nssm.exe ("" = assume present)
	NSSMSHA256  string // optional pinned hash of the source binary
	EnableWinRM bool
	// ServiceName, ExePath and ConfigPath install winify as a Windows service.
	// The step is included only when ServiceName and ExePath are set.
	ServiceName string
	ExePath     string
	ConfigPath  string
	// Caddy* provision the reverse proxy and open its firewall ports.
	CaddyEnabled bool
	CaddySource  string
	CaddyURL     string
	CaddySHA256  string
	CaddyAdmin   string
	// TargetCheck/Apply add a step that stores a WinRM credential and creates
	// the winsvc server entry. Both must be set for the step to appear.
	// TargetStepName names the step (default "local-target").
	TargetCheck    func(ctx context.Context) (bool, error)
	TargetApply    func(ctx context.Context) error
	TargetStepName string
	DryRun         bool
	Meta           MetaStore
	Runner         deployment.Runner
	Logf           func(format string, args ...any)
}

// Bootstrap runs the provisioning steps in order.
type Bootstrap struct {
	steps  []Step
	meta   MetaStore
	runner deployment.Runner
	dryRun bool
	logf   func(format string, args ...any)
}

// New builds the step list for the given options.
func New(opts Options) *Bootstrap {
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	steps := []Step{
		dirsStep{paths: []string{
			opts.Paths.Root, opts.Paths.Tools, opts.Paths.Work,
			opts.Paths.Apps, opts.Paths.Backups, opts.Paths.Logs, opts.Paths.Data,
		}},
		nssmStep{path: opts.Paths.NSSM, source: opts.NSSMSource, sha256: opts.NSSMSHA256, logf: opts.Logf},
	}
	if opts.EnableWinRM {
		steps = append(steps, winrmStep{})
	}
	if opts.ServiceName != "" && opts.ExePath != "" {
		steps = append(steps, selfServiceStep{name: opts.ServiceName, exe: opts.ExePath, config: opts.ConfigPath})
	}
	if opts.TargetCheck != nil && opts.TargetApply != nil {
		name := opts.TargetStepName
		if name == "" {
			name = "local-target"
		}
		steps = append(steps, funcStep{name: name, check: opts.TargetCheck, apply: opts.TargetApply})
	}
	if opts.CaddyEnabled {
		steps = append(steps,
			caddyStep{
				path:     opts.Paths.Caddy,
				nssmPath: opts.Paths.NSSM,
				dir:      filepath.Join(opts.Paths.Root, "caddy"),
				logDir:   opts.Paths.Logs,
				admin:    opts.CaddyAdmin,
				source:   opts.CaddySource,
				url:      opts.CaddyURL,
				sha256:   opts.CaddySHA256,
				logf:     opts.Logf,
			},
			firewallStep{ports: []int{80, 443}},
		)
	}
	return &Bootstrap{steps: steps, meta: opts.Meta, runner: opts.Runner, dryRun: opts.DryRun, logf: opts.Logf}
}

// StatusKind is the outcome of a step.
type StatusKind string

const (
	StatusDone    StatusKind = "done"    // already satisfied
	StatusApplied StatusKind = "applied" // applied this run
	StatusPending StatusKind = "pending" // would apply (dry-run)
	StatusError   StatusKind = "error"
)

// Result is the outcome of one step in a run.
type Result struct {
	Name   string
	Status StatusKind
	Error  string
}

// Run executes every step, stopping at the first failure. A nil error means all
// steps are satisfied.
func (b *Bootstrap) Run(ctx context.Context) ([]Result, error) {
	results := make([]Result, 0, len(b.steps))
	for _, step := range b.steps {
		done, err := step.Check(ctx, b.runner)
		if err != nil {
			results = append(results, Result{Name: step.Name(), Status: StatusError, Error: err.Error()})
			b.record(ctx, step.Name(), "error: "+err.Error())
			return results, fmt.Errorf("%s: check: %w", step.Name(), err)
		}
		if done {
			results = append(results, Result{Name: step.Name(), Status: StatusDone})
			b.record(ctx, step.Name(), "done")
			continue
		}
		if b.dryRun {
			results = append(results, Result{Name: step.Name(), Status: StatusPending})
			continue
		}
		b.logf("bootstrap: applying %s", step.Name())
		if err := step.Apply(ctx, b.runner); err != nil {
			results = append(results, Result{Name: step.Name(), Status: StatusError, Error: err.Error()})
			b.record(ctx, step.Name(), "error: "+err.Error())
			return results, fmt.Errorf("%s: %w", step.Name(), err)
		}
		results = append(results, Result{Name: step.Name(), Status: StatusApplied})
		b.record(ctx, step.Name(), "done")
		b.logf("bootstrap: %s done", step.Name())
	}
	return results, nil
}

// StepStatus is a step's recorded state, for the setup page.
type StepStatus struct {
	Name  string
	Done  bool
	Error string
}

// Status reads the recorded state of each step. It does not run any commands.
func (b *Bootstrap) Status(ctx context.Context) ([]StepStatus, error) {
	out := make([]StepStatus, 0, len(b.steps))
	for _, step := range b.steps {
		st := StepStatus{Name: step.Name()}
		if b.meta != nil {
			v, err := b.meta.GetMeta(ctx, metaKey(step.Name()))
			if err != nil {
				return nil, err
			}
			switch {
			case v == "done":
				st.Done = true
			case strings.HasPrefix(v, "error:"):
				st.Error = strings.TrimSpace(strings.TrimPrefix(v, "error:"))
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// Complete reports whether every step has been recorded done.
func (b *Bootstrap) Complete(ctx context.Context) (bool, error) {
	statuses, err := b.Status(ctx)
	if err != nil {
		return false, err
	}
	for _, st := range statuses {
		if !st.Done {
			return false, nil
		}
	}
	return true, nil
}

func (b *Bootstrap) record(ctx context.Context, step, value string) {
	if b.meta == nil {
		return
	}
	if err := b.meta.SetMeta(ctx, metaKey(step), value); err != nil {
		b.logf("bootstrap: record %s: %v", step, err)
	}
}
