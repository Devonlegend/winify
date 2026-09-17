package bootstrap

import "context"

// MetaStore persists bootstrap step state. models.Store implements it over the
// app_meta table, so progress survives restarts and re-runs skip done steps.
type MetaStore interface {
	GetMeta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error
}

// metaKey is the app_meta key holding one step's state.
func metaKey(step string) string { return "bootstrap." + step }
