package detect

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// ---- Go ----

func detectGo(tree FileTree) *Plan {
	if !has(tree, "go.mod") {
		return nil
	}
	plan := &Plan{
		Language:     LanguageGo,
		Target:       TargetWindowsService,
		BuildRuntime: RuntimeGo,
		BuildCommand: "go build -o app.exe .",
		Exe:          "app.exe",
		HealthPath:   "/",
		Confidence:   ConfidenceHigh,
		Evidence:     []Evidence{{Path: "go.mod", Reason: "Go module"}},
	}
	if v := goDirective(read(tree, "go.mod")); v != "" {
		plan.RuntimeVersion = v
	}
	switch mains := mainPackages(tree); len(mains) {
	case 0:
		// A library or an unusual layout: keep the root build but be honest
		// about the guess.
		plan.Confidence = ConfidenceMedium
	case 1:
		if mains[0] != "." {
			plan.BuildCommand = "go build -o app.exe " + goPackageArg(mains[0])
			plan.Evidence = append(plan.Evidence, Evidence{Path: mains[0], Reason: "main package"})
		}
	default:
		plan.Confidence = ConfidenceMedium
		plan.Evidence = append(plan.Evidence, Evidence{Path: "cmd/", Reason: "several main packages; set the build target"})
	}
	return plan
}

