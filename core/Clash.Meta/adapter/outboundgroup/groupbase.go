package outboundgroup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/common/atomic"
	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
	"github.com/metacubex/mihomo/log"

	"github.com/dlclark/regexp2"
	"golang.org/x/exp/slices"
)

const (
	failSlidingWindow   = 5 * time.Second
	failoverCooldown    = 10 * time.Second
	maxFailedTargetsCap = 16
)

type failedTarget struct {
	host string
	at   time.Time
}

var OnGroupNowChanged func(group C.ProxyAdapter, groupName, before, after string)

type GroupBase struct {
	*outbound.Base
	hidden            bool
	icon              string
	filterRegs        []*regexp2.Regexp
	excludeFilterRegs []*regexp2.Regexp
	excludeTypeArray  []string
	providers         []P.ProxyProvider
	failedTestMux          sync.Mutex
	failedTargets          []failedTarget
	probing                atomic.Bool
	providerTesting        atomic.Bool
	lastTriggerTime        time.Time
	testTimeout            int
	maxFailedTimes         int
	consecutiveAllFailures int
	emptyFallback          C.Proxy

	// for GetProxies
	getProxiesMutex  sync.Mutex
	providerVersions []uint32
	providerProxies  []C.Proxy
}

type GroupBaseOption struct {
	Name           string
	Type           C.AdapterType
	Hidden         bool
	Icon           string
	Filter         string
	ExcludeFilter  string
	ExcludeType    string
	TestTimeout    int
	MaxFailedTimes int
	EmptyFallback  C.Proxy
	Providers      []P.ProxyProvider
}

func NewGroupBase(opt GroupBaseOption) *GroupBase {
	var excludeTypeArray []string
	if opt.ExcludeType != "" {
		excludeTypeArray = strings.Split(opt.ExcludeType, "|")
	}

	var excludeFilterRegs []*regexp2.Regexp
	if opt.ExcludeFilter != "" {
		for _, excludeFilter := range strings.Split(opt.ExcludeFilter, "`") {
			excludeFilterReg := regexp2.MustCompile(excludeFilter, regexp2.None)
			excludeFilterRegs = append(excludeFilterRegs, excludeFilterReg)
		}
	}

	var filterRegs []*regexp2.Regexp
	if opt.Filter != "" {
		for _, filter := range strings.Split(opt.Filter, "`") {
			filterReg := regexp2.MustCompile(filter, regexp2.None)
			filterRegs = append(filterRegs, filterReg)
		}
	}

	gb := &GroupBase{
		Base:              outbound.NewBase(outbound.BaseOption{Name: opt.Name, Type: opt.Type}),
		hidden:            opt.Hidden,
		icon:              opt.Icon,
		filterRegs:        filterRegs,
		excludeFilterRegs: excludeFilterRegs,
		excludeTypeArray:  excludeTypeArray,
		providers:         opt.Providers,
		probing:           atomic.NewBool(false),
		providerTesting:   atomic.NewBool(false),
		testTimeout:       opt.TestTimeout,
		maxFailedTimes:    opt.MaxFailedTimes,
		emptyFallback:     opt.EmptyFallback,
	}

	if gb.testTimeout == 0 {
		gb.testTimeout = 5000
	}
	if gb.maxFailedTimes == 0 {
		gb.maxFailedTimes = 5
	}

	return gb
}

func (gb *GroupBase) Hidden() bool {
	return gb.hidden
}

func (gb *GroupBase) Icon() string {
	return gb.icon
}

func (gb *GroupBase) EmptyFallback() C.Proxy {
	return gb.emptyFallback
}

func (gb *GroupBase) Touch() {
	for _, pd := range gb.providers {
		pd.Touch()
	}
}

