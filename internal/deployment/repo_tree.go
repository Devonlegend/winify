package deployment

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

// CloneShallow clones repoURL into dir on the target using a single commit of
// branch (or the remote default when branch is empty), replacing dir when it
// already exists. Repository detection only needs the current tree, and the
// clone runs on the target so private repositories use its own git credentials.
func CloneShallow(ctx context.Context, runner Runner, dir, repoURL, branch string, auth *GitAuth, logf func(format string, args ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cloneCtx := ctx
	if auth.Sensitive() {
		cloneCtx = withAuditRedaction(ctx, "git clone --depth 1 (credential redacted)")
	}
	if _, err := execCmd(cloneCtx, runner, shallowCloneScript(dir, repoURL, branch, auth), "git clone --depth 1", logf); err != nil {
		return fmt.Errorf("clone repo: %w", err)
	}
	return nil
}

// shallowCloneScript removes any previous clone and fetches one commit.
func shallowCloneScript(dir, repoURL, branch string, auth *GitAuth) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$dir = %s\n", psQuote(dir))
	b.WriteString("if (Test-Path -LiteralPath $dir) { Remove-Item -LiteralPath $dir -Recurse -Force }\n")
	fmt.Fprintf(&b, "$parent = Split-Path -Parent $dir\n")
	b.WriteString("if ($parent) { New-Item -ItemType Directory -Force -Path $parent | Out-Null }\n")
	args := "--depth 1"
	if strings.TrimSpace(branch) != "" {
		args += " --branch " + psQuote(branch)
	}
	opt := auth.gitOption(psQuote)
	if opt != "" {
		opt = " " + opt
	}
	fmt.Fprintf(&b, "git%s clone %s %s %s\n", opt, args, psQuote(repoURL), psQuote(dir))
	return b.String()
}

// RepoTree is a read-only view of a repository working tree on a target. It
// lists the tree once (skipping VCS and build directories) and reads individual
// files on demand, so inspecting a repository costs one listing plus a read per
// file the detector actually needs.
//
// It satisfies the structural detect.FileTree interface without importing the
// detect package, keeping the deployment layer free of detection logic.
type RepoTree struct {
	ctx    context.Context
	runner Runner
	root   string
	files  []string
}

// NewRepoTree lists dir on the target and returns a FileTree over it. Paths are
// relative to dir and use forward slashes.
func NewRepoTree(ctx context.Context, runner Runner, dir string) (*RepoTree, error) {
	t := &RepoTree{ctx: ctx, runner: runner, root: strings.TrimRight(dir, `\/`)}
	out, err := runner.Run(ctx, listFilesScript(t.root))
	if err != nil {
		return nil, fmt.Errorf("list repository files: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if p := normalizeRepoPath(line); p != "" {
			t.files = append(t.files, p)
		}
	}
	return t, nil
}

// normalizeRepoPath turns one line of the listing into a repository-relative,
// slash-separated path. The PowerShell script already strips the root and
// converts separators, but normalizing here keeps detection correct even if a
// target emits "./" or backslash-prefixed paths.
func normalizeRepoPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	for {
		switch {
		case strings.HasPrefix(p, "./"):
			p = p[2:]
		case strings.HasPrefix(p, "/"):
			p = p[1:]
		default:
			return p
		}
	}
}

// List returns every file path under the root, relative and slash-separated.
func (t *RepoTree) List() []string { return t.files }

// Read returns the contents of p, which is relative to the root.
func (t *RepoTree) Read(p string) ([]byte, error) {
	rel := strings.ReplaceAll(p, "/", `\`)
	out, err := t.runner.Run(t.ctx, readFileScript(t.root, rel))
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lastLine(out)))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", p, err)
	}
	return data, nil
}

// listFilesScript prints every file under root, relative and slash-separated,
// skipping version-control and build output directories that would swamp the
// listing (and are never manifests).
func listFilesScript(root string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	// Canonicalize the root: [System.IO.Path]::GetFullPath expands 8.3 short
	// names to their long form, which matches the FullName Get-ChildItem
	// reports below. Without this, a caller whose temp directory is a short
	// path (e.g. C:\Users\RUNNER~1\...) would produce a garbled relative path.
	fmt.Fprintf(&b, "$root = [System.IO.Path]::GetFullPath(%s)\n", psQuote(root))
	b.WriteString("if (-not (Test-Path -LiteralPath $root)) { throw \"repository directory not found: $root\" }\n")
	b.WriteString("$skip = '(^|/)(\\.git|node_modules|\\.venv|venv|__pycache__|\\.mypy_cache|\\.pytest_cache|bin|obj|\\.next|\\.nuxt|dist|build|\\.idea|\\.vscode|target)(/|$)'\n")
	// Kept to a single pipeline line: a multi-line script block is not reliably
	// executed when the script is fed to `powershell -Command -` on stdin.
	b.WriteString("Get-ChildItem -LiteralPath $root -Recurse -File -Force | ForEach-Object { $_.FullName.Substring($root.Length + 1).Replace('\\', '/') } | Where-Object { $_ -notmatch $skip }\n")
	return b.String()
}

// readFileScript prints the base64 of one file so binary-safe contents survive
// the WinRM text channel.
func readFileScript(root, rel string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$root = [System.IO.Path]::GetFullPath(%s)\n", psQuote(root))
	fmt.Fprintf(&b, "$p = Join-Path $root %s\n", psQuote(rel))
	b.WriteString("if (-not (Test-Path -LiteralPath $p)) { throw \"file not found: $p\" }\n")
	b.WriteString("[Convert]::ToBase64String([IO.File]::ReadAllBytes($p))\n")
	return b.String()
}
