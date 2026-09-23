// Command control-center runs the DevOps Control Center server, and provides
// small admin subcommands for hashing the admin password and managing
// encrypted credentials.
//
// Usage:
//
//	control-center [serve] [-config config.yaml]
//	control-center bootstrap [-config config.yaml] [--dry-run]
//	control-center bootstrap -config config.yaml --remote http://host:5985/wsman --user DOMAIN\user
//	control-center hash-password
//	control-center cred add <name>
//	control-center cred list
//	control-center cred get <name>
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Devonlegend/winify/internal/assistant"
	"github.com/Devonlegend/winify/internal/auth"
	"github.com/Devonlegend/winify/internal/bootstrap"
	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/models"
	"github.com/Devonlegend/winify/internal/monitoring"
	"github.com/Devonlegend/winify/internal/proxy"
	"github.com/Devonlegend/winify/internal/server"
	"github.com/Devonlegend/winify/internal/service"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "serve":
			runServe(args[1:])
			return
		case "bootstrap":
			runBootstrap(args[1:])
			return
		case "hash-password":
			runHashPassword(args[1:])
			return
		case "cred":
			runCred(args[1:])
			return
		default:
			log.Fatalf("unknown command %q (want: serve, bootstrap, hash-password, cred)", args[0])
		}
	}
	runServe(args)
}

// ---- serve ----

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML config; missing file means built-in defaults")
	fs.Parse(args)

	cfg := mustConfig(*configPath)

	// Started by the Windows SCM: serve until the SCM asks us to stop.
	if service.IsWindowsService() {
		if err := service.Run(cfg.Bootstrap.ServiceName, func(ctx context.Context) error {
			return serve(cfg, *configPath, ctx)
		}); err != nil {
			log.Fatalf("windows service: %v", err)
		}
		return
	}

	// Fresh install on Windows: provisioning needs admin, so if we are not
	// elevated, relaunch ourselves elevated (UAC) and let that instance do it.
	if runtime.GOOS == "windows" && cfg.Bootstrap.Enabled && needsProvisioning(cfg, *configPath) {
		if elevated, err := bootstrap.IsElevated(context.Background(), deployment.NewLocalRunner()); err == nil && !elevated {
			exe, err := os.Executable()
			if err != nil {
				log.Fatalf("locate executable: %v", err)
			}
			if err := service.RelaunchElevated(exe, []string{"serve", "-config", *configPath}); err != nil {
				log.Printf("bootstrap: elevate: %v", err)
			} else {
				log.Printf("bootstrap: requested elevation to provision this host; approve the UAC prompt. This window can be closed.")
				return
			}
		}
	}

	// Interactive: stop on Ctrl-C / SIGINT. signal.NotifyContext is the modern
	// way to turn a signal into a context instead of a global handler.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := serve(cfg, *configPath, ctx); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// needsProvisioning reports whether the host has unfinished bootstrap steps.
func needsProvisioning(cfg config.Config, configPath string) bool {
	store, cleanup := mustStore(cfg)
	defer cleanup()
	boot := buildBootstrap(cfg, store, configPath)
	complete, err := boot.Complete(context.Background())
	return err != nil || !complete
}