func (gb *GroupBase) GetProxies(touch bool) []C.Proxy {
	providerVersions := make([]uint32, len(gb.providers))
	for i, pd := range gb.providers {
		if touch { // touch first
			pd.Touch()
		}
		providerVersions[i] = pd.Version()
	}

	// thread safe
	gb.getProxiesMutex.Lock()
	defer gb.getProxiesMutex.Unlock()

	// return the cached proxies if version not changed
	if slices.Equal(providerVersions, gb.providerVersions) {
		return gb.providerProxies
	}

	var proxies []C.Proxy
	if len(gb.filterRegs) == 0 {
		for _, pd := range gb.providers {
			proxies = append(proxies, pd.Proxies()...)
		}
	} else {
		for _, pd := range gb.providers {
			if pd.VehicleType() == P.Compatible { // compatible provider unneeded filter
				proxies = append(proxies, pd.Proxies()...)
				continue
			}

			var newProxies []C.Proxy
			proxiesSet := map[string]struct{}{}
			for _, filterReg := range gb.filterRegs {
				for _, p := range pd.Proxies() {
					name := p.Name()
					if mat, _ := filterReg.MatchString(name); mat {
						if _, ok := proxiesSet[name]; !ok {
							proxiesSet[name] = struct{}{}
							newProxies = append(newProxies, p)
						}
					}
				}
			}
			proxies = append(proxies, newProxies...)
		}
	}

	// Multiple filers means that proxies are sorted in the order in which the filers appear.
	// Although the filter has been performed once in the previous process,
	// when there are multiple providers, the array needs to be reordered as a whole.
	if len(gb.providers) > 1 && len(gb.filterRegs) > 1 {
		var newProxies []C.Proxy
		proxiesSet := map[string]struct{}{}
		for _, filterReg := range gb.filterRegs {
			for _, p := range proxies {
				name := p.Name()
				if mat, _ := filterReg.MatchString(name); mat {
					if _, ok := proxiesSet[name]; !ok {
						proxiesSet[name] = struct{}{}
						newProxies = append(newProxies, p)
					}
				}
			}
		}
		for _, p := range proxies { // add not matched proxies at the end
			name := p.Name()
			if _, ok := proxiesSet[name]; !ok {
				proxiesSet[name] = struct{}{}
				newProxies = append(newProxies, p)
			}
		}
		proxies = newProxies
	}

	if len(gb.excludeFilterRegs) > 0 {
		var newProxies []C.Proxy
	LOOP1:
		for _, p := range proxies {
			name := p.Name()
			for _, excludeFilterReg := range gb.excludeFilterRegs {
				if mat, _ := excludeFilterReg.MatchString(name); mat {
					continue LOOP1
				}
			}
			newProxies = append(newProxies, p)
		}
		proxies = newProxies
	}

	if gb.excludeTypeArray != nil {
		var newProxies []C.Proxy
	LOOP2:
		for _, p := range proxies {
			mType := p.Type().String()
			for _, excludeType := range gb.excludeTypeArray {
				if strings.EqualFold(mType, excludeType) {
					continue LOOP2
				}
			}
			newProxies = append(newProxies, p)
		}
		proxies = newProxies
	}

	if len(proxies) == 0 {
		return []C.Proxy{gb.EmptyFallback()}
	}

	// only cache when proxies not empty
	gb.providerVersions = providerVersions
	gb.providerProxies = proxies

	return proxies
}

const maxConcurrentProxyProbes = 10

var globalProbeSem = make(chan struct{}, maxConcurrentProxyProbes)

func RoundBudget(proxiesCount int, nodeTimeout time.Duration) time.Duration {
	if nodeTimeout <= 0 {
		nodeTimeout = 5 * time.Second
	}
	batches := (proxiesCount + maxConcurrentProxyProbes - 1) / maxConcurrentProxyProbes
	if batches < 1 {
		batches = 1
	}
	budget := time.Duration(batches)*nodeTimeout + 500*time.Millisecond
	if budget < nodeTimeout+500*time.Millisecond {
		budget = nodeTimeout + 500*time.Millisecond
	}
	if budget > 30*time.Second {
		budget = 30 * time.Second
	}
	return budget
}

