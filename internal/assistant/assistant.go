package assistant

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
)

// Answer is the result returned to the UI.
type Answer struct {
	Answer    string   `json:"answer"`
	Sources   []string `json:"sources"`
	Intent    string   `json:"intent"` // docs | deploy | server
	Model     string   `json:"model"`
	Live      bool     `json:"live"`      // live deploy/monitoring data was used
	DocsUsed  bool     `json:"docs_used"` // retrieved docs were used
	LatencyMS int64    `json:"latency_ms"`
}

// Service ties retrieval, live data and generation together.
type Service struct {
	cfg      config.Config
	store    *models.Store
	docs     []Doc
	primary  VectorStore
	fallback *MemoryStore
	llm      LLM
}

// NewService builds the assistant. primary may be Chroma; fallback is always
// populated so retrieval survives Chroma being unavailable.
func NewService(cfg config.Config, store *models.Store, primary VectorStore, fallback *MemoryStore, llm LLM, docs []Doc) *Service {
	return &Service{cfg: cfg, store: store, docs: docs, primary: primary, fallback: fallback, llm: llm}
}

// Ingest loads the curated docs into the vector store(s). It is idempotent.
func (s *Service) Ingest(ctx context.Context) error {
	if s.fallback != nil {
		if err := s.fallback.Upsert(ctx, s.docs); err != nil {
			return fmt.Errorf("ingest into memory: %w", err)
		}
	}
	if s.primary != nil && s.primary != VectorStore(s.fallback) {
		if err := s.primary.Upsert(ctx, s.docs); err != nil {
			return fmt.Errorf("ingest into %s: %w", s.primary.Name(), err)
		}
	}
	return nil
}

// Ready reports whether the knowledge base has been loaded.
func (s *Service) Ready() bool { return len(s.docs) > 0 }

// Ask answers a question from the curated docs and/or live data, citing what it
// used. It always returns an answer: if the model is unavailable it falls back
// to a deterministic, grounded response.
func (s *Service) Ask(ctx context.Context, question string) (Answer, error) {
	start := time.Now()
	question = strings.TrimSpace(question)
	if question == "" {
		return Answer{}, fmt.Errorf("empty question")
	}

	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return Answer{}, fmt.Errorf("load projects: %w", err)
	}
	servers, err := s.store.ListServers(ctx)
	if err != nil {
		return Answer{}, fmt.Errorf("load servers: %w", err)
	}

	intent, entity := DetectIntent(question, projects, servers)

	var liveBlock string
	var liveSources []string
	switch intent {
	case IntentDeploy:
		liveBlock, liveSources = s.deployContext(ctx, projects, entity)
	case IntentServer:
		liveBlock, liveSources = s.serverContext(ctx, servers, entity)
	}

	docs := s.retrieve(ctx, question)
	prompt := buildPrompt(question, docs, liveBlock)

	var answer string
	if s.llm != nil {
		if text, err := s.llm.Generate(ctx, prompt); err == nil && strings.TrimSpace(text) != "" {
			answer = strings.TrimSpace(text)
		}
	}
	if answer == "" {
		answer = fallbackAnswer(liveBlock, docs)
	}

	sources := make([]string, 0, len(liveSources)+len(docs))
	sources = append(sources, liveSources...)
	for _, d := range docs {
		sources = append(sources, "doc: "+d.Title)
	}

	return Answer{
		Answer:    answer,
		Sources:   dedupe(sources),
		Intent:    string(intent),
		Model:     s.cfg.Assistant.Model,
		Live:      liveBlock != "",
		DocsUsed:  len(docs) > 0,
		LatencyMS: time.Since(start).Milliseconds(),
	}, nil
}

// retrieve queries the primary store, falling back to the in-memory store.
func (s *Service) retrieve(ctx context.Context, question string) []Doc {
	k := s.cfg.Assistant.TopK
	if k <= 0 {
		k = 4
	}
	if s.primary != nil {
		if docs, err := s.primary.Query(ctx, question, k); err == nil && len(docs) > 0 {
			return docs
		}
	}
	if s.fallback != nil {
		if docs, err := s.fallback.Query(ctx, question, k); err == nil {
			return docs
		}
	}
	return nil
}

// deployContext formats live deploy history for the question's entity.
func (s *Service) deployContext(ctx context.Context, projects []config.Project, entity Entity) (string, []string) {
	var b strings.Builder
	var sources []string
	b.WriteString("Live deploy history (source: deployments database):\n")

	appendDeploy := func(p config.Project, d models.Deployment) {
		when := d.FinishedAt
		if when.IsZero() {
			when = d.StartedAt
		}
		fmt.Fprintf(&b, "- %s (%s) deployment #%d: status=%s, trigger=%s, commit=%s, artifact=%s, finished=%s",
			p.ID, p.Name, d.ID, d.Status, d.Trigger, shortCommit(d.CommitSHA), d.ImageTag, when.Format("2006-01-02 15:04:05"))
		if d.Error != "" {
			fmt.Fprintf(&b, ", error=%q", d.Error)
		}
		b.WriteString("\n")
		sources = append(sources, fmt.Sprintf("deploy history: %s #%d", p.ID, d.ID))
	}

	switch {
	case entity.Kind == "project":
		p, ok := findProject(projects, entity.ID)
		if !ok {
			b.WriteString("- no matching project.\n")
			break
		}
		deploys, err := s.store.ListDeployments(ctx, p.ID, 3)
		if err != nil || len(deploys) == 0 {
			fmt.Fprintf(&b, "- %s has no deployments recorded yet.\n", p.ID)
			break
		}
		for _, d := range deploys {
			appendDeploy(p, d)
		}
	case entity.Kind == "server":
		found := false
		for _, p := range projects {
			if p.ServerID != entity.ID {
				continue
			}
			deploys, err := s.store.ListDeployments(ctx, p.ID, 1)
			if err != nil || len(deploys) == 0 {
				continue
			}
			found = true
			appendDeploy(p, deploys[0])
		}
		if !found {
			fmt.Fprintf(&b, "- no deployments found for server %s.\n", entity.ID)
		}
	default:
		deploys, err := s.store.RecentDeployments(ctx, 5)
		if err != nil || len(deploys) == 0 {
			b.WriteString("- no deployments recorded yet.\n")
			break
		}
		for _, d := range deploys {
			if p, ok := findProject(projects, d.ProjectID); ok {
				appendDeploy(p, d)
			} else {
				fmt.Fprintf(&b, "- %s deployment #%d: status=%s\n", d.ProjectID, d.ID, d.Status)
				sources = append(sources, fmt.Sprintf("deploy history: %s #%d", d.ProjectID, d.ID))
			}
		}
	}
	return b.String(), sources
}