// serve runs the control center until ctx is cancelled. It is shared by the
// interactive and Windows-service entry points.
func serve(cfg config.Config, configPath string, ctx context.Context) error {
	store, cleanup := mustStore(cfg)
	defer cleanup()

	if err := store.DeleteExpiredSessions(ctx, time.Now()); err != nil {
		log.Printf("prune sessions: %v", err)
	}

	seedAdmin(ctx, store, cfg)
	seedInventory(ctx, store, cfg)

	key, err := auth.LoadMasterKey(cfg.Credentials.MasterKey, masterKeyPath(cfg))
	if err != nil {
		log.Fatalf("master key: %v", err)
	}
	credStore, err := auth.NewCredentialStore(store, key)
	if err != nil {
		log.Fatalf("credential store: %v", err)
	}

	var registrar proxy.Registrar = proxy.Noop{}
	if cfg.Proxy.Enabled {
		caddy := proxy.NewCaddy(cfg.Proxy.AdminURL, cfg.Proxy.ServerName)
		registrar = caddy
		// Warn now rather than failing every deploy later: with the proxy on, a
		// deploy that has a domain fails if the route cannot be registered.
		if err := caddy.Reachable(ctx); err != nil {
			log.Printf("WARNING: proxy registration is enabled but Caddy is not reachable at %s: %v",
				cfg.Proxy.AdminURL, err)
			log.Printf("WARNING: deploys with a domain will fail until Caddy is running (or set proxy.enabled: false)")
		}
	}
	if cfg.Deploy.KnownHostsFile == "" {
		log.Printf("WARNING: deploy.known_hosts_file is empty; SSH host keys will NOT be verified")
	}
	sshDial := func(ctx context.Context, srv config.Server, key string) (deployment.Runner, error) {
		return deployment.DialSSH(ctx, srv.SSHHost, srv.SSHPort, srv.SSHUser, key, cfg.Deploy.KnownHostsFile)
	}

	// Audit every remote command (deploy, rollback, monitor): target, action,
	// timestamp and deployment. Commands never contain credentials; sensitive
	// payloads are redacted by the caller before they reach here.
	auditRecorder := func(ctx context.Context, meta deployment.AuditMeta, command string, runErr error) {
		rc := models.RemoteCommand{
			ServerID:     meta.ServerID,
			ServerType:   meta.ServerType,
			Action:       meta.Action,
			DeploymentID: meta.DeploymentID,
			Command:      command,
			ExecutedAt:   time.Now(),
		}
		if runErr != nil {
			rc.Error = deployment.RedactAuditText(runErr.Error())
		}
		shown := deployment.RedactAuditText(command)
		if len(shown) > 200 {
			shown = shown[:200] + "..."
		}
		log.Printf("audit: server=%s type=%s action=%s deploy=%d command=%q error=%s",
			meta.ServerID, meta.ServerType, meta.Action, meta.DeploymentID, shown, rc.Error)
		// Persist even if the command's context was cancelled.
		if err := store.InsertRemoteCommand(context.WithoutCancel(ctx), rc); err != nil {
			log.Printf("audit: persist: %v", err)
		}
	}

	targetFactory := deployment.NewTargetFactory(cfg, sshDial, auditRecorder)
	deployer := deployment.NewDeployer(cfg, store, credStore, registrar, targetFactory)

	// Metrics reuse the same SSH/WinRM connection code as deploys (no agent).
	collector := monitoring.NewCollector(monitoring.NewRunnerFactory(cfg, credStore, auditRecorder))
	scheduler := monitoring.NewScheduler(cfg, store, collector)
	if cfg.Monitoring.Enabled {
		scheduler.Start()
		defer scheduler.Stop()
		log.Printf("monitoring: polling every %s", scheduler.Interval())
	}

	authSvc := auth.NewService(store, cfg.Auth.CookieSecure, time.Duration(cfg.Auth.SessionTTLHours)*time.Hour)

	// Local RAG assistant: curated docs + live data, generated by Ollama.
	var assistantSvc *assistant.Service
	if cfg.Assistant.Enabled {
		assistantSvc = buildAssistant(ctx, cfg, store)
	}

	// First-run registration is protected by a one-time token. The default
	// listener is loopback-only, but the token also protects installations that
	// explicitly bind to a public interface.
	setupToken := cfg.Auth.SetupToken
	if setupToken == "" {
		if n, err := store.CountUsers(ctx); err == nil && n == 0 {
			setupToken, err = auth.NewSetupToken()
			if err != nil {
				log.Fatalf("setup token: %v", err)
			}
			log.Printf("first-run setup token (required to register the admin): %s", setupToken)
		}
	}

	// Bootstrap backs the Setup page and provisions the host on first run.
	var boot *bootstrap.Bootstrap
	if cfg.Bootstrap.Enabled {
		boot = buildBootstrap(cfg, store, configPath)
		provisionIfNeeded(ctx, boot)
	}

	srv, err := server.New(server.Deps{
		Cfg:             cfg,
		Store:           store,
		Auth:            authSvc,
		Secrets:         credStore,
		Deployer:        deployer,
		Assistant:       assistantSvc,
		CredentialAdmin: credStore,
		RunnerFactory:   deployment.NewRunnerFactory(sshDial, auditRecorder),
		Bootstrap:       boot,
		BootstrapRun: func(runCtx context.Context) error {
			if boot == nil {
				return errors.New("bootstrap is disabled")
			}
			_, err := boot.Run(runCtx)
			return err
		},
		BootstrapElevate: func() error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			return service.RelaunchElevated(exe, []string{"bootstrap", "-config", configPath})
		},
		BootstrapElevated: func(ctx context.Context) (bool, error) {
			return bootstrap.IsElevated(ctx, deployment.NewLocalRunner())
		},
		SetupToken: setupToken,
	})
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	httpServer := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		log.Printf("control-center listening on %s", cfg.Server.Addr)
		listenErr <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Println("shutting down")
	case err := <-listenErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	return nil
}