func roundBudget(proxiesCount int, nodeTimeout time.Duration) time.Duration {
	return RoundBudget(proxiesCount, nodeTimeout)
}

type probeTask struct {
	cancel   context.CancelFunc
	waiters  int
	done     chan struct{}
	val      uint16
	err      error
	retiring bool
}

type cancellableProbePool struct {
	mu           sync.Mutex
	tasks        map[string]*probeTask
	currentRound any
	results      map[string]uint16
}

var globalProbePool = &cancellableProbePool{
	tasks:   make(map[string]*probeTask),
	results: make(map[string]uint16),
}

func (p *cancellableProbePool) CleanupRound(round any) {
	if round == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.currentRound == round {
		p.currentRound = nil
		p.results = nil
	}
}

func CleanupProbeRound(round any) {
	globalProbePool.CleanupRound(round)
}

type probeRoundCtxKey struct{}

func WithProbeRound(ctx context.Context, round any) context.Context {
	return context.WithValue(ctx, probeRoundCtxKey{}, round)
}

func ProbeRoundFromContext(ctx context.Context) any {
	return ctx.Value(probeRoundCtxKey{})
}

func (p *cancellableProbePool) Do(
	ctx context.Context,
	key string,
	timeout time.Duration,
	run func(workCtx context.Context) (uint16, error),
) (uint16, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	roundVal := ProbeRoundFromContext(ctx)

	p.mu.Lock()
	if roundVal != nil {
		if p.currentRound != roundVal {
			p.currentRound = roundVal
			p.results = make(map[string]uint16)
		} else if cachedVal, ok := p.results[key]; ok {
			p.mu.Unlock()
			return cachedVal, nil
		}
	}

	var task *probeTask
	for {
		t, exists := p.tasks[key]
		if !exists {
			workCtx, cancel := context.WithCancel(context.Background())
			task = &probeTask{
				cancel:  cancel,
				waiters: 1,
				done:    make(chan struct{}),
			}
			p.tasks[key] = task
			p.mu.Unlock()

			go func() {
				defer func() {
					task.cancel()
					p.mu.Lock()
					if p.tasks[key] == task {
						delete(p.tasks, key)
					}
					if task.err == nil && roundVal != nil && p.currentRound == roundVal {
						if p.results == nil {
							p.results = make(map[string]uint16)
						}
						p.results[key] = task.val
					}
					task.retiring = true
					close(task.done)
					p.mu.Unlock()
				}()

				val, err := run(workCtx)
				task.val = val
				task.err = err
			}()
			break
		}

		if t.retiring {
			if p.tasks[key] == t {
				delete(p.tasks, key)
			}
			continue
		}

		t.waiters++
		task = t
		p.mu.Unlock()
		break
	}

	select {
	case <-ctx.Done():
		var toCancel context.CancelFunc
		p.mu.Lock()
		task.waiters--
		if task.waiters == 0 && !task.retiring {
			task.retiring = true
			if p.tasks[key] == task {
				delete(p.tasks, key)
			}
			toCancel = task.cancel
		}
		p.mu.Unlock()

		if toCancel != nil {
			toCancel()
		}
		return 0, ctx.Err()

	case <-task.done:
		p.mu.Lock()
		task.waiters--
		p.mu.Unlock()

		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return task.val, task.err
	}
}

