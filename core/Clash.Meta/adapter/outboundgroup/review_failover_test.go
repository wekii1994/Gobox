package outboundgroup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
	"github.com/stretchr/testify/require"
)

type reviewHealthProxy struct {
	urlTestMockProxy
	probeDelay uint16
	probeErr   error
}

func (p *reviewHealthProxy) URLTest(context.Context, string, utils.IntRanges[uint16]) (uint16, error) {
	p.alive.Store(p.probeErr == nil)
	p.lastDelay.Store(uint32(p.probeDelay))
	return p.probeDelay, p.probeErr
}

func TestReviewHealthCheckClassifiesHealthyReselection(t *testing.T) {
	for _, kind := range []string{"urltest", "fallback"} {
		for _, failed := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/healthy", true: "/failed"}[failed], func(t *testing.T) {
				failovers, selections := 0, 0
				oldNow, oldSelection := OnGroupNowChanged, OnGroupSelectionChanged
				OnGroupNowChanged = func(C.ProxyAdapter, string, string, string) { failovers++ }
				OnGroupSelectionChanged = func(string, string, string) { selections++ }
				t.Cleanup(func() { OnGroupNowChanged, OnGroupSelectionChanged = oldNow, oldSelection })

				old := &reviewHealthProxy{urlTestMockProxy: urlTestMockProxy{name: "old"}, probeDelay: 200}
				old.alive.Store(true)
				old.lastDelay.Store(10)
				if failed {
					old.probeErr = errors.New("node unreachable")
				}
				next := &reviewHealthProxy{urlTestMockProxy: urlTestMockProxy{name: "next"}, probeDelay: 20}
				next.alive.Store(kind == "urltest")
				next.lastDelay.Store(100)
				members := []C.Proxy{old, next}
				if kind == "fallback" {
					members = []C.Proxy{next, old}
				}
				providers := []P.ProxyProvider{&concurrencyMockProvider{proxies: members}}
				options := GroupCommonOption{Name: "review", URL: "http://example.test", TestTimeout: 300}
				var group interface {
					Now() string
					HealthCheckContext(context.Context) (map[string]uint16, error)
				}
				var err error
				if kind == "urltest" {
					group, err = NewURLTest(options, URLTestOption{}, namedProxy("DIRECT"), providers)
				} else {
					group, err = NewFallback(options, FallbackOption{}, namedProxy("DIRECT"), providers)
				}
				require.NoError(t, err)
				require.Equal(t, "old", group.Now())
				_, err = group.HealthCheckContext(context.Background())
				require.NoError(t, err)
				require.Equal(t, "next", group.Now())
				if failed {
					require.Equal(t, 1, failovers)
					require.Zero(t, selections)
				} else {
					require.Zero(t, failovers, "healthy old node must keep its connections")
					require.Equal(t, 1, selections)
				}
			})
		}
	}
}

type reviewProbeConn struct {
	C.Conn
	handshake bool
	writeErr  error
}

func (c *reviewProbeConn) AppendToChains(C.ProxyAdapter) {}
func (c *reviewProbeConn) Upstream() any                 { return nil }
func (c *reviewProbeConn) NeedHandshake() bool           { return c.handshake }
func (c *reviewProbeConn) Write(b []byte) (int, error)   { return len(b), c.writeErr }

type reviewDialProxy struct {
	urlTestMockProxy
	handshake bool
	dialErr   error
	writeErr  error
}

func (p *reviewDialProxy) DialContext(context.Context, *C.Metadata) (C.Conn, error) {
	if p.dialErr != nil {
		return nil, p.dialErr
	}
	return &reviewProbeConn{handshake: p.handshake, writeErr: p.writeErr}, nil
}

func TestReviewSyntheticSuccessPreservesPassiveFailures(t *testing.T) {
	for _, kind := range []string{"urltest", "fallback", "loadbalance"} {
		for _, handshake := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/immediate", true: "/first-write"}[handshake], func(t *testing.T) {
				proxy := &reviewDialProxy{urlTestMockProxy: urlTestMockProxy{name: "node"}, handshake: handshake}
				proxy.alive.Store(true)
				proxy.lastDelay.Store(10)
				providers := []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{proxy}}}
				options := GroupCommonOption{Name: "review", URL: "http://example.test"}
				var group C.ProxyAdapter
				var base *GroupBase
				if kind == "urltest" {
					g, err := NewURLTest(options, URLTestOption{}, namedProxy("DIRECT"), providers)
					require.NoError(t, err)
					group, base = g, g.GroupBase
				} else if kind == "fallback" {
					g, err := NewFallback(options, FallbackOption{}, namedProxy("DIRECT"), providers)
					require.NoError(t, err)
					group, base = g, g.GroupBase
				} else {
					g, err := NewLoadBalance(options, LoadBalanceOption{Strategy: "round-robin"}, namedProxy("DIRECT"), providers)
					require.NoError(t, err)
					group, base = g, g.GroupBase
				}
				base.failedTargets = []failedTarget{{host: "a.test", at: time.Now()}, {host: "b.test", at: time.Now()}}
				base.consecutiveAllFailures = 2
				conn, err := group.DialContext(C.WithLatencyTestContext(context.Background()), &C.Metadata{Host: "probe.test"})
				require.NoError(t, err)
				_, err = conn.Write([]byte("probe"))
				require.NoError(t, err)
				require.Len(t, base.failedTargets, 2, "synthetic success must not erase real failure evidence")
				require.Equal(t, 2, base.ConsecutiveFailures())
				require.True(t, base.LastTrafficAt().IsZero())

				conn, err = group.DialContext(context.Background(), &C.Metadata{Host: "real.test"})
				require.NoError(t, err)
				_, err = conn.Write([]byte("traffic"))
				require.NoError(t, err)
				require.Empty(t, base.failedTargets, "real success should reset failures")
				require.Zero(t, base.ConsecutiveFailures())
			})
		}
	}
}

func TestReviewLoadBalanceSyntheticFailurePreservesPassiveFailures(t *testing.T) {
	for _, handshake := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate", true: "first-write"}[handshake], func(t *testing.T) {
			failure := errors.New("test target unavailable")
			proxy := &reviewDialProxy{urlTestMockProxy: urlTestMockProxy{name: "node"}, handshake: handshake}
			proxy.alive.Store(true)
			if handshake {
				proxy.writeErr = failure
			} else {
				proxy.dialErr = failure
			}
			group, err := NewLoadBalance(
				GroupCommonOption{Name: "review", URL: "http://example.test"},
				LoadBalanceOption{Strategy: "round-robin"}, namedProxy("DIRECT"),
				[]P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{proxy}}},
			)
			require.NoError(t, err)
			for _, synthetic := range []bool{true, false} {
				ctx := context.Background()
				if synthetic {
					ctx = C.WithLatencyTestContext(ctx)
				}
				conn, err := group.DialContext(ctx, &C.Metadata{Host: "example.test"})
				if handshake {
					require.NoError(t, err)
					_, err = conn.Write([]byte("request"))
				}
				require.ErrorIs(t, err, failure)
				if synthetic {
					require.Empty(t, group.failedTargets, "synthetic failure must not create passive evidence")
				} else {
					require.Len(t, group.failedTargets, 1, "real failure should create passive evidence")
				}
			}
		})
	}
}