// seedAdmin creates the admin account when a password is supplied out of band
// (CC_ADMIN_PASSWORD, hashed here, or a pre-hashed auth.admin_password_hash) —
// useful for headless/automated installs. When neither is set, it does nothing
// and the operator creates the first account in the browser via /register. No
// password is ever logged.
func seedAdmin(ctx context.Context, store *models.Store, cfg config.Config) {
	hash := cfg.Auth.AdminPasswordHash
	if plaintext := os.Getenv("CC_ADMIN_PASSWORD"); plaintext != "" {
		h, err := auth.HashPassword(plaintext)
		if err != nil {
			log.Fatalf("hash admin password: %v", err)
		}
		hash = h
	}
	if hash == "" {
		if n, err := store.CountUsers(ctx); err == nil && n == 0 {
			log.Printf("no admin account yet: open the control-center in a browser to create one")
		}
		return
	}
	if err := store.UpsertUser(ctx, cfg.Auth.AdminUser, hash); err != nil {
		log.Fatalf("seed admin user: %v", err)
	}
}

// seedInventory imports servers.yaml / projects.yaml into an empty database.
// After the first run the database is the source of truth and the dashboard
// manages the inventory, so the files are not re-imported.
func seedInventory(ctx context.Context, store *models.Store, cfg config.Config) {
	if n, err := store.CountServers(ctx); err != nil {
		log.Fatalf("count servers: %v", err)
	} else if n == 0 {
		servers, err := config.LoadServers(cfg.Files.Servers)
		if err != nil {
			log.Fatalf("servers config: %v", err)
		}
		for _, srv := range servers {
			if err := config.ValidateServer(srv); err != nil {
				log.Fatalf("invalid server %q in %s: %v", srv.ID, cfg.Files.Servers, err)
			}
			if err := store.UpsertServer(ctx, srv); err != nil {
				log.Fatalf("seed server: %v", err)
			}
		}
		log.Printf("seeded %d servers from %s", len(servers), cfg.Files.Servers)
	} else {
		log.Printf("servers table has %d rows; skipping YAML seed", n)
	}

	if n, err := store.CountProjects(ctx); err != nil {
		log.Fatalf("count projects: %v", err)
	} else if n == 0 {
		projects, err := config.LoadProjects(cfg.Files.Projects)
		if err != nil {
			log.Fatalf("projects config: %v", err)
		}
		servers, err := store.ListServers(ctx)
		if err != nil {
			log.Fatalf("list servers for project validation: %v", err)
		}
		byID := make(map[string]config.Server, len(servers))
		for _, srv := range servers {
			byID[srv.ID] = srv
		}
		for _, p := range projects {
			srv, ok := byID[p.ServerID]
			if !ok {
				log.Fatalf("project %s references unknown server %s", p.ID, p.ServerID)
			}
			if err := config.ValidateProject(p, srv); err != nil {
				log.Fatalf("invalid project %q in %s: %v", p.ID, cfg.Files.Projects, err)
			}
			if err := store.UpsertProject(ctx, p); err != nil {
				log.Fatalf("seed project: %v", err)
			}
		}
		log.Printf("seeded %d projects from %s", len(projects), cfg.Files.Projects)
	} else {
		log.Printf("projects table has %d rows; skipping YAML seed", n)
	}
}

