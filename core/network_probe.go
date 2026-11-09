package main

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outboundgroup"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
)

const (
	networkSettleDelay      = 300 * time.Millisecond
	networkRetryBackoff     = 1200 * time.Millisecond
	primaryProbeConcurrency = 3
)

var (
	resetNetworkMu     sync.Mutex
	resetNetworkCancel context.CancelFunc
	networkProbeRound  atomic.Uint64
)

// runBoundedProbes owns both the semaphore token and WaitGroup registration.
// A canceled submission never creates a goroutine without a token.
func runBoundedProbes(ctx context.Context, count, limit int, probe func(context.Context, int) bool) bool {
	if count <= 0 || limit <= 0 || ctx.Err() != nil {
		return false
	}
	if limit > count {
		limit = count
	}
	slots := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var mu sync.Mutex
	anySuccess := false
submit:
	for index := 0; index < count; index++ {
		if ctx.Err() != nil {
			break
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break submit
		}
		if ctx.Err() != nil {
			<-slots
			break
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-slots }()
			if ctx.Err() != nil {
				return
			}
			if probe(ctx, i) {
				mu.Lock()
				anySuccess = true
				mu.Unlock()
			}
		}(index)
	}
	wg.Wait()
	return anySuccess
}

func isAutoGroup(p *adapter.Proxy) bool {
	if p == nil {
		return false
	}
	switch p.Type() {
	case constant.URLTest, constant.Fallback:
		return true
	default:
		return false
	}
}

