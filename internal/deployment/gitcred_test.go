package deployment

import (
	"context"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
)

func TestGitAuthKindDetection(t *testing.T) {
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----"
	if !isPrivateKeyPEM(key) {
		t.Fatal("PEM key not detected")
	}
	if isPrivateKeyPEM("ghp_token123") {
		t.Fatal("token misdetected as PEM key")
	}
}

func TestGitAuthOptionQuoting(t *testing.T) {
	ssh := &GitAuth{Kind: GitAuthSSH, KeyPath: `C:\winify\app.gitkey`}
	opt := ssh.gitOption(psQuote)
	if !strings.Contains(opt, "core.sshCommand=ssh -i") || !strings.Contains(opt, "app.gitkey") {
		t.Errorf("ssh option = %q", opt)
	}
	http := &GitAuth{Kind: GitAuthHTTP, Token: "tok123"}
	opt = http.gitOption(shellQuote)
	if !strings.Contains(opt, "http.extraHeader=AUTHORIZATION: bearer tok123") {
		t.Errorf("http option = %q", opt)
	}
	if !http.Sensitive() || ssh.Sensitive() {
		t.Error("Sensitive(): token auth must be sensitive, key auth must not")
	}
	if (*GitAuth)(nil).Sensitive() {
		t.Error("nil auth must not be sensitive")
	}
}

func TestPrepareGitAuthNone(t *testing.T) {
	auth, err := PrepareGitAuth(context.Background(), &fakeRunner{}, fakeSecrets{value: "x"}, config.Project{}, "", false, noopLogf)
	if err != nil || auth != nil {
		t.Fatalf("auth = %+v, err = %v", auth, err)
	}
}

func TestPrepareGitAuthMissingStore(t *testing.T) {
	_, err := PrepareGitAuth(context.Background(), &fakeRunner{}, nil, config.Project{GitCredentialRef: "vault:x"}, "", false, noopLogf)
	if err == nil {
		t.Fatal("nil secret store accepted")
	}
}

func TestPrepareGitAuthTokenNeedsNoFile(t *testing.T) {
	runner := &fakeRunner{}
	auth, err := PrepareGitAuth(context.Background(), runner, fakeSecrets{value: "ghp_secret"}, config.Project{GitCredentialRef: "vault:git"}, "", false, noopLogf)
	if err != nil {
		t.Fatalf("PrepareGitAuth: %v", err)
	}
	if auth == nil || auth.Kind != GitAuthHTTP || auth.Token != "ghp_secret" {
		t.Fatalf("auth = %+v", auth)
	}
	if len(runner.commands) != 0 {
		t.Errorf("token auth should stage no files, commands = %v", runner.commands)
	}
}

func TestPrepareGitAuthSSHUploadsKey(t *testing.T) {
	runner := &fakeRunner{}
	auth, err := PrepareGitAuth(context.Background(), runner, fakeSecrets{value: "-----BEGIN OPENSSH PRIVATE KEY-----\nx\n-----END OPENSSH PRIVATE KEY-----"},
		config.Project{GitCredentialRef: "vault:git"}, "/opt/cc/app.gitkey", false, noopLogf)
	if err != nil {
		t.Fatalf("PrepareGitAuth: %v", err)
	}
	if auth.Kind != GitAuthSSH || auth.KeyPath != "/opt/cc/app.gitkey" {
		t.Fatalf("auth = %+v", auth)
	}
	joined := runner.joined()
	if !strings.Contains(joined, "chmod 600") || !strings.Contains(joined, "app.gitkey") {
		t.Errorf("key upload command missing restrictions:\n%s", joined)
	}
	if strings.Contains(joined, "PRIVATE KEY") {
		t.Error("key material appeared in the command (must be base64)")
	}
}

func TestSyncRepoScriptWithHTTPAuth(t *testing.T) {
	script := syncRepoScript(`C:\work\app`, "https://git.example.com/app.git", "main", &GitAuth{Kind: GitAuthHTTP, Token: "tok"})
	if !strings.Contains(script, "http.extraHeader") {
		t.Errorf("script missing extraHeader:\n%s", script)
	}
	// Every git call must carry the option.
	if strings.Contains(script, "git clone https://") {
		t.Error("clone call missing the credential option")
	}
}
