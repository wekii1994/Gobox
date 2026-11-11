package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outboundgroup"
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

	// 5. Group with ErrHealthCheckInFlight -> counts as satisfied (returns true) without error retry
	gInFlight := adapter.NewProxy(&mockAutoGroupAdapter{
		name:      "gInFlight",
		groupType: constant.URLTest,
		proxies:   make([]constant.Proxy, 2),
		healthCheckContextFunc: func(ctx context.Context) (map[string]uint16, error) {
			return nil, outboundgroup.ErrHealthCheckInFlight
		},
	})
	if ok := probeGroupsConcurrently(context.Background(), []*adapter.Proxy{gInFlight}); !ok {
		t.Fatalf("expected true when group check returns ErrHealthCheckInFlight, got false")
	}

	// Wrapped ErrHealthCheckInFlight is also recognized via errors.Is
	gWrappedInFlight := adapter.NewProxy(&mockAutoGroupAdapter{
		name:      "gWrappedInFlight",
		groupType: constant.Fallback,
		proxies:   make([]constant.Proxy, 2),
		healthCheckContextFunc: func(ctx context.Context) (map[string]uint16, error) {
			return nil, fmt.Errorf("wrapped: %w", outboundgroup.ErrHealthCheckInFlight)
		},
	})
	if ok := probeGroupsConcurrently(context.Background(), []*adapter.Proxy{gSuccess, gWrappedInFlight}); !ok {
		t.Fatalf("expected true with wrapped ErrHealthCheckInFlight, got false")
	}
	if !errors.Is(fmt.Errorf("wrapped: %w", outboundgroup.ErrHealthCheckInFlight), outboundgroup.ErrHealthCheckInFlight) {
		t.Fatal("sanity check failed")
	}
}

func TestCalculateGroupProbeBudgetProductionGroups(t *testing.T) {
	emptyFallback := adapter.NewProxy(&mockSelectorAdapter{name: "DIRECT"})

	urlTestCustom, err := outboundgroup.NewURLTest(
		outboundgroup.GroupCommonOption{
			Name:        "ProdURLCustom",
			URL:         "https://www.gstatic.com/generate_204",
			TestTimeout: 2000,
		},
		outboundgroup.URLTestOption{Tolerance: 50},
		emptyFallback,
		nil,
	)
	if err != nil {
		t.Fatalf("NewURLTest custom failed: %v", err)
	}
	if got := urlTestCustom.TestTimeout(); got != 2*time.Second {
		t.Fatalf("URLTest.TestTimeout() = %v, want 2s", got)
	}
	if budget := calculateGroupProbeBudget(adapter.NewProxy(urlTestCustom)); budget != 2500*time.Millisecond {
		t.Fatalf("URLTest custom budget = %v, want 2500ms", budget)
	}

	urlTestDefault, err := outboundgroup.NewURLTest(
		outboundgroup.GroupCommonOption{
			Name:        "ProdURLDefault",
			URL:         "https://www.gstatic.com/generate_204",
			TestTimeout: 0,
		},
		outboundgroup.URLTestOption{},
		emptyFallback,
		nil,
	)
	if err != nil {
		t.Fatalf("NewURLTest default failed: %v", err)
	}
	if got := urlTestDefault.TestTimeout(); got != 5*time.Second {
		t.Fatalf("URLTest default TestTimeout() = %v, want 5s", got)
	}
	if budget := calculateGroupProbeBudget(adapter.NewProxy(urlTestDefault)); budget != 5500*time.Millisecond {
		t.Fatalf("URLTest default budget = %v, want 5500ms", budget)
	}

	fallbackCustom, err := outboundgroup.NewFallback(
		outboundgroup.GroupCommonOption{
			Name:        "ProdFallbackCustom",
			URL:         "https://www.gstatic.com/generate_204",
			TestTimeout: 3000,
		},
		outboundgroup.FallbackOption{},
		emptyFallback,
		nil,
	)
	if err != nil {
		t.Fatalf("NewFallback custom failed: %v", err)
	}
	if got := fallbackCustom.TestTimeout(); got != 3*time.Second {
		t.Fatalf("Fallback.TestTimeout() = %v, want 3s", got)
	}
	if budget := calculateGroupProbeBudget(adapter.NewProxy(fallbackCustom)); budget != 3500*time.Millisecond {
		t.Fatalf("Fallback custom budget = %v, want 3500ms", budget)
	}
}

