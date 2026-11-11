package constant

import (
	"context"
	"net"

	N "github.com/metacubex/mihomo/common/net"

	"github.com/gofrs/uuid/v5"
)

type latencyTestContextKey struct{}

// WithLatencyTestContext marks a context as originating from a synthetic latency probe.
func WithLatencyTestContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, latencyTestContextKey{}, true)
}

// IsLatencyTestContext reports whether ctx originates from a synthetic latency probe.
func IsLatencyTestContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(latencyTestContextKey{}).(bool)
	return v
}

type PlainContext interface {
	ID() uuid.UUID
}

type ConnContext interface {
	PlainContext
	Metadata() *Metadata
	Conn() *N.BufferedConn
}

type PacketConnContext interface {
	PlainContext
	Metadata() *Metadata
	PacketConn() net.PacketConn
}
