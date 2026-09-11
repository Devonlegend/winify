package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// LLM generates a completion for a prompt. Ollama is the production
// implementation; tests substitute a fake.
type LLM interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// Ollama talks to a local Ollama server's /api/generate endpoint. The model is
// configurable; the default is a small local model so the assistant stays
// offline and fast.
type Ollama struct {
	url    string
	model  string
	client *http.Client
}

// NewOllama builds an Ollama client. timeout bounds a single generation.
func NewOllama(url, model string, timeout time.Duration) *Ollama {
	if model == "" {
		model = "qwen3:1.7b"
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Ollama{
		url:    strings.TrimRight(url, "/"),
		model:  model,
		client: &http.Client{Timeout: timeout},
	}
}

// Model returns the configured model name.
func (o *Ollama) Model() string { return o.model }

// Generate runs a non-streaming completion. temperature=0 keeps answers
// grounded and repeatable; num_predict caps latency. think=false disables
// Qwen3's reasoning trace, which is pure overhead for this task.
func (o *Ollama) Generate(ctx context.Context, prompt string) (string, error) {
	body := map[string]any{
		"model":  o.model,
		"prompt": prompt,
		"stream": false,
		"think":  false,
		"options": map[string]any{
			"temperature":    0,
			"num_predict":    200,
			"repeat_penalty": 1.15,
			"repeat_last_n":  128,
		},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+"/api/generate", bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("ollama status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var out struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}
	return stripThinking(out.Response), nil
}

var thinkingRE = regexp.MustCompile(`(?s) thinking.*?<｜end▁of▁thinking｜>`)

// stripThinking removes any Qwen3 reasoning block from the response.
func stripThinking(s string) string {
	return strings.TrimSpace(thinkingRE.ReplaceAllString(s, ""))
}