func (gb *GroupBase) URLTest(ctx context.Context, url string, expectedStatus utils.IntRanges[uint16]) (map[string]uint16, error) {
	proxies := gb.GetProxies(false)
	results := make(map[string]uint16, len(proxies))
	if len(proxies) == 0 {
		return results, fmt.Errorf("get delay: all proxies timeout")
	}
	if err := ctx.Err(); err != nil {
		return results, err
	}

	workers := len(proxies)
	if workers > maxConcurrentProxyProbes {
		workers = maxConcurrentProxyProbes
	}
	timeout := time.Duration(gb.testTimeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	expectedStr := ""
	if expectedStatus != nil {
		expectedStr = expectedStatus.String()
	}
	roundVal := ProbeRoundFromContext(ctx)

	jobs := make(chan C.Proxy)
	var wg sync.WaitGroup
	var resultsMu sync.Mutex

	probe := func(proxy C.Proxy) {
		if proxy == nil || ctx.Err() != nil {
			return
		}
		sfKey := fmt.Sprintf("%p|%v|%s|%s|%s|%v", proxy, roundVal, proxy.Name(), url, expectedStr, timeout)
		val, err := globalProbePool.Do(ctx, sfKey, timeout, func(workCtx context.Context) (uint16, error) {
			select {
			case globalProbeSem <- struct{}{}:
				defer func() { <-globalProbeSem }()
				if err := workCtx.Err(); err != nil {
					return 0, err
				}
			case <-workCtx.Done():
				return 0, workCtx.Err()
			}
			dialCtx, dialCancel := context.WithTimeout(workCtx, timeout)
			defer dialCancel()
			return proxy.URLTest(dialCtx, url, expectedStatus)
		})

		if err == nil {
			resultsMu.Lock()
			results[proxy.Name()] = val
			resultsMu.Unlock()
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case proxy, ok := <-jobs:
					if !ok {
						return
					}
					probe(proxy)
				}
			}
		}()
	}

submit:
	for _, proxy := range proxies {
		select {
		case jobs <- proxy:
		case <-ctx.Done():
			break submit
		}
	}
	close(jobs)
	wg.Wait()

	if len(results) == 0 {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		return results, fmt.Errorf("get delay: all proxies timeout")
	}
	return results, nil
}

func isProxyServerHardFailure(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, " connect error: ") {
		return false
	}

	// 1. POSIX syscall errnos
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return true
	}

	// 2. Windows Winsock WSA error codes (e.g. 10061 WSAECONNREFUSED, 10054 WSAECONNRESET)
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch uint32(errno) {
		case 10061, // WSAECONNREFUSED: Connection refused
			10054, // WSAECONNRESET: Connection reset by peer
			10065, // WSAEHOSTUNREACH: No route to host
			10051: // WSAENETUNREACH: Network unreachable
			return true
		}
	}

	// 3. String representations across OSes (including Windows localized English WSA messages)
	lower := strings.ToLower(errMsg)
	if strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "actively refused it") || // Windows WSA 10061: "target machine actively refused it"
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "forcibly closed") || // Windows WSA 10054: "forcibly closed by the remote host"
		strings.Contains(lower, "no route to host") ||
		strings.Contains(lower, "network is unreachable") {
		return true
	}
	return false
}

