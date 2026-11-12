package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outboundgroup"
	"github.com/metacubex/mihomo/adapter/provider"
	"github.com/metacubex/mihomo/common/utils"
	"github.com/metacubex/mihomo/constant"
	cp "github.com/metacubex/mihomo/constant/provider"
)

func TestReviewNetworkProbeWaitsForFreshRound(t *testing.T) {
	var calls atomic.Int32
	group := adapter.NewProxy(&mockAutoGroupAdapter{
		name: "busy-then-ready", groupType: constant.URLTest,
		healthCheckContextFunc: func(context.Context) (map[string]uint16, error) {
			if calls.Add(1) == 1 {
				return nil, fmt.Errorf("older network round: %w", outboundgroup.ErrHealthCheckInFlight)
			}
			return map[string]uint16{"fresh-node": 25}, nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !probeGroupsConcurrently(ctx, []*adapter.Proxy{group}) {
		t.Fatal("fresh network probe did not complete after the previous round released the group")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("reported success without testing the new network: calls=%d, want 2", got)
	}
}

type reviewProviderProbeProxy struct {
	constant.Proxy
	status             string
	budget             time.Duration
	acceptsServerError bool
}

func (p *reviewProviderProbeProxy) URLTest(ctx context.Context, _ string, expected utils.IntRanges[uint16]) (uint16, error) {
	p.status = expected.String()
	p.acceptsServerError = expected.Check(500)
	if deadline, ok := ctx.Deadline(); ok {
		p.budget = time.Until(deadline)
	}
	return 10, nil
}

func TestReviewProviderFallbackPreservesHealthPolicy(t *testing.T) {
	node := &reviewProviderProbeProxy{}
	expected, err := utils.NewUnsignedRanges[uint16]("204")
	if err != nil {
		t.Fatal(err)
	}
	proxies := []constant.Proxy{node}
	hc := provider.NewHealthCheck(proxies, "https://example.test/health", 1500, 0, true, expected)
	pd, err := provider.NewCompatibleProvider("configured-provider", proxies, hc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pd.Close() })
	probeProvidersFallback(context.Background(), map[string]cp.ProxyProvider{pd.Name(): pd})
	if node.status != "204" || node.acceptsServerError {
		t.Errorf("provider HTTP policy lost: expected=%s, accepts 500=%v", node.status, node.acceptsServerError)
	}
	if node.budget <= 0 || node.budget > 1500*time.Millisecond {
		t.Errorf("provider timeout lost: got %v, want at most 1.5s", node.budget)
	}
}

func TestReviewNetworkProbeBusyIsNotSuccess(t *testing.T) {
	group := adapter.NewProxy(&mockAutoGroupAdapter{
		name: "always-busy", groupType: constant.Fallback,
		healthCheckContextFunc: func(context.Context) (map[string]uint16, error) {
			return nil, outboundgroup.ErrHealthCheckInFlight
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if probeGroupsConcurrently(ctx, []*adapter.Proxy{group}) {
		t.Fatal("an unfinished old-network probe must not satisfy a new-network check")
	}
	if isNetworkProbing(group.Name()) {
		t.Fatal("network probing marker leaked after cancellation")
	}
}