// buildAssistant loads the curated docs, picks a vector store (ChromaDB with an
// in-memory fallback) and ingests the knowledge base. Ingestion is idempotent.
func buildAssistant(ctx context.Context, cfg config.Config, store *models.Store) *assistant.Service {
	docs, err := assistant.LoadDocs()
	if err != nil {
		log.Fatalf("assistant: load docs: %v", err)
	}

	fallback := assistant.NewMemoryStore()
	var primary assistant.VectorStore = fallback
	if cfg.Assistant.ChromaURL != "" {
		chroma := assistant.NewChroma(cfg.Assistant.ChromaURL, cfg.Assistant.Collection)
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := chroma.Ping(pingCtx)
		cancel()
		if err == nil {
			primary = chroma
			log.Printf("assistant: using ChromaDB at %s (collection %q)", cfg.Assistant.ChromaURL, cfg.Assistant.Collection)
		} else {
			log.Printf("assistant: ChromaDB unavailable (%v); using in-memory store", err)
		}
	}

	timeout := time.Duration(cfg.Assistant.TimeoutSeconds) * time.Second
	llm := assistant.NewOllama(cfg.Assistant.OllamaURL, cfg.Assistant.Model, timeout)
	svc := assistant.NewService(cfg, store, primary, fallback, llm, docs)

	ingestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := svc.Ingest(ingestCtx); err != nil {
		log.Printf("assistant: ingest: %v", err)
	}
	log.Printf("assistant: %d doc chunks ready (store=%s, model=%s)", len(docs), primary.Name(), cfg.Assistant.Model)
	return svc
}

// ---- bootstrap ----

// runBootstrap provisions this host (directories, NSSM, WinRM). It must run
// elevated; use --dry-run to preview the changes without applying them.
func runBootstrap(args []string) {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML config")
	dryRun := fs.Bool("dry-run", false, "report intended changes without applying them")
	remote := fs.String("remote", "", "WinRM endpoint of a remote Windows server to provision")
	remoteUser := fs.String("user", "", "WinRM user for --remote (or CC_REMOTE_WINRM_USER)")
	remoteID := fs.String("id", "", "server id to create for --remote (default derived from the host)")
	remoteName := fs.String("name", "", "server display name for --remote")
	fs.Parse(args)

	cfg := mustConfig(*configPath)
	if !cfg.Bootstrap.Enabled {
		log.Fatal("bootstrap is disabled (set bootstrap.enabled: true to enable it)")
	}
	store, cleanup := mustStore(cfg)
	defer cleanup()

	if *remote != "" {
		runRemoteBootstrap(cfg, store, *remote, *remoteUser, *remoteID, *remoteName, *dryRun)
		return
	}

	root := cfg.Bootstrap.InstallDir
	if root == "" {
		root = bootstrap.DefaultRoot()
	}
	runner := deployment.NewLocalRunner()
	ctx := context.Background()

	if !*dryRun {
		elevated, err := bootstrap.IsElevated(ctx, runner)
		if err != nil {
			log.Fatalf("bootstrap: check elevation: %v", err)
		}
		if !elevated {
			log.Fatal("bootstrap must run elevated: open PowerShell as Administrator, or run it from the winify service")
		}
	}

	exe := usableExe()
	paths := bootstrap.DefaultPaths(root)
	opts := bootstrap.Options{
		Paths:        paths,
		NSSMSource:   cfg.Deploy.NSSMSource,
		NSSMSHA256:   cfg.Deploy.NSSMSHA256,
		EnableWinRM:  cfg.Bootstrap.EnableWinRM,
		ServiceName:  cfg.Bootstrap.ServiceName,
		ExePath:      exe,
		ConfigPath:   *configPath,
		CaddyEnabled: cfg.Bootstrap.Caddy.Enabled,
		CaddySource:  cfg.Bootstrap.Caddy.Source,
		CaddyURL:     cfg.Bootstrap.Caddy.URL,
		CaddySHA256:  cfg.Bootstrap.Caddy.SHA256,
		CaddyAdmin:   cfg.Bootstrap.Caddy.Admin,
		DryRun:       *dryRun,
		Meta:         store,
		Runner:       runner,
		Logf:         log.Printf,
	}

	// The local target runs PowerShell in-process, so it needs no credential.
	opts.TargetStepName = "local-target"
	opts.TargetCheck, opts.TargetApply = serverSteps(store, localTargetServer(paths))

	b := bootstrap.New(opts)
	results, err := b.Run(ctx)
	for _, r := range results {
		line := string(r.Status)
		if r.Error != "" {
			line += ": " + r.Error
		}
		log.Printf("bootstrap: %-6s %s", r.Name, line)
	}
	if err != nil {
		log.Fatalf("bootstrap failed: %v", err)
	}
	log.Printf("bootstrap complete (root %s)", root)
}

