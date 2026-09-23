// Package detect inspects a repository working tree and proposes a build and
// run plan for it: language/framework, build command, executable and arguments,
// listening port and health path.
//
// It is deliberately pure — it only reads a FileTree — so it can be unit-tested
// against fixture trees and reused for any target type. It never executes
// anything and never touches the network.
package detect

import (
	"path"
	"sort"
	"strings"
)

// Language is the detected application language.
type Language string

const (
	LanguagePython Language = "python"
	LanguageNode   Language = "node"
	LanguageGo     Language = "go"
	LanguageDotNet Language = "dotnet"
	LanguageStatic Language = "static"
)

// Toolchain identifiers, used to decide what has to be installed on a target
// before a build or a run can succeed.
const (
	RuntimePython = "python"
	RuntimeNode   = "node"
	RuntimeGo     = "go"
	RuntimeDotNet = "dotnet"
)

// Confidence levels for a detected plan.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// Target values name the deployment mechanism a plan should use. They match the
// server types in internal/config, so the wizard can select the right one.
const (
	// TargetWindowsService runs the app as a Windows service via NSSM (or, for
	// a static site, serves it with the per-target Caddy).
	TargetWindowsService = "winsvc"
	// TargetIIS publishes files to an IIS site.
	TargetIIS = "iis"
)

// Evidence records one file that contributed to a detection, so the UI can
// explain why a plan was proposed ("detected Python from requirements.txt").
type Evidence struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Plan is a proposed build and run configuration derived from a repository.
//
// Exe is the suggested service executable. When it is relative (for example
// ".venv\Scripts\python.exe" or "app.exe") the caller resolves it against the
// project's install directory; when it names a program on PATH (for example
// "node.exe") it is used as-is. BuildCommand is executed with the working
// directory set to the repository root.
type Plan struct {
	Language  Language `json:"language"`
	Framework string   `json:"framework,omitempty"`
	// Target is the deployment mechanism to use: TargetWindowsService or
	// TargetIIS.
	Target string `json:"target"`
	// BuildRuntime and RunRuntime name the toolchains needed to build and to
	// run the workload. RunRuntime is empty for compiled output (Go) and static
	// sites; BuildRuntime is empty for a static site.
	BuildRuntime   string `json:"build_runtime,omitempty"`
	RunRuntime     string `json:"run_runtime,omitempty"`
	RuntimeVersion string `json:"runtime_version,omitempty"`
	BuildCommand   string `json:"build_command,omitempty"`
	Exe            string `json:"exe,omitempty"`
	Args           string `json:"args,omitempty"`
	SourceSubdir   string `json:"source_subdir,omitempty"`
	Port           int    `json:"port,omitempty"`
	HealthPath     string `json:"health_path,omitempty"`
	// CaddyMode is set to "static" when the workload is a set of files to serve
	// rather than a process to run.
	CaddyMode  string     `json:"caddy_mode,omitempty"`
	Confidence string     `json:"confidence"`
	Evidence   []Evidence `json:"evidence"`
}

// FileTree is a read-only view of a repository working tree. Paths are relative
// to the repository root and use forward slashes.
type FileTree interface {
	List() []string
	Read(p string) ([]byte, error)
}

// detector inspects a tree and returns a plan, or nil when it does not apply.
type detector func(FileTree) *Plan

// detectors are tried in order; the first non-nil plan wins. The order is by
// specificity: a repository that carries several manifests (a Python API with a
// bundled JS frontend, say) resolves to the backend first.
var detectors = []struct {
	language Language
	detect   detector
}{
	{LanguageGo, detectGo},
	{LanguageDotNet, detectDotNet},
	{LanguagePython, detectPython},
	{LanguageNode, detectNode},
	{LanguageStatic, detectStatic},
}

// Detect returns the best plan for tree, or nil when nothing recognisable is
// found.
func Detect(tree FileTree) *Plan {
	for _, d := range detectors {
		if p := d.detect(tree); p != nil {
			return p
		}
	}
	return nil
}

// DetectAs returns the plan for one explicitly requested language, or nil when
// that language's detector does not apply. It backs the wizard's manual
// language override.
func DetectAs(tree FileTree, language Language) *Plan {
	for _, d := range detectors {
		if d.language == language {
			return d.detect(tree)
		}
	}
	return nil
}

// ---- FileTree helpers ----

// sortedList returns List() sorted, so detection is deterministic.
func sortedList(tree FileTree) []string {
	files := append([]string(nil), tree.List()...)
	sort.Strings(files)
	return files
}

// has reports whether p is present in the tree.
func has(tree FileTree, p string) bool {
	for _, f := range tree.List() {
		if f == p {
			return true
		}
	}
	return false
}

// read returns the contents of p, or "" when it is missing or unreadable.
func read(tree FileTree, p string) string {
	b, err := tree.Read(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// firstExt returns the shallowest listed path with the given extension (for
// example ".csproj"), or "".
func firstExt(tree FileTree, ext string) string {
	for _, f := range sortedList(tree) {
		if strings.EqualFold(path.Ext(f), ext) {
			return f
		}
	}
	return ""
}

// allExt returns every listed path with the given extension.
func allExt(tree FileTree, ext string) []string {
	var out []string
	for _, f := range sortedList(tree) {
		if strings.EqualFold(path.Ext(f), ext) {
			out = append(out, f)
		}
	}
	return out
}

// firstBase returns the shallowest listed path whose base name equals name
// (case-insensitive), or "".
func firstBase(tree FileTree, name string) string {
	for _, f := range sortedList(tree) {
		if strings.EqualFold(path.Base(f), name) {
			return f
		}
	}
	return ""
}

// moduleDir returns the forward-slash directory of p, using "." for the root.
func moduleDir(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}
