// Package config loads application settings from an optional YAML file and
// from environment variables layered on top, plus the deployment inventory
// (servers.yaml, projects.yaml). It never holds decrypted secrets: server
// entries carry only credential *refs* that internal/auth resolves at
// connection time.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Target types. A project's server type selects the deploy pipeline.
const (
	ServerTypeDocker = "docker"
	ServerTypeIIS    = "iis"
	// ServerTypeWindowsService runs a native Windows executable as a service
	// managed by NSSM over WinRM, optionally fronted by Caddy on the target.
	// It is the lightweight alternative to IIS for non-IIS Windows workloads.
	ServerTypeWindowsService = "winsvc"
)

// Caddy modes for a winsvc project's per-target Caddy. "none" leaves Caddy off
// the target and relies on the central reverse proxy only.
const (
	CaddyModeNone   = "none"
	CaddyModeProxy  = "proxy"
	CaddyModeStatic = "static"
)

// Docker deploy sources. A Docker project builds a Dockerfile, deploys the
// repository's own compose file, or runs a prebuilt registry image.
const (
	ProjectSourceDockerfile = "dockerfile"
	ProjectSourceCompose    = "compose"
	ProjectSourceImage      = "image"
)

// Config is the top-level application configuration.
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Database    DatabaseConfig    `yaml:"database"`
	Auth        AuthConfig        `yaml:"auth"`
	Credentials CredentialsConfig `yaml:"credentials"`
	Files       FilesConfig       `yaml:"files"`
	Proxy       ProxyConfig       `yaml:"proxy"`
	Deploy      DeployConfig      `yaml:"deploy"`
	Bootstrap   BootstrapConfig   `yaml:"bootstrap"`
	Monitoring  MonitoringConfig  `yaml:"monitoring"`
	Assistant   AssistantConfig   `yaml:"assistant"`
	GitHub      GitHubConfig      `yaml:"github"`
}

// GitHubConfig configures the GitHub App used for automatic webhook
// registration. The App needs Administration (read/write) on repositories it
// manages, and must be installed on those repositories.
type GitHubConfig struct {
	// AppID is the GitHub App's numeric ID.
	AppID string `yaml:"app_id"`
	// InstallationID is the App installation on the account/org.
	InstallationID int64 `yaml:"installation_id"`
	// PrivateKeyRef names the encrypted credential holding the App's PEM
	// private key (for example vault:github-app-key).
	PrivateKeyRef string `yaml:"private_key_ref"`
	// APIURL defaults to https://api.github.com; set it for GitHub Enterprise.
	APIURL string `yaml:"api_url"`
}

// GitHubEnabled reports whether automatic webhook registration is configured.
func (g GitHubConfig) Enabled() bool {
	return g.AppID != "" && g.InstallationID != 0 && g.PrivateKeyRef != ""
}

// BootstrapConfig controls zero-touch provisioning of the host winify runs on.
type BootstrapConfig struct {
	// Enabled allows the bootstrap command and first-run trigger.
	Enabled bool `yaml:"enabled"`
	// InstallDir is the on-host root. Empty means %ProgramData%\winify on
	// Windows (or /var/lib/winify elsewhere).
	InstallDir string `yaml:"install_dir"`
	// ServiceName is the Windows service name winify installs for itself.
	ServiceName string `yaml:"service_name"`
	// EnableWinRM enables PowerShell Remoting so the host is a deploy target.
	EnableWinRM bool `yaml:"enable_winrm"`
	// Caddy provisions the reverse proxy on this host.
	Caddy CaddyBootstrapConfig `yaml:"caddy"`
}