func TestClassifyAutoGroupsDeepChainsCyclesAndConvergence(t *testing.T) {
	t.Cleanup(func() { tunnel.UpdateProxies(nil, nil) })

	// 1. Deep chain (> 6 hops): Sel1 -> Sel2 -> ... -> Sel8 -> DeepAuto, plus an unused Auto group
	deepProxies := make(map[string]constant.Proxy)
	for i := 1; i <= 8; i++ {
		next := fmt.Sprintf("Sel%d", i+1)
		if i == 8 {
			next = "DeepAuto"
		}
		name := fmt.Sprintf("Sel%d", i)
		deepProxies[name] = adapter.NewProxy(&mockSelectorAdapter{name: name, now: next})
	}
	deepProxies["DeepAuto"] = adapter.NewProxy(&mockAutoGroupAdapter{name: "DeepAuto", groupType: constant.URLTest})
	deepProxies["UnusedAuto"] = adapter.NewProxy(&mockAutoGroupAdapter{name: "UnusedAuto", groupType: constant.Fallback})
	tunnel.UpdateProxies(deepProxies, nil)

	primary, secondary := classifyAutoGroups()
	if len(primary) != 1 || primary[0].Name() != "DeepAuto" {
		t.Fatalf("expected DeepAuto in primary beyond 6-hop chain, got %v", primary)
	}
	if len(secondary) != 1 || secondary[0].Name() != "UnusedAuto" {
		t.Fatalf("expected UnusedAuto in secondary, got %v", secondary)
	}

	// 2. Cyclic selectors + active primary & secondary coexistence
	tunnel.UpdateProxies(map[string]constant.Proxy{
		"SelCycleA":     adapter.NewProxy(&mockSelectorAdapter{name: "SelCycleA", now: "SelCycleB"}),
		"SelCycleB":     adapter.NewProxy(&mockSelectorAdapter{name: "SelCycleB", now: "SelCycleA"}),
		"SelEntry":      adapter.NewProxy(&mockSelectorAdapter{name: "SelEntry", now: "PrimaryAuto"}),
		"PrimaryAuto":   adapter.NewProxy(&mockAutoGroupAdapter{name: "PrimaryAuto", groupType: constant.URLTest}),
		"SecondaryAuto": adapter.NewProxy(&mockAutoGroupAdapter{name: "SecondaryAuto", groupType: constant.URLTest}),
	}, nil)

	primary, secondary = classifyAutoGroups()
	if len(primary) != 1 || primary[0].Name() != "PrimaryAuto" {
		t.Fatalf("expected PrimaryAuto despite selector cycle, got %v", primary)
	}
	if len(secondary) != 1 || secondary[0].Name() != "SecondaryAuto" {
		t.Fatalf("expected SecondaryAuto in secondary, got %v", secondary)
	}

	// 3. Convergent selectors sharing downstream selector -> auto group + unused secondary
	tunnel.UpdateProxies(map[string]constant.Proxy{
		"SelLeft":       adapter.NewProxy(&mockSelectorAdapter{name: "SelLeft", now: "SharedMid"}),
		"SelRight":      adapter.NewProxy(&mockSelectorAdapter{name: "SelRight", now: "SharedMid"}),
		"SharedMid":     adapter.NewProxy(&mockSelectorAdapter{name: "SharedMid", now: "ConvergedAuto"}),
		"ConvergedAuto": adapter.NewProxy(&mockAutoGroupAdapter{name: "ConvergedAuto", groupType: constant.Fallback}),
		"IdleAuto":      adapter.NewProxy(&mockAutoGroupAdapter{name: "IdleAuto", groupType: constant.URLTest}),
	}, nil)

	primary, secondary = classifyAutoGroups()
	if len(primary) != 1 || primary[0].Name() != "ConvergedAuto" {
		t.Fatalf("expected ConvergedAuto in primary, got %v", primary)
	}
	if len(secondary) != 1 || secondary[0].Name() != "IdleAuto" {
		t.Fatalf("expected IdleAuto in secondary, got %v", secondary)
	}
}