// usableExe returns the running executable's path, or "" when it is a throwaway
// build (`go run`, tests): installing that path as a service would leave a
// broken service behind, so self-service is skipped.
func usableExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if strings.Contains(exe, "go-build") || strings.HasPrefix(exe, os.TempDir()) {
		log.Printf("bootstrap: running from a temporary build (%s); skipping self-service install", exe)
		return ""
	}
	return exe
}

// buildBootstrap assembles the host-provisioning steps for this configuration.
func buildBootstrap(cfg config.Config, store *models.Store, configPath string) *bootstrap.Bootstrap {
	root := cfg.Bootstrap.InstallDir
	if root == "" {
		root = bootstrap.DefaultRoot()
	}
	paths := bootstrap.DefaultPaths(root)
	exe := usableExe()
	opts := bootstrap.Options{
		Paths:          paths,
		NSSMSource:     cfg.Deploy.NSSMSource,
		NSSMSHA256:     cfg.Deploy.NSSMSHA256,
		EnableWinRM:    cfg.Bootstrap.EnableWinRM,
		ServiceName:    cfg.Bootstrap.ServiceName,
		ExePath:        exe,
		ConfigPath:     configPath,
		CaddyEnabled:   cfg.Bootstrap.Caddy.Enabled,
		CaddySource:    cfg.Bootstrap.Caddy.Source,
		CaddyURL:       cfg.Bootstrap.Caddy.URL,
		CaddySHA256:    cfg.Bootstrap.Caddy.SHA256,
		CaddyAdmin:     cfg.Bootstrap.Caddy.Admin,
		TargetStepName: "local-target",
		Meta:           store,
		Runner:         deployment.NewLocalRunner(),
		Logf:           log.Printf,
	}
	opts.TargetCheck, opts.TargetApply = serverSteps(store, localTargetServer(paths))
	return bootstrap.New(opts)
}

// provisionIfNeeded runs bootstrap automatically when the host is not yet
// provisioned and we can (elevated: a service, or after a UAC relaunch).
func provisionIfNeeded(ctx context.Context, boot *bootstrap.Bootstrap) {
	complete, err := boot.Complete(ctx)
	if err != nil || complete {
		return
	}
	elevated, err := bootstrap.IsElevated(ctx, deployment.NewLocalRunner())
	if err != nil || !elevated {
		log.Printf("bootstrap: host not fully provisioned; open the Setup page or run an elevated `winify bootstrap`")
		return
	}
	log.Printf("bootstrap: host not fully provisioned; running bootstrap")
	if _, err := boot.Run(ctx); err != nil {
		log.Printf("bootstrap: %v", err)
	}
}

// localTargetServer is the winsvc server entry for the machine winify runs on.
// It is a local target: no WinRM, no credential.
func localTargetServer(paths bootstrap.Paths) config.Server {
	name := "This machine"
	if h, err := os.Hostname(); err == nil && h != "" {
		name = h
	}
	return config.Server{
		ID:       "local",
		Name:     name,
		Type:     config.ServerTypeWindowsService,
		Local:    true,
		NSSMPath: paths.NSSM,
		Host:     "127.0.0.1",
	}
}