// CaddyBootstrapConfig configures Caddy provisioning during bootstrap.
type CaddyBootstrapConfig struct {
	// Enabled installs Caddy (binary + service) and opens 80/443. Requires
	// Source or URL to supply the binary.
	Enabled bool `yaml:"enabled"`
	// Source is a local path on the control-center host to caddy.exe.
	Source string `yaml:"source"`
	// URL downloads a Caddy release zip when Source is empty.
	URL string `yaml:"url"`
	// SHA256 optionally pins the expected hash of the binary.
	SHA256 string `yaml:"sha256"`
	// Admin is the Caddy admin API address.
	Admin string `yaml:"admin"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	// Addr is the listen address in host:port form, e.g. ":8080".
	Addr string `yaml:"addr"`
	// PublicURL is the externally reachable base URL (e.g. https://cc.example.com),
	// used for provider webhook callbacks. Empty derives it from the request host.
	PublicURL string `yaml:"public_url"`
}

// DatabaseConfig locates the SQLite file.
type DatabaseConfig struct {
	// Path is the SQLite database file, created on first run if missing.
	Path string `yaml:"path"`
}

// AuthConfig configures the single v1 admin account and its sessions.
type AuthConfig struct {
	AdminUser string `yaml:"admin_user"`
	// AdminPasswordHash is a bcrypt hash. The plaintext never lives in config;
	// set CC_ADMIN_PASSWORD instead to have the server hash it at startup.
	AdminPasswordHash string `yaml:"admin_password_hash"`
	// SetupToken protects the public first-run registration page when the
	// listener is exposed beyond loopback. An empty value makes the server
	// generate a one-time token and print it to its startup log.
	SetupToken string `yaml:"setup_token" json:"-"`
	// CookieSecure sets the Secure flag on the session cookie. Keep true in
	// production; browsers still accept Secure cookies on http://localhost.
	CookieSecure bool `yaml:"cookie_secure"`
	// SessionTTLHours is how long a login lasts.
	SessionTTLHours int `yaml:"session_ttl_hours"`
}

// CredentialsConfig holds the master key used to encrypt credential values.
type CredentialsConfig struct {
	// MasterKey is a base64-encoded 32-byte AES key. Prefer the CC_MASTER_KEY
	// env var; if empty, a key is generated and stored in the data directory.
	MasterKey string `yaml:"master_key"`
}

// FilesConfig points at the deployment inventory files.
type FilesConfig struct {
	Servers  string `yaml:"servers"`
	Projects string `yaml:"projects"`
}

// ProxyConfig configures registration with the reverse proxy (Caddy) that
// fronts every deployment with automatic HTTPS.
type ProxyConfig struct {
	// Enabled turns on proxy registration. Defaults to true because bootstrap
	// installs Caddy; set false when there is no reverse proxy (deploys then
	// run but register no public URL).
	Enabled bool `yaml:"enabled"`
	// AdminURL is the Caddy admin API endpoint.
	AdminURL string `yaml:"admin_url"`
	// ServerName is the Caddy HTTP server object routes are added to.
	ServerName string `yaml:"server_name"`
	// PublicIP is the public address of the central Caddy ingress. It is
	// separate from a deployment target's PublicIP because remote targets are
	// usually behind the controller's proxy.
	PublicIP string `yaml:"public_ip"`
}

// MonitoringConfig controls the metrics poller.
type MonitoringConfig struct {
	// Enabled turns the background metrics scheduler on or off.
	Enabled bool `yaml:"enabled"`
	// IntervalSeconds is how often each server is polled.
	IntervalSeconds int `yaml:"interval_seconds"`
	// TimeoutSeconds bounds a single server's collection.
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// RetentionHours is how long samples are kept before pruning.
	RetentionHours int `yaml:"retention_hours"`
	// HistoryPoints caps the points returned for charts.
	HistoryPoints int `yaml:"history_points"`
}

// AssistantConfig configures the local RAG assistant (ChromaDB + Ollama).
type AssistantConfig struct {
	// Enabled turns the /assistant/ask endpoint and ingestion on.
	Enabled bool `yaml:"enabled"`
	// ChromaURL is the ChromaDB server. Empty or unreachable falls back to an
	// in-process store.
	ChromaURL string `yaml:"chroma_url"`
	// Collection is the ChromaDB collection holding the curated docs.
	Collection string `yaml:"collection"`
	// OllamaURL is the Ollama server.
	OllamaURL string `yaml:"ollama_url"`
	// Model is the local generation model.
	Model string `yaml:"model"`
	// TimeoutSeconds bounds a single answer (retrieval + generation).
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// TopK is how many doc chunks to retrieve per question.
	TopK int `yaml:"top_k"`
}

// DeployConfig controls how commands run on target servers.
type DeployConfig struct {
	// WorkDir is the parent directory on Docker/Linux targets for cloned repos.
	WorkDir string `yaml:"work_dir"`
	// IISWorkDir is the parent directory on Windows targets for cloned repos.
	// Separate from WorkDir because the two target types use different path
	// syntaxes (POSIX vs Windows).
	IISWorkDir string `yaml:"iis_work_dir"`
	// IISBackupDir is where timestamped pre-deploy copies of the live IIS
	// directory are stored for rollback.
	IISBackupDir string `yaml:"iis_backup_dir"`
	// TargetRoot bounds destructive live-directory copies on Windows targets.
	// Set AllowExternalTargetPaths only for a deliberately managed IIS/site
	// root outside this tree.
	TargetRoot               string `yaml:"target_root"`
	AllowExternalTargetPaths bool   `yaml:"allow_external_target_paths"`
	// NSSMSource is the path on the control-center host to the nssm.exe that
	// gets uploaded to a winsvc target on first deploy (public-domain binary).
	// The target path is Server.NSSMPath. Empty disables upload and requires
	// NSSM to already be present on the target.
	NSSMSource string `yaml:"nssm_source"`
	// NSSMSHA256 pins the expected SHA-256 (hex) of the uploaded nssm.exe.
	// Empty skips the check; set it to detect a tampered/incorrect binary.
	NSSMSHA256 string `yaml:"nssm_sha256"`
	// KnownHostsFile enables SSH host-key verification. It should point to a
	// file maintained from a trusted provisioning source.
	KnownHostsFile string `yaml:"known_hosts_file"`
	// AllowInsecureHostKey is an explicit emergency escape hatch. It is false
	// by default; never enable it for production targets.
	AllowInsecureHostKey bool `yaml:"allow_insecure_host_key"`
	// HealthTimeoutSeconds bounds how long the post-deploy health check waits.
	HealthTimeoutSeconds int `yaml:"health_timeout_seconds"`
	// HealthIntervalSeconds is the delay between health-check attempts.
	HealthIntervalSeconds int `yaml:"health_interval_seconds"`
	// RetentionDays bounds deployment/audit history growth.
	RetentionDays int `yaml:"retention_days"`
	// TimeoutMinutes bounds a whole deploy/rollback so a hung remote command
	// cannot leave a deployment stuck in "running" forever.
	TimeoutMinutes int `yaml:"timeout_minutes"`
}

// Default returns the built-in configuration used when a field is not set
// in YAML and not overridden by the environment.
func Default() Config {
	return Config{
		Server:   ServerConfig{Addr: "127.0.0.1:8080"},
		Database: DatabaseConfig{Path: "data/control-center.db"},
		Auth: AuthConfig{
			AdminUser:       "admin",
			CookieSecure:    true,
			SessionTTLHours: 12,
		},
		Files: FilesConfig{Servers: "servers.yaml", Projects: "projects.yaml"},
		Proxy: ProxyConfig{
			// On by default: bootstrap provisions Caddy, so deploys should
			// register their domain and get automatic HTTPS. Set false only
			// when there is no reverse proxy (deploys then skip registration).
			Enabled:    true,
			AdminURL:   "http://127.0.0.1:2019",
			ServerName: "srv0",
		},
		Deploy: DeployConfig{
			WorkDir:               "/opt/control-center",
			IISWorkDir:            `C:\ProgramData\winify\work`,
			IISBackupDir:          `C:\ProgramData\winify\backups`,
			TargetRoot:            `C:\ProgramData\winify\apps`,
			NSSMSource:            "tools/nssm.exe",
			HealthTimeoutSeconds:  60,
			HealthIntervalSeconds: 3,
			RetentionDays:         90,
			TimeoutMinutes:        30,
		},
		Bootstrap: BootstrapConfig{
			Enabled:     true,
			ServiceName: "winify",
			EnableWinRM: false,
			Caddy: CaddyBootstrapConfig{
				Enabled: true,
				URL:     "https://github.com/caddyserver/caddy/releases/download/v2.11.4/caddy_2.11.4_windows_amd64.zip",
				SHA256:  "5CB9AB71E5756CE72840B8234177A2F40C8B4AB47A806B8E841E2B784E9DF62B",
				Admin:   "127.0.0.1:2019",
			},
		},
		Monitoring: MonitoringConfig{
			Enabled:         true,
			IntervalSeconds: 30,
			TimeoutSeconds:  15,
			RetentionHours:  24,
			HistoryPoints:   200,
		},
		Assistant: AssistantConfig{
			Enabled:        true,
			ChromaURL:      "http://127.0.0.1:8000",
			Collection:     "control-center-docs",
			OllamaURL:      "http://127.0.0.1:11434",
			Model:          "gemma3:1b",
			TimeoutSeconds: 60,
			TopK:           3,
		},
	}
}

// Load reads the YAML file at path (if it exists) over the defaults, then
// layers environment overrides on top.
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		// yaml.Unmarshal merges into cfg: keys absent from the file keep
		// their default values, so a partial config file is fine.
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// No file: run on defaults. Not an error.
	default:
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}

	// Resolve paths from the YAML file before applying environment overrides:
	// CLI/library callers commonly set CC_* paths relative to their current
	// working directory, while file-backed settings should be stable when the
	// service starts with System32 as its working directory.
	if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
		abs, absErr := filepath.Abs(path)
		if absErr != nil {
			return cfg, fmt.Errorf("resolve config path %s: %w", path, absErr)
		}
		resolveConfigPaths(&cfg, filepath.Dir(abs))
	}
	if err := cfg.applyEnv(); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate checks settings that would otherwise fail much later during a
// deployment or monitoring pass. It is intentionally independent of the
// database so a malformed service configuration cannot start in a half-ready
// state.
func (cfg Config) Validate() error {
	if _, port, err := net.SplitHostPort(cfg.Server.Addr); err != nil {
		return fmt.Errorf("server.addr must be host:port: %w", err)
	} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return errors.New("server.addr port must be between 1 and 65535")
	}
	if cfg.Server.PublicURL != "" {
		u, err := url.Parse(cfg.Server.PublicURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return errors.New("server.public_url must be an http or https URL")
		}
	}
	if cfg.Auth.SessionTTLHours < 1 {
		return errors.New("auth.session_ttl_hours must be positive")
	}
	if cfg.Deploy.WorkDir == "" {
		return errors.New("deploy.work_dir is required")
	}
	if strings.TrimSpace(cfg.Deploy.TargetRoot) == "" {
		return errors.New("deploy.target_root is required")
	}
	if cfg.Deploy.HealthTimeoutSeconds < 1 || cfg.Deploy.HealthIntervalSeconds < 1 {
		return errors.New("deploy health timeout and interval must be positive")
	}
	if cfg.Deploy.RetentionDays < 1 {
		return errors.New("deploy.retention_days must be positive")
	}
	if cfg.Deploy.TimeoutMinutes < 1 {
		return errors.New("deploy.timeout_minutes must be positive")
	}
	if cfg.Monitoring.Enabled {
		if cfg.Monitoring.IntervalSeconds < 1 || cfg.Monitoring.TimeoutSeconds < 1 || cfg.Monitoring.RetentionHours < 1 || cfg.Monitoring.HistoryPoints < 1 {
			return errors.New("enabled monitoring intervals, retention and history_points must be positive")
		}
	}
	if strings.TrimSpace(cfg.Proxy.ServerName) == "" || strings.ContainsAny(cfg.Proxy.ServerName, "/\\\r\n ") {
		return errors.New("proxy.server_name must be a simple Caddy server object name")
	}
	if cfg.Proxy.Enabled && strings.TrimSpace(cfg.Proxy.AdminURL) == "" {
		return errors.New("proxy.admin_url is required when proxy.enabled is true")
	}
	if cfg.Proxy.AdminURL != "" {
		u, err := url.Parse(cfg.Proxy.AdminURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("proxy.admin_url must be an http or https URL")
		}
		if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
			return errors.New("proxy.admin_url must use HTTPS unless it points to loopback")
		}
	}
	if cfg.Bootstrap.Caddy.Admin != "" {
		if err := ValidateCaddyAdmin(cfg.Bootstrap.Caddy.Admin); err != nil {
			return err
		}
	}
	if cfg.Bootstrap.Caddy.URL != "" {
		u, err := url.Parse(cfg.Bootstrap.Caddy.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("bootstrap.caddy.url must be an HTTPS URL")
		}
	}
	if cfg.GitHub.AppID != "" || cfg.GitHub.InstallationID != 0 || cfg.GitHub.PrivateKeyRef != "" {
		if !cfg.GitHub.Enabled() {
			return errors.New("github requires app_id, installation_id and private_key_ref together")
		}
	}
	if cfg.GitHub.APIURL != "" {
		u, err := url.Parse(cfg.GitHub.APIURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return errors.New("github.api_url must be an http or https URL")
		}
	}
	return nil
}

// ValidateCaddyAdmin rejects an unauthenticated admin API exposed on a
// non-loopback HTTP interface. HTTPS may be used for a protected remote admin.
func ValidateCaddyAdmin(admin string) error {
	if !validCaddyAdmin(admin) {
		return errors.New("caddy admin address must be loopback HTTP or HTTPS")
	}
	return nil
}

func validCaddyAdmin(admin string) bool {
	value := strings.TrimSpace(admin)
	if value == "" {
		return true
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname()))
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func looksLikeWindowsAbs(value string) bool {
	if strings.HasPrefix(value, `\\`) {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func resolveConfigPaths(cfg *Config, base string) {
	resolve := func(value string) string {
		value = strings.TrimSpace(value)
		if value == "" || value == ":memory:" || filepath.IsAbs(value) || looksLikeWindowsAbs(value) || strings.HasPrefix(value, "/") {
			return value
		}
		return filepath.Clean(filepath.Join(base, value))
	}
	cfg.Database.Path = resolve(cfg.Database.Path)
	cfg.Files.Servers = resolve(cfg.Files.Servers)
	cfg.Files.Projects = resolve(cfg.Files.Projects)
	// Credentials.MasterKey is base64 key material, not a filesystem path.
	// Leave it untouched; the generated key path is derived from Database.Path.
	cfg.Deploy.WorkDir = resolve(cfg.Deploy.WorkDir)
	cfg.Deploy.IISWorkDir = resolve(cfg.Deploy.IISWorkDir)
	cfg.Deploy.IISBackupDir = resolve(cfg.Deploy.IISBackupDir)
	cfg.Deploy.NSSMSource = resolve(cfg.Deploy.NSSMSource)
	cfg.Deploy.KnownHostsFile = resolve(cfg.Deploy.KnownHostsFile)
	cfg.Bootstrap.InstallDir = resolve(cfg.Bootstrap.InstallDir)
	cfg.Bootstrap.Caddy.Source = resolve(cfg.Bootstrap.Caddy.Source)
}

// applyEnv overrides selected fields from environment variables, so secrets
// and per-environment values can avoid the config file entirely. Secret
// values (master key, admin password) are read here or in main, never logged.
func (cfg *Config) applyEnv() error {
	if v := os.Getenv("CC_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("CC_PUBLIC_URL"); v != "" {
		cfg.Server.PublicURL = strings.TrimRight(v, "/")
	}
	if v := os.Getenv("CC_DB_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := os.Getenv("CC_ADMIN_USER"); v != "" {
		cfg.Auth.AdminUser = v
	}
	if v := os.Getenv("CC_SETUP_TOKEN"); v != "" {
		cfg.Auth.SetupToken = v
	}
	if v := os.Getenv("CC_MASTER_KEY"); v != "" {
		cfg.Credentials.MasterKey = v
	}
	if v := os.Getenv("CC_SERVERS_FILE"); v != "" {
		cfg.Files.Servers = v
	}
	if v := os.Getenv("CC_PROJECTS_FILE"); v != "" {
		cfg.Files.Projects = v
	}
	if v := os.Getenv("CC_COOKIE_SECURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_COOKIE_SECURE: %w", err)
		}
		cfg.Auth.CookieSecure = b
	}
	if v := os.Getenv("CC_PROXY_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_PROXY_ENABLED: %w", err)
		}
		cfg.Proxy.Enabled = b
	}
	if v := os.Getenv("CC_PROXY_ADMIN_URL"); v != "" {
		cfg.Proxy.AdminURL = v
	}
	if v := os.Getenv("CC_PROXY_PUBLIC_IP"); v != "" {
		cfg.Proxy.PublicIP = v
	}
	if v := os.Getenv("CC_DEPLOY_WORKDIR"); v != "" {
		cfg.Deploy.WorkDir = v
	}
	if v := os.Getenv("CC_DEPLOY_IIS_WORKDIR"); v != "" {
		cfg.Deploy.IISWorkDir = v
	}
	if v := os.Getenv("CC_DEPLOY_IIS_BACKUP_DIR"); v != "" {
		cfg.Deploy.IISBackupDir = v
	}
	if v := os.Getenv("CC_DEPLOY_TARGET_ROOT"); v != "" {
		cfg.Deploy.TargetRoot = v
	}
	if v := os.Getenv("CC_ALLOW_EXTERNAL_TARGET_PATHS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_ALLOW_EXTERNAL_TARGET_PATHS: %w", err)
		}
		cfg.Deploy.AllowExternalTargetPaths = b
	}
	if v := os.Getenv("CC_DEPLOY_NSSM_SOURCE"); v != "" {
		cfg.Deploy.NSSMSource = v
	}
	if v := os.Getenv("CC_DEPLOY_NSSM_SHA256"); v != "" {
		cfg.Deploy.NSSMSHA256 = v
	}
	if v := os.Getenv("CC_DEPLOY_KNOWN_HOSTS"); v != "" {
		cfg.Deploy.KnownHostsFile = v
	}
	if v := os.Getenv("CC_ALLOW_INSECURE_SSH"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_ALLOW_INSECURE_SSH: %w", err)
		}
		cfg.Deploy.AllowInsecureHostKey = b
	}
	if v := os.Getenv("CC_DEPLOY_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CC_DEPLOY_RETENTION_DAYS: %w", err)
		}
		cfg.Deploy.RetentionDays = n
	}
	if v := os.Getenv("CC_DEPLOY_TIMEOUT_MINUTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CC_DEPLOY_TIMEOUT_MINUTES: %w", err)
		}
		cfg.Deploy.TimeoutMinutes = n
	}
	if v := os.Getenv("CC_BOOTSTRAP_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_BOOTSTRAP_ENABLED: %w", err)
		}
		cfg.Bootstrap.Enabled = b
	}
	if v := os.Getenv("CC_BOOTSTRAP_INSTALL_DIR"); v != "" {
		cfg.Bootstrap.InstallDir = v
	}
	if v := os.Getenv("CC_BOOTSTRAP_ENABLE_WINRM"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_BOOTSTRAP_ENABLE_WINRM: %w", err)
		}
		cfg.Bootstrap.EnableWinRM = b
	}
	if v := os.Getenv("CC_BOOTSTRAP_CADDY_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_BOOTSTRAP_CADDY_ENABLED: %w", err)
		}
		cfg.Bootstrap.Caddy.Enabled = b
	}
	if v := os.Getenv("CC_BOOTSTRAP_CADDY_SOURCE"); v != "" {
		cfg.Bootstrap.Caddy.Source = v
	}
	if v := os.Getenv("CC_BOOTSTRAP_CADDY_URL"); v != "" {
		cfg.Bootstrap.Caddy.URL = v
	}
	if v := os.Getenv("CC_BOOTSTRAP_CADDY_SHA256"); v != "" {
		cfg.Bootstrap.Caddy.SHA256 = v
	}
	if v := os.Getenv("CC_MONITOR_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_MONITOR_ENABLED: %w", err)
		}
		cfg.Monitoring.Enabled = b
	}
	if v := os.Getenv("CC_MONITOR_INTERVAL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CC_MONITOR_INTERVAL: %w", err)
		}
		cfg.Monitoring.IntervalSeconds = n
	}
	if v := os.Getenv("CC_MONITOR_TIMEOUT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CC_MONITOR_TIMEOUT: %w", err)
		}
		cfg.Monitoring.TimeoutSeconds = n
	}
	if v := os.Getenv("CC_MONITOR_RETENTION_HOURS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CC_MONITOR_RETENTION_HOURS: %w", err)
		}
		cfg.Monitoring.RetentionHours = n
	}
	if v := os.Getenv("CC_ASSISTANT_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_ASSISTANT_ENABLED: %w", err)
		}
		cfg.Assistant.Enabled = b
	}
	if v := os.Getenv("CC_CHROMA_URL"); v != "" {
		cfg.Assistant.ChromaURL = v
	}
	if v := os.Getenv("CC_ASSISTANT_COLLECTION"); v != "" {
		cfg.Assistant.Collection = v
	}
	if v := os.Getenv("CC_OLLAMA_URL"); v != "" {
		cfg.Assistant.OllamaURL = v
	}
	if v := os.Getenv("CC_ASSISTANT_MODEL"); v != "" {
		cfg.Assistant.Model = v
	}
	if v := os.Getenv("CC_ASSISTANT_TIMEOUT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CC_ASSISTANT_TIMEOUT: %w", err)
		}
		cfg.Assistant.TimeoutSeconds = n
	}
	if v := os.Getenv("CC_ASSISTANT_TOP_K"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CC_ASSISTANT_TOP_K: %w", err)
		}
		cfg.Assistant.TopK = n
	}
	if v := os.Getenv("CC_GITHUB_APP_ID"); v != "" {
		cfg.GitHub.AppID = v
	}
	if v := os.Getenv("CC_GITHUB_INSTALLATION_ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("CC_GITHUB_INSTALLATION_ID: %w", err)
		}
		cfg.GitHub.InstallationID = n
	}
	if v := os.Getenv("CC_GITHUB_PRIVATE_KEY_REF"); v != "" {
		cfg.GitHub.PrivateKeyRef = v
	}
	if v := os.Getenv("CC_GITHUB_API_URL"); v != "" {
		cfg.GitHub.APIURL = v
	}
	return nil
}