// serverContext formats the latest monitoring sample(s) for the entity.
func (s *Service) serverContext(ctx context.Context, servers []config.Server, entity Entity) (string, []string) {
	latest, err := s.store.LatestMetrics(ctx)
	if err != nil {
		return "Live monitoring data is unavailable.\n", nil
	}
	byID := make(map[string]models.Metric, len(latest))
	for _, m := range latest {
		byID[m.ServerID] = m
	}

	var b strings.Builder
	var sources []string
	b.WriteString("Live monitoring data (source: metrics database):\n")

	appendServer := func(srv config.Server) {
		m, ok := byID[srv.ID]
		if !ok {
			fmt.Fprintf(&b, "- %s (%s): no metrics collected yet.\n", srv.ID, srv.Type)
			return
		}
		if !m.Reachable {
			fmt.Fprintf(&b, "- %s (%s) is UNREACHABLE: %s (last attempt %s)\n",
				srv.ID, srv.Type, m.Error, m.Timestamp.Format("2006-01-02 15:04:05"))
		} else {
			fmt.Fprintf(&b, "- %s (%s): cpu=%.1f%%, mem=%.1f%% (%s of %s), disk=%.1f%%, uptime=%s, load1=%.2f, sampled %s\n",
				srv.ID, srv.Type, m.CPUPercent, m.MemPercent,
				humanBytes(m.MemUsed), humanBytes(m.MemTotal), m.DiskPercent,
				humanDuration(m.UptimeSeconds), m.Load1, m.Timestamp.Format("15:04:05"))
		}
		sources = append(sources, fmt.Sprintf("monitoring: %s @ %s", srv.ID, m.Timestamp.Format("2006-01-02 15:04:05")))
	}

	if entity.Kind == "server" {
		if srv, ok := findServer(servers, entity.ID); ok {
			appendServer(srv)
		} else {
			b.WriteString("- no matching server.\n")
		}
		return b.String(), sources
	}
	if len(servers) == 0 {
		b.WriteString("- no servers configured.\n")
		return b.String(), sources
	}
	for _, srv := range servers {
		appendServer(srv)
	}
	return b.String(), sources
}

// buildPrompt keeps the prompt short (latency matters) and instructs the model
// to answer only from context, stay brief, and cite sources. The structured
// Sources field on the response carries the full citation list, so the model is
// told not to enumerate every source (which small models tend to loop on).
func buildPrompt(question string, docs []Doc, liveBlock string) string {
	var b strings.Builder
	b.WriteString("You are the DevOps Control Center assistant. Answer the question using ONLY the context below.\n")
	b.WriteString("Answer in at most 4 sentences. Do not repeat yourself and do not list more than one or two sources. ")
	b.WriteString("If you used live data, say so. If the context does not contain the answer, say you don't know.\n\n")

	if liveBlock != "" {
		b.WriteString("LIVE DATA:\n")
		b.WriteString(truncate(liveBlock, 1200))
		b.WriteString("\n")
	}
	if len(docs) > 0 {
		b.WriteString("DOCUMENTATION:\n")
		for _, d := range docs {
			b.WriteString("[" + d.Title + "]\n")
			b.WriteString(truncate(d.Text, 600))
			b.WriteString("\n\n")
		}
	}
	b.WriteString("QUESTION: " + question + "\nANSWER:")
	return b.String()
}

// fallbackAnswer is used when the model is unavailable: it returns the live
// data and/or the top retrieved chunk verbatim, so the answer is still grounded.
func fallbackAnswer(liveBlock string, docs []Doc) string {
	var b strings.Builder
	if liveBlock != "" {
		b.WriteString(liveBlock)
	}
	if len(docs) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("From " + docs[0].Title + ":\n")
		b.WriteString(truncate(docs[0].Text, 700))
	}
	if b.Len() == 0 {
		return "I don't have an answer for that in the knowledge base yet."
	}
	return strings.TrimSpace(b.String())
}

func findProject(projects []config.Project, id string) (config.Project, bool) {
	for _, p := range projects {
		if p.ID == id {
			return p, true
		}
	}
	return config.Project{}, false
}

func findServer(servers []config.Server, id string) (config.Server, bool) {
	for _, s := range servers {
		if s.ID == id {
			return s, true
		}
	}
	return config.Server{}, false
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func shortCommit(sha string) string {
	if sha == "" {
		return "-"
	}
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func humanDuration(sec int64) string {
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		return fmt.Sprintf("%dm", m)
	}
}