func (gb *GroupBase) onDialFailed(adapterType C.AdapterType, metadata *C.Metadata, err error, fn func()) {
	if adapterType == C.Direct || adapterType == C.Compatible || adapterType == C.Reject || adapterType == C.Pass || adapterType == C.RejectDrop {
		return
	}

	// Filter out client-side cancellation; dial deadlines/timeouts represent genuine node stalls
	if errors.Is(err, context.Canceled) {
		return
	}

	if errors.Is(err, C.ErrNotSupport) {
		return
	}

	if metadata == nil {
		return
	}

	var host string
	if metadata.Host != "" {
		host = strings.ToLower(strings.TrimRight(metadata.Host, "."))
	} else if metadata.DstIP.IsValid() {
		host = metadata.DstIP.Unmap().String()
	}
	if host == "" {
		return
	}

	gb.failedTestMux.Lock()
	defer gb.failedTestMux.Unlock()

	now := time.Now()
	isHardFail := isProxyServerHardFailure(err)

	n := 0
	existingIdx := -1
	for _, target := range gb.failedTargets {
		if now.Sub(target.at) <= failSlidingWindow {
			if target.host == host {
				existingIdx = n
			}
			gb.failedTargets[n] = target
			n++
		}
	}
	for i := n; i < len(gb.failedTargets); i++ {
		gb.failedTargets[i] = failedTarget{}
	}
	gb.failedTargets = gb.failedTargets[:n]

	if existingIdx >= 0 {
		// Update timestamp and move to end (MRU: Most Recently Used)
		target := gb.failedTargets[existingIdx]
		target.at = now
		copy(gb.failedTargets[existingIdx:], gb.failedTargets[existingIdx+1:])
		gb.failedTargets[len(gb.failedTargets)-1] = target
	} else {
		if len(gb.failedTargets) < maxFailedTargetsCap {
			gb.failedTargets = append(gb.failedTargets, failedTarget{host: host, at: now})
		} else {
			// Find and evict the oldest entry
			oldestIdx := 0
			for i := 1; i < len(gb.failedTargets); i++ {
				if gb.failedTargets[i].at.Before(gb.failedTargets[oldestIdx].at) {
					oldestIdx = i
				}
			}
			copy(gb.failedTargets[oldestIdx:], gb.failedTargets[oldestIdx+1:])
			gb.failedTargets[len(gb.failedTargets)-1] = failedTarget{host: host, at: now}
		}
	}

	shouldTrigger := isHardFail || len(gb.failedTargets) >= gb.maxFailedTimes
	if shouldTrigger {
		cooldown := gb.currentCooldown()
		if time.Since(gb.lastTriggerTime) < cooldown || gb.probing.Load() {
			return
		}
		gb.lastTriggerTime = now
		for i := range gb.failedTargets {
			gb.failedTargets[i] = failedTarget{}
		}
		gb.failedTargets = gb.failedTargets[:0]

		log.Warnln("ProxyGroup: %s triggered fast health check (hardFail=%v, failures=%d, consecutiveAllFailures=%d, cooldown=%v)", gb.Name(), isHardFail, gb.maxFailedTimes, gb.consecutiveAllFailures, cooldown)
		go fn()
	}
}

func (gb *GroupBase) currentCooldown() time.Duration {
	switch gb.consecutiveAllFailures {
	case 0:
		return failoverCooldown
	case 1:
		return 30 * time.Second
	default:
		return 60 * time.Second
	}
}

func (gb *GroupBase) UpdateConsecutiveFailures(allFailed bool) {
	gb.failedTestMux.Lock()
	defer gb.failedTestMux.Unlock()
	if allFailed {
		if gb.consecutiveAllFailures < 5 {
			gb.consecutiveAllFailures++
		}
	} else {
		gb.consecutiveAllFailures = 0
	}
}

func (gb *GroupBase) ResetConsecutiveFailures() {
	gb.failedTestMux.Lock()
	defer gb.failedTestMux.Unlock()
	gb.consecutiveAllFailures = 0
}

func (gb *GroupBase) ConsecutiveFailures() int {
	gb.failedTestMux.Lock()
	defer gb.failedTestMux.Unlock()
	return gb.consecutiveAllFailures
}

func (gb *GroupBase) onDialSuccess() {
	gb.failedTestMux.Lock()
	for i := range gb.failedTargets {
		gb.failedTargets[i] = failedTarget{}
	}
	gb.failedTargets = gb.failedTargets[:0]
	gb.consecutiveAllFailures = 0
	gb.failedTestMux.Unlock()
}

func (gb *GroupBase) healthCheck() {
	if !gb.providerTesting.CompareAndSwap(false, true) {
		return
	}
	defer gb.providerTesting.Store(false)

	wg := sync.WaitGroup{}
	for _, proxyProvider := range gb.providers {
		wg.Add(1)
		proxyProvider := proxyProvider
		go func() {
			defer wg.Done()
			proxyProvider.HealthCheck()
		}()
	}
	wg.Wait()
}
