// Package assistant implements the local, offline RAG assistant: it ingests a
// small curated knowledge base into ChromaDB, retrieves relevant chunks for a
// question, augments them with live deployment/monitoring data from SQLite, and
// generates an answer with a local Ollama model. It never calls a cloud service.
package assistant

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// docsFS holds the curated knowledge base. It is deliberately small: platform
// usage, IIS and Docker troubleshooting, and an FAQ. Do not add the whole
// codebase here.
//
//go:embed docs/*.md
var docsFS embed.FS

// Doc is one retrievable chunk of the knowledge base.
type Doc struct {
	ID     string
	Title  string
	Source string
	Text   string
	Score  float64 // set by retrieval, not stored
}

// LoadDocs reads and chunks every embedded markdown file.
func LoadDocs() ([]Doc, error) {
	entries, err := fs.ReadDir(docsFS, "docs")
	if err != nil {
		return nil, fmt.Errorf("read docs dir: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var docs []Doc
	for _, entry := range entries {
		content, err := docsFS.ReadFile("docs/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read doc %s: %w", entry.Name(), err)
		}
		docs = append(docs, chunkMarkdown(entry.Name(), string(content))...)
	}
	return docs, nil
}

// chunkMarkdown splits a document into one chunk per "##" section. The document
// title (the first "# " heading) is prefixed to each chunk's title so citations
// are meaningful. A preamble before the first section becomes its own chunk.
func chunkMarkdown(filename, content string) []Doc {
	var (
		docs     []Doc
		docTitle = strings.TrimSuffix(filename, ".md")
		section  string
		body     []string
	)
	flush := func() {
		text := strings.TrimSpace(strings.Join(body, "\n"))
		body = nil
		if text == "" {
			return
		}
		title := docTitle
		if section != "" {
			title = docTitle + " — " + section
		}
		docs = append(docs, Doc{
			ID:     fmt.Sprintf("%s#%d", filename, len(docs)),
			Title:  title,
			Source: filename,
			Text:   text,
		})
	}

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "# "):
			docTitle = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		case strings.HasPrefix(trimmed, "## "):
			flush()
			section = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
		default:
			body = append(body, line)
		}
	}
	flush()
	return docs
}
