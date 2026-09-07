// Package static embeds the server-rendered templates and the web assets
// (CSS, JS, images) into the binary with go:embed, so the compiled server is
// self-contained and needs no template files beside it at runtime.
package static

import "embed"

// Templates is rooted at internal/static/templates.
//
//go:embed templates
var Templates embed.FS

// Web is rooted at internal/static/web (assets served under /assets).
//
//go:embed web
var Web embed.FS
