package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/common/utils"
	"github.com/metacubex/mihomo/constant"
	cp "github.com/metacubex/mihomo/constant/provider"
	"github.com/metacubex/mihomo/tunnel"
)

type mockSelectorAdapter struct {
	constant.ProxyAdapter
	name string
	now  string
}

func (m *mockSelectorAdapter) Name() string                     { return m.name }
func (m *mockSelectorAdapter) Type() constant.AdapterType       { return constant.Selector }
func (m *mockSelectorAdapter) Now() string                      { return m.now }
func (m *mockSelectorAdapter) Touch()                           {}
func (m *mockSelectorAdapter) Hidden() bool                     { return false }
func (m *mockSelectorAdapter) Icon() string                     { return "" }
func (m *mockSelectorAdapter) Providers() []cp.ProxyProvider    { return nil }
func (m *mockSelectorAdapter) Proxies() []constant.Proxy        { return nil }
func (m *mockSelectorAdapter) URLTest(ctx context.Context, url string, expectedStatus utils.IntRanges[uint16]) (map[string]uint16, error) {
	return nil, nil
}

type mockAutoGroupAdapter struct {
	constant.ProxyAdapter
	name                   string
	groupType              constant.AdapterType
	proxies                []constant.Proxy
	consecutiveResets      atomic.Int32
	testTimeout            time.Duration
	healthCheckContextFunc func(context.Context) (map[string]uint16, error)
}

func (m *mockAutoGroupAdapter) Name() string                     { return m.name }
func (m *mockAutoGroupAdapter) Type() constant.AdapterType       { return m.groupType }
func (m *mockAutoGroupAdapter) Now() string                      { return "" }
func (m *mockAutoGroupAdapter) Touch()                           {}
func (m *mockAutoGroupAdapter) Hidden() bool                     { return false }
func (m *mockAutoGroupAdapter) Icon() string                     { return "" }
func (m *mockAutoGroupAdapter) Providers() []cp.ProxyProvider    { return nil }
func (m *mockAutoGroupAdapter) Proxies() []constant.Proxy        { return m.proxies }
func (m *mockAutoGroupAdapter) GetProxies(bool) []constant.Proxy { return m.proxies }
func (m *mockAutoGroupAdapter) TestTimeout() time.Duration       { return m.testTimeout }
func (m *mockAutoGroupAdapter) ResetConsecutiveFailures()         { m.consecutiveResets.Add(1) }
func (m *mockAutoGroupAdapter) HealthCheckContext(ctx context.Context) (map[string]uint16, error) {
	if m.healthCheckContextFunc != nil {
		return m.healthCheckContextFunc(ctx)
	}
	return nil, nil
}
func (m *mockAutoGroupAdapter) URLTest(ctx context.Context, url string, expectedStatus utils.IntRanges[uint16]) (map[string]uint16, error) {
	return nil, nil
}

func TestClassifyAutoGroupsActiveVsKeywords(t *testing.T) {
	activeAuto := &mockAutoGroupAdapter{name: "RegularAuto", groupType: constant.URLTest}
	inactiveKeywordAuto := &mockAutoGroupAdapter{name: "Inactive-Auto-Test", groupType: constant.URLTest}
	selector := &mockSelectorAdapter{name: "PROXY", now: "RegularAuto"}

	tunnel.UpdateProxies(map[string]constant.Proxy{
		"RegularAuto":        adapter.NewProxy(activeAuto),
		"Inactive-Auto-Test": adapter.NewProxy(inactiveKeywordAuto),
		"PROXY":              adapter.NewProxy(selector),
	}, nil)
	t.Cleanup(func() { tunnel.UpdateProxies(nil, nil) })

	primary, secondary := classifyAutoGroups()

	if len(primary) != 1 || primary[0].Name() != "RegularAuto" {
		t.Fatalf("expected primary to contain only RegularAuto, got %v", primary)
	}
	if len(secondary) != 1 || secondary[0].Name() != "Inactive-Auto-Test" {
		t.Fatalf("expected secondary to contain Inactive-Auto-Test, got %v", secondary)
	}

	tunnel.UpdateProxies(map[string]constant.Proxy{
		"PlainGroup": adapter.NewProxy(&mockAutoGroupAdapter{name: "PlainGroup", groupType: constant.URLTest}),
		"AutoGroup":  adapter.NewProxy(&mockAutoGroupAdapter{name: "AutoGroup", groupType: constant.URLTest}),
	}, nil)

	primaryFallback, secondaryFallback := classifyAutoGroups()
	if len(secondaryFallback) != 0 {
		t.Fatalf("expected 0 secondary on fallback, got %d", len(secondaryFallback))
	}
	if len(primaryFallback) != 2 {
		t.Fatalf("expected 2 primary on fallback, got %d", len(primaryFallback))
	}
	if primaryFallback[0].Name() != "AutoGroup" {
		t.Fatalf("expected AutoGroup (keyword) to sort before PlainGroup, got %s", primaryFallback[0].Name())
	}
}