func hasPrimaryAutoKeyword(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range []string{"自动", "auto", "url-test", "best", "优选", "极速", "主力"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// classifyAutoGroups partitions auto groups into primary and secondary based on active selector chains and naming.
func classifyAutoGroups() (primary []*adapter.Proxy, secondary []*adapter.Proxy) {
	allProxies := tunnel.Proxies()
	activeNames := make(map[string]struct{})

	for _, raw := range allProxies {
		p, ok := raw.(*adapter.Proxy)
		if !ok || p.Type() != constant.Selector {
			continue
		}
		curr := p
		for depth := 0; depth < 6 && curr != nil; depth++ {
			pg, ok := curr.ProxyAdapter.(outboundgroup.ProxyGroup)
			if !ok {
				break
			}
			nowName := pg.Now()
			if nowName == "" {
				break
			}
			if _, visited := activeNames[nowName]; visited {
				break
			}
			activeNames[nowName] = struct{}{}
			nextRaw, exists := allProxies[nowName]
			if !exists {
				break
			}
			nextProxy, ok := nextRaw.(*adapter.Proxy)
			if !ok {
				break
			}
			curr = nextProxy
		}
	}

	for _, raw := range allProxies {
		p, ok := raw.(*adapter.Proxy)
		if !ok || !isAutoGroup(p) {
			continue
		}
		_, isActive := activeNames[p.Name()]
		if isActive {
			primary = append(primary, p)
		} else {
			secondary = append(secondary, p)
		}
	}

	if len(primary) == 0 && len(secondary) > 0 {
		primary = secondary
		secondary = nil
	}

	sort.SliceStable(primary, func(i, j int) bool {
		_, aActive := activeNames[primary[i].Name()]
		_, bActive := activeNames[primary[j].Name()]
		if aActive != bActive {
			return aActive
		}
		aHasKw := hasPrimaryAutoKeyword(primary[i].Name())
		bHasKw := hasPrimaryAutoKeyword(primary[j].Name())
		if aHasKw != bHasKw {
			return aHasKw
		}
		return primary[i].Name() < primary[j].Name()
	})
	return primary, secondary
}

type failureResetter interface {
	ResetConsecutiveFailures()
}

func resetAllGroupsConsecutiveFailures() {
	for _, raw := range tunnel.Proxies() {
		if p, ok := raw.(*adapter.Proxy); ok {
			if r, ok := p.ProxyAdapter.(failureResetter); ok {
				r.ResetConsecutiveFailures()
			}
		}
	}
}

type candidateCountGroup interface {
	GetProxies(touch bool) []constant.Proxy
}

type timeoutAwareGroup interface {
	TestTimeout() time.Duration
}

func getGroupCandidateCount(proxy *adapter.Proxy) int {
	if cg, ok := proxy.ProxyAdapter.(candidateCountGroup); ok {
		return len(cg.GetProxies(false))
	}
	if pg, ok := proxy.ProxyAdapter.(outboundgroup.ProxyGroup); ok {
		return len(pg.Proxies())
	}
	return 1
}

func getGroupTestTimeout(proxy *adapter.Proxy) time.Duration {
	if tg, ok := proxy.ProxyAdapter.(timeoutAwareGroup); ok {
		if d := tg.TestTimeout(); d > 0 {
			return d
		}
	}
	return 5 * time.Second
}

func calculateGroupProbeBudget(proxy *adapter.Proxy, competingCandidates ...int) time.Duration {
	count := getGroupCandidateCount(proxy)
	competing := 0
	if len(competingCandidates) > 0 && competingCandidates[0] > 0 {
		competing = competingCandidates[0]
	}
	timeout := getGroupTestTimeout(proxy)
	return outboundgroup.RoundBudget(count+competing, timeout)
}

type contextHealthCheckedGroup interface {
	HealthCheckContext(context.Context) (map[string]uint16, error)
}

func probeGroupsConcurrently(ctx context.Context, groups []*adapter.Proxy) bool {
	if len(groups) == 0 {
		return false
	}
	totalCandidates := 0
	for _, g := range groups {
		totalCandidates += getGroupCandidateCount(g)
	}

	var completedGroups atomic.Int32

	_ = runBoundedProbes(ctx, len(groups), primaryProbeConcurrency,
		func(probeCtx context.Context, index int) bool {
			g := groups[index]
			checker, ok := g.ProxyAdapter.(contextHealthCheckedGroup)
			if !ok {
				return false
			}
			done := markNetworkProbing(g.Name())
			defer done()

			groupCount := getGroupCandidateCount(g)
			competingCandidates := totalCandidates - groupCount
			if competingCandidates < 0 {
				competingCandidates = 0
			}
			budget := calculateGroupProbeBudget(g, competingCandidates)
			testCtx, cancel := context.WithTimeout(probeCtx, budget)
			defer cancel()
			results, err := checker.HealthCheckContext(testCtx)

			if err == nil && len(results) > 0 {
				completedGroups.Add(1)
				return true
			}
			return false
		})

	return int(completedGroups.Load()) == len(groups)
}

func waitOrCancel(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func cancelResetNetwork() {
	resetNetworkMu.Lock()
	if resetNetworkCancel != nil {
		resetNetworkCancel()
		resetNetworkCancel = nil
	}
	resetNetworkMu.Unlock()
}

func handleResetNetwork() {
	closeConnections()
	closeIdleOutbounds(tunnel.Proxies(), tunnel.Providers())
	resolver.ResetConnection()
	handleFlushDnsCache()
	resetAllGroupsConsecutiveFailures()

	resetNetworkMu.Lock()
	if resetNetworkCancel != nil {
		resetNetworkCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	resetNetworkCancel = cancel
	resetNetworkMu.Unlock()

	probeRound := networkProbeRound.Add(1)
	resetCtx := outboundgroup.WithProbeRound(ctx, probeRound)

	go func(resetCtx context.Context) {
		defer cancel()
		defer outboundgroup.CleanupProbeRound(probeRound)

		if !waitOrCancel(resetCtx, networkSettleDelay) {
			return
		}

		primaryGroups, _ := classifyAutoGroups()
		if len(primaryGroups) > 0 {
			ok := probeGroupsConcurrently(resetCtx, primaryGroups)
			if !ok && waitOrCancel(resetCtx, networkRetryBackoff) {
				handleFlushDnsCache()
				retryRound := networkProbeRound.Add(1)
				retryCtx := outboundgroup.WithProbeRound(resetCtx, retryRound)
				defer outboundgroup.CleanupProbeRound(retryRound)
				_ = probeGroupsConcurrently(retryCtx, primaryGroups)
			}
		}
	}(resetCtx)
}
