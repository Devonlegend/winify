// Package proxy registers deployed applications with the reverse proxy so each
// one is reachable over HTTPS with an automatically managed TLS certificate.
// It talks to Caddy's admin API and never handles application credentials.
package proxy

import "context"

// Registrar adds and removes public host routes in the reverse proxy. It is an
// interface so the deploy pipeline can be tested without a live proxy.
type Registrar interface {
	// Register makes host publicly reachable and forwards it to upstream
	// (host:port). It must be idempotent: re-registering updates in place.
	Register(ctx context.Context, host, upstream string) error
	// Deregister removes a previously registered host.
	Deregister(ctx context.Context, host string) error
}

// Noop is used when proxy registration is disabled (local development).
type Noop struct{}

func (Noop) Register(context.Context, string, string) error { return nil }
func (Noop) Deregister(context.Context, string) error       { return nil }