func TestCalculateGroupProbeBudget(t *testing.T) {
	g1 := adapter.NewProxy(&mockAutoGroupAdapter{
		name:    "g1",
		proxies: make([]constant.Proxy, 1),
	})
	if b := calculateGroupProbeBudget(g1); b != 5500*time.Millisecond {
		t.Fatalf("expected 5500ms for 1 proxy with default timeout, got %v", b)
	}

	g20 := adapter.NewProxy(&mockAutoGroupAdapter{
		name:    "g20",
		proxies: make([]constant.Proxy, 20),
	})
	if b := calculateGroupProbeBudget(g20); b != 10500*time.Millisecond {
		t.Fatalf("expected 10500ms for 20 proxies with default timeout, got %v", b)
	}

	g100 := adapter.NewProxy(&mockAutoGroupAdapter{
		name:    "g100",
		proxies: make([]constant.Proxy, 100),
	})
	if b := calculateGroupProbeBudget(g100); b != 30*time.Second {
		t.Fatalf("expected 30s cap for 100 proxies, got %v", b)
	}

	gCustom := adapter.NewProxy(&mockAutoGroupAdapter{
		name:        "gCustom",
		proxies:     make([]constant.Proxy, 1),
		testTimeout: 2 * time.Second,
	})
	if b := calculateGroupProbeBudget(gCustom); b != 2500*time.Millisecond {
		t.Fatalf("expected 2500ms for custom 2s timeout, got %v", b)
	}
}

func TestResetAllGroupsConsecutiveFailures(t *testing.T) {
	g := &mockAutoGroupAdapter{name: "auto-group", groupType: constant.URLTest}
	tunnel.UpdateProxies(map[string]constant.Proxy{
		"auto-group": adapter.NewProxy(g),
	}, nil)
	t.Cleanup(func() { tunnel.UpdateProxies(nil, nil) })

	resetAllGroupsConsecutiveFailures()
	if count := g.consecutiveResets.Load(); count != 1 {
		t.Fatalf("expected 1 reset call, got %d", count)
	}
}

func TestProbeGroupsConcurrentlyCompletionContract(t *testing.T) {
	// 1. Single group complete success
	gSuccess := adapter.NewProxy(&mockAutoGroupAdapter{
		name:      "gSuccess",
		groupType: constant.URLTest,
		proxies:   make([]constant.Proxy, 2),
		healthCheckContextFunc: func(ctx context.Context) (map[string]uint16, error) {
			return map[string]uint16{"n1": 50, "n2": 80}, nil
		},
	})
	if ok := probeGroupsConcurrently(context.Background(), []*adapter.Proxy{gSuccess}); !ok {
		t.Fatalf("expected true for fully complete group check, got false")
	}

	// 2. Single group partial success (deadline exceeded) -> must return false
	gPartial := adapter.NewProxy(&mockAutoGroupAdapter{
		name:      "gPartial",
		groupType: constant.URLTest,
		proxies:   make([]constant.Proxy, 5),
		healthCheckContextFunc: func(ctx context.Context) (map[string]uint16, error) {
			return map[string]uint16{"n1": 50}, context.DeadlineExceeded
		},
	})
	if ok := probeGroupsConcurrently(context.Background(), []*adapter.Proxy{gPartial}); ok {
		t.Fatalf("expected false for deadline-truncated partial group check, got true")
	}

	// 3. Multiple groups: Group 1 complete, Group 2 partial -> must return false
	if ok := probeGroupsConcurrently(context.Background(), []*adapter.Proxy{gSuccess, gPartial}); ok {
		t.Fatalf("expected false when any group check is incomplete, got true")
	}

	// 4. Multiple groups: Both complete -> must return true
	gSuccess2 := adapter.NewProxy(&mockAutoGroupAdapter{
		name:      "gSuccess2",
		groupType: constant.Fallback,
		proxies:   make([]constant.Proxy, 1),
		healthCheckContextFunc: func(ctx context.Context) (map[string]uint16, error) {
			return map[string]uint16{"fb1": 100}, nil
		},
	})
	if ok := probeGroupsConcurrently(context.Background(), []*adapter.Proxy{gSuccess, gSuccess2}); !ok {
		t.Fatalf("expected true when all group checks succeed, got false")
	}
}