// goDirective returns the version from the "go 1.22" line of go.mod.
func goDirective(gomod string) string {
	for _, line := range strings.Split(gomod, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	return ""
}

// mainPackages returns the directories holding a package main, root first.
func mainPackages(tree FileTree) []string {
	seen := map[string]bool{}
	for _, f := range sortedList(tree) {
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		if isMainPackage(read(tree, f)) {
			seen[moduleDir(f)] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// isMainPackage reports whether src's first code line declares package main.
func isMainPackage(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		return line == "package main"
	}
	return false
}

func goPackageArg(dir string) string {
	if dir == "." {
		return "."
	}
	return "./" + dir
}

// ---- .NET ----

var (
	tfmRe = regexp.MustCompile(`(?i)<TargetFramework[^>]*>([^<]+)<`)
	asmRe = regexp.MustCompile(`(?i)<AssemblyName[^>]*>([^<]+)<`)
)

func detectDotNet(tree FileTree) *Plan {
	proj := firstExt(tree, ".csproj")
	sln := firstExt(tree, ".sln")
	if proj == "" && sln == "" {
		return nil
	}
	plan := &Plan{
		Language:     LanguageDotNet,
		Framework:    ".NET",
		Target:       TargetWindowsService,
		BuildRuntime: RuntimeDotNet,
		RunRuntime:   RuntimeDotNet,
		BuildCommand: "dotnet publish -c Release -o out",
		Exe:          "dotnet",
		Port:         5000,
		HealthPath:   "/",
		Confidence:   ConfidenceMedium,
	}
	name := ""
	if proj != "" {
		name = strings.TrimSuffix(path.Base(proj), ".csproj")
		plan.Evidence = append(plan.Evidence, Evidence{Path: proj, Reason: ".NET project"})
		csproj := read(tree, proj)
		if tfm := firstGroup(tfmRe, csproj); tfm != "" {
			plan.RuntimeVersion = tfm
		}
		if asm := firstGroup(asmRe, csproj); asm != "" {
			name = asm
		}
	} else {
		plan.Evidence = append(plan.Evidence, Evidence{Path: sln, Reason: ".NET solution"})
		plan.Confidence = ConfidenceLow
	}
	if name != "" {
		plan.Args = `out\` + name + ".dll"
	}
	if has(tree, "web.config") {
		// An IIS-hosted app (ASP.NET Framework, or ASP.NET Core in-process):
		// publish files for IIS instead of running an exe as a service.
		plan.Target = TargetIIS
		plan.Exe = ""
		plan.Args = ""
		plan.RunRuntime = ""
		plan.Evidence = append(plan.Evidence, Evidence{Path: "web.config", Reason: "IIS-hosted application"})
	}
	return plan
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// ---- Python ----

const (
	pythonBuildRequirements = `python -m venv .venv; .\.venv\Scripts\python.exe -m pip install --upgrade pip; .\.venv\Scripts\python.exe -m pip install -r requirements.txt`
	pythonBuildProject      = `python -m venv .venv; .\.venv\Scripts\python.exe -m pip install --upgrade pip; .\.venv\Scripts\python.exe -m pip install .`
	pythonBuildVenvOnly     = `python -m venv .venv`
)

func detectPython(tree FileTree) *Plan {
	var evidence []Evidence
	for _, m := range []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py"} {
		if has(tree, m) {
			evidence = append(evidence, Evidence{Path: m, Reason: "Python manifest"})
		}
	}
	if len(evidence) == 0 && firstBase(tree, "manage.py") == "" && !hasPythonEntry(tree) {
		return nil
	}

	plan := &Plan{
		Language:     LanguagePython,
		Target:       TargetWindowsService,
		BuildRuntime: RuntimePython,
		RunRuntime:   RuntimePython,
		Exe:          `.venv\Scripts\python.exe`,
		HealthPath:   "/",
		Confidence:   ConfidenceHigh,
		Evidence:     evidence,
	}
	switch {
	case has(tree, "requirements.txt"):
		plan.BuildCommand = pythonBuildRequirements
	case has(tree, "pyproject.toml"):
		plan.BuildCommand = pythonBuildProject
	default:
		plan.BuildCommand = pythonBuildVenvOnly
		plan.Confidence = ConfidenceMedium
	}

	deps := strings.ToLower(read(tree, "requirements.txt") + "\n" + read(tree, "pyproject.toml") + "\n" + read(tree, "Pipfile"))
	switch {
	case has(tree, "manage.py"):
		plan.Framework = "Django"
		plan.Args = "manage.py runserver 127.0.0.1:8000"
		plan.Port = 8000
		plan.Evidence = append(plan.Evidence, Evidence{Path: "manage.py", Reason: "Django entry point"})
	case strings.Contains(deps, "uvicorn") || strings.Contains(deps, "fastapi"):
		plan.Framework = "FastAPI"
		plan.Args = fmt.Sprintf("-m uvicorn %s:app --host 127.0.0.1 --port 8000", pythonAppModule(tree))
		plan.Port = 8000
	case strings.Contains(deps, "flask"):
		plan.Framework = "Flask"
		plan.Args = fmt.Sprintf("-m flask --app %s run --host 127.0.0.1 --port 5000", strings.TrimSuffix(pythonEntry(tree), ".py"))
		plan.Port = 5000
	default:
		plan.Framework = "Python"
		plan.Args = pythonEntry(tree)
		plan.Port = 8000
		plan.Confidence = ConfidenceMedium
	}
	return plan
}

// hasPythonEntry reports whether the repository root looks like a Python app.
func hasPythonEntry(tree FileTree) bool {
	for _, name := range []string{"app.py", "main.py", "server.py", "run.py", "wsgi.py", "asgi.py"} {
		if has(tree, name) {
			return true
		}
	}
	for _, f := range tree.List() {
		if strings.HasSuffix(f, ".py") && !strings.Contains(f, "/") {
			return true
		}
	}
	return false
}

// pythonEntry picks the most likely script to run, preferring common names.
func pythonEntry(tree FileTree) string {
	for _, name := range []string{"app.py", "main.py", "server.py", "run.py", "wsgi.py", "asgi.py"} {
		if p := firstBase(tree, name); p != "" {
			return p
		}
	}
	if p := firstExt(tree, ".py"); p != "" {
		return p
	}
	return "app.py"
}

// pythonAppModule returns the importable module name holding the ASGI app.
func pythonAppModule(tree FileTree) string {
	for _, name := range []string{"app.py", "main.py", "asgi.py", "api.py"} {
		p := firstBase(tree, name)
		if p == "" {
			continue
		}
		src := read(tree, p)
		if strings.Contains(src, "FastAPI(") || strings.Contains(src, "app =") {
			return strings.TrimSuffix(name, ".py")
		}
	}
	return strings.TrimSuffix(path.Base(pythonEntry(tree)), ".py")
}

// ---- Node ----

func detectNode(tree FileTree) *Plan {
	if !has(tree, "package.json") {
		return nil
	}
	var pkg struct {
		Main            string            `json:"main"`
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	parsed := json.Unmarshal([]byte(read(tree, "package.json")), &pkg) == nil

	plan := &Plan{
		Language:     LanguageNode,
		Target:       TargetWindowsService,
		BuildRuntime: RuntimeNode,
		RunRuntime:   RuntimeNode,
		Exe:          "node.exe",
		Port:         3000,
		HealthPath:   "/",
		Confidence:   ConfidenceHigh,
		Evidence:     []Evidence{{Path: "package.json", Reason: "Node package"}},
	}
	if !parsed {
		plan.Confidence = ConfidenceLow
		plan.Args = nodeEntry(tree)
		return plan
	}

	build := "npm ci"
	if _, ok := pkg.Scripts["build"]; ok {
		build += "; npm run build"
	}
	plan.BuildCommand = build

	deps := map[string]bool{}
	for k := range pkg.Dependencies {
		deps[strings.ToLower(k)] = true
	}
	for k := range pkg.DevDependencies {
		deps[strings.ToLower(k)] = true
	}

	switch {
	case deps["next"]:
		plan.Framework = "Next.js"
		plan.Args = `node_modules\next\dist\bin\next start -p 3000`
		plan.Confidence = ConfidenceMedium
	case deps["vite"]:
		// A Vite build is static output: serve it rather than running a process.
		plan.Framework = "Vite"
		plan.SourceSubdir = "dist"
		plan.CaddyMode = "static"
		plan.Exe = ""
		plan.Args = ""
		plan.RunRuntime = ""
		plan.Port = 8080
		plan.Confidence = ConfidenceMedium
	default:
		if deps["express"] {
			plan.Framework = "Express"
		} else {
			plan.Framework = "Node"
		}
		entry := pkg.Main
		if entry == "" {
			entry = nodeEntry(tree)
		}
		plan.Args = entry
	}
	return plan
}

// nodeEntry picks the most likely Node entry script.
func nodeEntry(tree FileTree) string {
	for _, name := range []string{"server.js", "index.js", "app.js", "main.js", "index.mjs"} {
		if p := firstBase(tree, name); p != "" {
			return p
		}
	}
	return "index.js"
}

// ---- Static ----

func detectStatic(tree FileTree) *Plan {
	if !has(tree, "index.html") {
		return nil
	}
	return &Plan{
		Language:   LanguageStatic,
		Framework:  "static site",
		Target:     TargetWindowsService,
		CaddyMode:  "static",
		Port:       8080,
		HealthPath: "/",
		Confidence: ConfidenceMedium,
		Evidence:   []Evidence{{Path: "index.html", Reason: "static entry point"}},
	}
}