// serverSteps returns the closures that create a server entry (no credential).
func serverSteps(store *models.Store, srv config.Server) (func(context.Context) (bool, error), func(context.Context) error) {
	check := func(ctx context.Context) (bool, error) {
		_, err := store.GetServer(ctx, srv.ID)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, models.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	apply := func(ctx context.Context) error {
		if srv.Local && srv.PublicIP == "" {
			if result := bootstrap.DetectPublicIP(ctx); result.OK {
				srv.PublicIP = result.IP
			} else {
				log.Printf("bootstrap: no usable public IP (%s); set it manually to enable sslip.io domains", result.Reason)
			}
		}
		return store.UpsertServer(ctx, srv)
	}
	return check, apply
}

// targetSteps returns the closures that store a WinRM credential and create a
// winsvc server entry. Used for remote bootstrap.
func targetSteps(store *models.Store, credStore *auth.CredentialStore, srv config.Server, refName, password string) (func(context.Context) (bool, error), func(context.Context) error) {
	check := func(ctx context.Context) (bool, error) {
		_, err := store.GetServer(ctx, srv.ID)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, models.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	apply := func(ctx context.Context) error {
		if err := credStore.Put(ctx, refName, password); err != nil {
			return fmt.Errorf("store credential: %w", err)
		}
		return store.UpsertServer(ctx, srv)
	}
	return check, apply
}

// runRemoteBootstrap provisions a remote Windows server over WinRM: directories,
// NSSM, WinRM and (optionally) Caddy, then creates a winsvc server entry. It does
// not install winify itself on the target.
func runRemoteBootstrap(cfg config.Config, store *models.Store, endpoint, user, id, name string, dryRun bool) {
	if user == "" {
		user = strings.TrimSpace(os.Getenv("CC_REMOTE_WINRM_USER"))
	}
	if user == "" {
		log.Fatal("--user is required for --remote (or set CC_REMOTE_WINRM_USER)")
	}
	password := os.Getenv("CC_REMOTE_WINRM_PASSWORD")
	if password == "" {
		pw, err := readSecret("Remote WinRM password (input is echoed): ")
		if err != nil || pw == "" {
			log.Fatal("no remote password provided")
		}
		password = pw
	}
	if id == "" {
		id = remoteServerID(endpoint)
	}
	if name == "" {
		name = id
	}

	srv := config.Server{
		ID:             id,
		Name:           name,
		Type:           config.ServerTypeWindowsService,
		WinRMEndpoint:  endpoint,
		WinRMUser:      user,
		WinRMTransport: "ntlm",
	}
	runner, err := deployment.DialWinRM(srv, password)
	if err != nil {
		log.Fatalf("connect %s: %v", endpoint, err)
	}
	defer runner.Close()

	key, err := auth.LoadMasterKey(cfg.Credentials.MasterKey, masterKeyPath(cfg))
	if err != nil {
		log.Fatalf("master key: %v", err)
	}
	credStore, err := auth.NewCredentialStore(store, key)
	if err != nil {
		log.Fatalf("credential store: %v", err)
	}

	root := cfg.Bootstrap.InstallDir
	if root == "" {
		root = bootstrap.DefaultRoot()
	}
	refName := id + "-winrm"
	srv.CredentialRef = "vault:" + refName

	opts := bootstrap.Options{
		Paths:          bootstrap.DefaultPaths(root),
		NSSMSource:     cfg.Deploy.NSSMSource,
		NSSMSHA256:     cfg.Deploy.NSSMSHA256,
		EnableWinRM:    cfg.Bootstrap.EnableWinRM,
		CaddyEnabled:   cfg.Bootstrap.Caddy.Enabled,
		CaddySource:    cfg.Bootstrap.Caddy.Source,
		CaddyURL:       cfg.Bootstrap.Caddy.URL,
		CaddySHA256:    cfg.Bootstrap.Caddy.SHA256,
		CaddyAdmin:     cfg.Bootstrap.Caddy.Admin,
		TargetStepName: "target",
		DryRun:         dryRun,
		Meta:           store,
		Runner:         runner,
		Logf:           log.Printf,
	}
	opts.TargetCheck, opts.TargetApply = targetSteps(store, credStore, srv, refName, password)

	results, err := bootstrap.New(opts).Run(context.Background())
	for _, res := range results {
		line := string(res.Status)
		if res.Error != "" {
			line += ": " + res.Error
		}
		log.Printf("bootstrap: %-8s %s", res.Name, line)
	}
	if err != nil {
		log.Fatalf("remote bootstrap failed: %v", err)
	}
	log.Printf("remote bootstrap complete: %s (%s)", id, endpoint)
}

// remoteServerID derives a server id from a WinRM endpoint host.
func remoteServerID(endpoint string) string {
	host := endpoint
	if u, err := url.Parse(endpoint); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return "remote-" + sanitizeID(host)
}

// sanitizeID lowercases and replaces non-alphanumerics for use in an id.
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// ---- hash-password ----

func runHashPassword(args []string) {
	fs := flag.NewFlagSet("hash-password", flag.ExitOnError)
	fs.Parse(args)

	password, err := readSecret("New admin password (input is echoed): ")
	if err != nil {
		log.Fatalf("read password: %v", err)
	}
	if password == "" {
		log.Fatal("empty password")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		log.Fatalf("hash: %v", err)
	}
	// stdout carries only the bcrypt hash, which is safe to paste into config.
	fmt.Println(hash)
}

// ---- cred ----

func runCred(args []string) {
	fs := flag.NewFlagSet("cred", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to YAML config")
	fs.Parse(args)
	rest := fs.Args()

	if len(rest) == 0 {
		log.Fatal("usage: control-center cred <add|list|get> [name]")
	}
	action := rest[0]

	cfg := mustConfig(*configPath)
	store, cleanup := mustStore(cfg)
	defer cleanup()

	key, err := auth.LoadMasterKey(cfg.Credentials.MasterKey, masterKeyPath(cfg))
	if err != nil {
		log.Fatalf("master key: %v", err)
	}
	credStore, err := auth.NewCredentialStore(store, key)
	if err != nil {
		log.Fatalf("credential store: %v", err)
	}
	ctx := context.Background()

	switch action {
	case "add":
		if len(rest) < 2 {
			log.Fatal("usage: control-center cred add <name>")
		}
		name := rest[1]
		secret, err := readSecret("Secret value (input is echoed): ")
		if err != nil {
			log.Fatalf("read secret: %v", err)
		}
		if secret == "" {
			log.Fatal("empty secret, nothing stored")
		}
		if err := credStore.Put(ctx, name, secret); err != nil {
			log.Fatalf("store credential: %v", err)
		}
		// Log the name only. The secret is never printed.
		log.Printf("stored credential %q (encrypted at rest)", name)

	case "list":
		names, err := store.CredentialNames(ctx)
		if err != nil {
			log.Fatalf("list credentials: %v", err)
		}
		for _, n := range names {
			fmt.Println(n)
		}

	case "get":
		if len(rest) < 2 {
			log.Fatal("usage: control-center cred get <name>")
		}
		name := rest[1]
		secret, err := credStore.Get(ctx, name)
		if errors.Is(err, models.ErrNotFound) {
			log.Fatalf("credential %q not found", name)
		}
		if err != nil {
			log.Fatalf("read credential: %v", err)
		}
		// Deliberate: the operator asked for the plaintext. It goes to stdout,
		// never to the log, so it cannot end up in a log aggregation pipeline.
		fmt.Println(secret)

	default:
		log.Fatalf("unknown cred action %q (want: add, list, get)", action)
	}
}

// ---- shared helpers ----

func defaultConfigPath() string {
	if v := os.Getenv("CC_CONFIG"); v != "" {
		return v
	}
	return "config.yaml"
}

func mustConfig(path string) config.Config {
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	return cfg
}

func mustStore(cfg config.Config) (*models.Store, func()) {
	if dir := filepath.Dir(cfg.Database.Path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("create data dir %s: %v", dir, err)
		}
	}
	db, err := models.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	if err := models.Migrate(db); err != nil {
		db.Close()
		log.Fatalf("migrate: %v", err)
	}
	return models.NewStore(db), func() { db.Close() }
}

// masterKeyPath keeps the generated key beside the database in the data dir.
func masterKeyPath(cfg config.Config) string {
	return filepath.Join(filepath.Dir(cfg.Database.Path), "master.key")
}

// readSecret reads a secret from stdin. When stdin is piped or redirected it
// reads the whole stream, so multi-line secrets such as SSH private keys work
// with `control-center cred add name < key`. Interactively it reads one line.
func readSecret(prompt string) (string, error) {
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}

	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
