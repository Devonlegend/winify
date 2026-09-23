package detect

import (
	"errors"
	"strings"
	"testing"
)

// memTree is an in-memory FileTree for tests.
type memTree map[string]string

func (m memTree) List() []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (m memTree) Read(p string) ([]byte, error) {
	s, ok := m[p]
	if !ok {
		return nil, errors.New("not found: " + p)
	}
	return []byte(s), nil
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name          string
		files         memTree
		wantNil       bool
		wantLanguage  Language
		wantFramework string
		wantExe       string
		wantArgs      string
		wantPort      int
		wantCaddy     string
		wantBuild     string
	}{
		{
			name:         "go root main",
			files:        memTree{"go.mod": "module x\n\ngo 1.22\n", "main.go": "package main\n\nfunc main() {}\n"},
			wantLanguage: LanguageGo,
			wantExe:      "app.exe",
			wantBuild:    "go build -o app.exe .",
		},
		{
			name:         "go cmd layout",
			files:        memTree{"go.mod": "module x\n", "cmd/api/main.go": "package main\nfunc main() {}\n"},
			wantLanguage: LanguageGo,
			wantExe:      "app.exe",
			wantBuild:    "go build -o app.exe ./cmd/api",
		},
		{
			name:         "dotnet",
			files:        memTree{"App.csproj": "<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>"},
			wantLanguage: LanguageDotNet,
			wantExe:      "dotnet",
			wantArgs:     `out\App.dll`,
			wantPort:     5000,
		},
		{
			name:          "python fastapi",
			files:         memTree{"requirements.txt": "fastapi\nuvicorn\n", "app.py": "from fastapi import FastAPI\napp = FastAPI()\n"},
			wantLanguage:  LanguagePython,
			wantFramework: "FastAPI",
			wantExe:       `.venv\Scripts\python.exe`,
			wantArgs:      "-m uvicorn app:app --host 127.0.0.1 --port 8000",
			wantPort:      8000,
			wantBuild:     "pip install -r requirements.txt",
		},
		{
			name:          "python django",
			files:         memTree{"requirements.txt": "Django\n", "manage.py": "import django\n"},
			wantLanguage:  LanguagePython,
			wantFramework: "Django",
			wantArgs:      "manage.py runserver 127.0.0.1:8000",
			wantPort:      8000,
		},
		{
			name:          "python flask",
			files:         memTree{"requirements.txt": "Flask\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n"},
			wantLanguage:  LanguagePython,
			wantFramework: "Flask",
			wantArgs:      "-m flask --app app run --host 127.0.0.1 --port 5000",
			wantPort:      5000,
		},
		{
			name:          "node express",
			files:         memTree{"package.json": `{"main":"server.js","dependencies":{"express":"^4"}}`, "server.js": "require('express')"},
			wantLanguage:  LanguageNode,
			wantFramework: "Express",
			wantExe:       "node.exe",
			wantArgs:      "server.js",
			wantPort:      3000,
			wantBuild:     "npm ci",
		},
		{
			name:          "node next",
			files:         memTree{"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"14"}}`},
			wantLanguage:  LanguageNode,
			wantFramework: "Next.js",
			wantBuild:     "npm run build",
		},
		{
			name:          "node vite is static",
			files:         memTree{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"5"}}`},
			wantLanguage:  LanguageNode,
			wantFramework: "Vite",
			wantPort:      8080,
			wantCaddy:     "static",
		},
		{
			name:         "static",
			files:        memTree{"index.html": "<html></html>", "style.css": "body{}"},
			wantLanguage: LanguageStatic,
			wantCaddy:    "static",
			wantPort:     8080,
		},
		{
			name:    "empty",
			files:   memTree{},
			wantNil: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Detect(c.files)
			if c.wantNil {
				if got != nil {
					t.Fatalf("Detect = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("Detect = nil, want %s", c.wantLanguage)
			}
			if got.Language != c.wantLanguage {
				t.Errorf("language = %q, want %q", got.Language, c.wantLanguage)
			}
			if c.wantFramework != "" && got.Framework != c.wantFramework {
				t.Errorf("framework = %q, want %q", got.Framework, c.wantFramework)
			}
			if c.wantExe != "" && got.Exe != c.wantExe {
				t.Errorf("exe = %q, want %q", got.Exe, c.wantExe)
			}
			if c.wantArgs != "" && got.Args != c.wantArgs {
				t.Errorf("args = %q, want %q", got.Args, c.wantArgs)
			}
			if c.wantPort != 0 && got.Port != c.wantPort {
				t.Errorf("port = %d, want %d", got.Port, c.wantPort)
			}
			if c.wantCaddy != "" && got.CaddyMode != c.wantCaddy {
				t.Errorf("caddy mode = %q, want %q", got.CaddyMode, c.wantCaddy)
			}
			if c.wantBuild != "" && !strings.Contains(got.BuildCommand, c.wantBuild) {
				t.Errorf("build command = %q, want substring %q", got.BuildCommand, c.wantBuild)
			}
		})
	}
}

func TestDetectTargetRouting(t *testing.T) {
	cases := []struct {
		name  string
		files memTree
		want  string
	}{
		{"python is a service", memTree{"requirements.txt": "flask\n", "app.py": "x"}, TargetWindowsService},
		{"go is a service", memTree{"go.mod": "module x\n", "main.go": "package main\n"}, TargetWindowsService},
		{"static is a service (caddy)", memTree{"index.html": "<html>"}, TargetWindowsService},
		{"dotnet is a service", memTree{"App.csproj": "<Project/>"}, TargetWindowsService},
		{"dotnet with web.config is IIS", memTree{"App.csproj": "<Project/>", "web.config": "<configuration/>"}, TargetIIS},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := Detect(c.files)
			if plan == nil {
				t.Fatal("Detect = nil")
			}
			if plan.Target != c.want {
				t.Fatalf("target = %q, want %q", plan.Target, c.want)
			}
		})
	}
}

func TestDetectStaticHasNoExe(t *testing.T) {
	plan := Detect(memTree{"index.html": "<html>"})
	if plan == nil {
		t.Fatal("Detect = nil")
	}
	if plan.Exe != "" || plan.CaddyMode != "static" {
		t.Fatalf("plan = %+v, want static with no exe", plan)
	}
}

func TestDetectViteIsStatic(t *testing.T) {
	plan := Detect(memTree{"package.json": `{"devDependencies":{"vite":"5"}}`})
	if plan == nil {
		t.Fatal("Detect = nil, want a plan")
	}
	if plan.CaddyMode != "static" || plan.Exe != "" || plan.RunRuntime != "" {
		t.Fatalf("vite plan = %+v, want static with no run runtime", plan)
	}
}

func TestDetectPriorityBackendWins(t *testing.T) {
	files := memTree{
		"go.mod":       "module x\n",
		"main.go":      "package main\nfunc main() {}\n",
		"package.json": `{"dependencies":{"express":"4"}}`,
		"index.html":   "<html></html>",
	}
	if got := Detect(files); got == nil || got.Language != LanguageGo {
		t.Fatalf("Detect = %+v, want go (highest priority)", got)
	}
}

func TestDetectMalformedPackageJSON(t *testing.T) {
	plan := Detect(memTree{"package.json": "{not json", "server.js": ""})
	if plan == nil || plan.Language != LanguageNode {
		t.Fatalf("plan = %+v, want node", plan)
	}
	if plan.Confidence != ConfidenceLow {
		t.Errorf("confidence = %q, want low", plan.Confidence)
	}
	if plan.Args != "server.js" {
		t.Errorf("args = %q, want server.js", plan.Args)
	}
}

func TestDetectBarePythonScript(t *testing.T) {
	plan := Detect(memTree{"main.py": "print('hi')\n"})
	if plan == nil || plan.Language != LanguagePython {
		t.Fatalf("plan = %+v, want python", plan)
	}
	if plan.Args != "main.py" {
		t.Errorf("args = %q, want main.py", plan.Args)
	}
	if plan.Confidence != ConfidenceMedium {
		t.Errorf("confidence = %q, want medium", plan.Confidence)
	}
}

func TestDetectRecordsEvidence(t *testing.T) {
	plan := Detect(memTree{"requirements.txt": "flask\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n"})
	if plan == nil || len(plan.Evidence) == 0 {
		t.Fatalf("plan = %+v, want evidence", plan)
	}
	if plan.Evidence[0].Path != "requirements.txt" {
		t.Errorf("evidence[0] = %+v, want requirements.txt", plan.Evidence[0])
	}
}

func TestDetectGoLibraryIsMediumConfidence(t *testing.T) {
	plan := Detect(memTree{"go.mod": "module x\n", "lib.go": "package x\n"})
	if plan == nil || plan.Language != LanguageGo {
		t.Fatalf("plan = %+v, want go", plan)
	}
	if plan.Confidence != ConfidenceMedium {
		t.Errorf("confidence = %q, want medium", plan.Confidence)
	}
}
