package outboundgroup

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
	"github.com/metacubex/mihomo/common/utils"

	"github.com/stretchr/testify/require"
)

func TestDialTimeoutCounting(t *testing.T) {
	gb := NewGroupBase(GroupBaseOption{
		Name:           "timeout-test",
		Type:           C.URLTest,
		TestTimeout:    5000,
		MaxFailedTimes: 3,
	})

	var triggered atomic.Int32
	mockHealthCheck := func() {
		triggered.Add(1)
	}

	// 1. Client active cancellation must be ignored
	canceledErr := fmt.Errorf("request aborted: %w", context.Canceled)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "site-cancel.com"}, canceledErr, mockHealthCheck)
	time.Sleep(50 * time.Millisecond)
	if count := triggered.Load(); count != 0 {
		t.Fatalf("expected 0 triggers on context.Canceled, got %d", count)
	}

	// 2. Real dial deadlines / timeouts (context.DeadlineExceeded) MUST be counted
	timeoutErr1 := fmt.Errorf("dial tcp 1.1.1.1:443: i/o timeout: %w", context.DeadlineExceeded)
	timeoutErr2 := fmt.Errorf("dial tcp 2.2.2.2:443: i/o timeout: %w", context.DeadlineExceeded)
	timeoutErr3 := fmt.Errorf("dial tcp 3.3.3.3:443: i/o timeout: %w", context.DeadlineExceeded)

	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "site1.com"}, timeoutErr1, mockHealthCheck)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "site2.com"}, timeoutErr2, mockHealthCheck)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "site3.com"}, timeoutErr3, mockHealthCheck)

	time.Sleep(50 * time.Millisecond)
	if count := triggered.Load(); count != 1 {
		t.Fatalf("expected 1 trigger after 3 real dial timeouts, got %d", count)
	}
}

func TestWindowsWSASocketErrorsRecognized(t *testing.T) {
	// 1. Winsock 10061 (WSAECONNREFUSED)
	wsaRefusedErr := fmt.Errorf("192.0.2.1:443 connect error: dial tcp: %w", syscall.Errno(10061))
	if !isProxyServerHardFailure(wsaRefusedErr) {
		t.Fatal("expected WSA 10061 to be recognized as hard failure")
	}

	// 2. Winsock 10054 (WSAECONNRESET)
	wsaResetErr := fmt.Errorf("192.0.2.1:443 connect error: dial tcp: %w", syscall.Errno(10054))
	if !isProxyServerHardFailure(wsaResetErr) {
		t.Fatal("expected WSA 10054 to be recognized as hard failure")
	}

	// 3. Windows English localized WSA string
	winRefusedStrErr := fmt.Errorf("192.0.2.1:443 connect error: dial tcp: No connection could be made because the target machine actively refused it.")
	if !isProxyServerHardFailure(winRefusedStrErr) {
		t.Fatal("expected Windows 'actively refused it' string to be recognized as hard failure")
	}

	winResetStrErr := fmt.Errorf("192.0.2.1:443 connect error: An existing connection was forcibly closed by the remote host.")
	if !isProxyServerHardFailure(winResetStrErr) {
		t.Fatal("expected Windows 'forcibly closed' string to be recognized as hard failure")
	}

	// 4. Target website rejection (NOT proxy server) -> MUST NOT be recognized as proxy hard failure
	targetErr := fmt.Errorf("dial tcp 93.184.216.34:80: connectex: No connection could be made because the target machine actively refused it.")
	if isProxyServerHardFailure(targetErr) {
		t.Fatal("target site error without ' connect error: ' must NOT be recognized as proxy server hard failure")
	}
}

func TestLRUEvictionPreservesRecentlyRefreshedHost(t *testing.T) {
	gb := NewGroupBase(GroupBaseOption{
		Name:           "lru-test",
		Type:           C.URLTest,
		TestTimeout:    5000,
		MaxFailedTimes: 3,
	})

	var triggered atomic.Int32
	mockHealthCheck := func() {
		triggered.Add(1)
	}

	testErr := errors.New("timeout")

	// Trigger initial probe to enter cooldown
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "init1.com"}, testErr, mockHealthCheck)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "init2.com"}, testErr, mockHealthCheck)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "init3.com"}, testErr, mockHealthCheck)

	time.Sleep(50 * time.Millisecond)
	if count := triggered.Load(); count != 1 {
		t.Fatalf("expected 1 initial trigger, got %d", count)
	}

	// 1. Fill list with 16 hosts: A to P
	for i := 0; i < 16; i++ {
		hostName := fmt.Sprintf("host-%c.com", 'A'+i)
		gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: hostName}, testErr, mockHealthCheck)
	}

	// Artificially simulate that host-A to host-P failed 4.5 seconds ago
	gb.failedTestMux.Lock()
	for i := range gb.failedTargets {
		gb.failedTargets[i].at = time.Now().Add(-4500 * time.Millisecond)
	}
	gb.failedTestMux.Unlock()

	// 2. host-A fails again NOW (refreshed timestamp, moved to MRU end)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "host-A.com"}, testErr, mockHealthCheck)

	// 3. New host Q fails (capacity full; oldest should be evicted, host-A MUST be preserved!)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "host-Q.com"}, testErr, mockHealthCheck)

	// Check that host-A is still present
	gb.failedTestMux.Lock()
	foundA := false
	for _, target := range gb.failedTargets {
		if target.host == "host-a.com" {
			foundA = true
			break
		}
	}
	gb.failedTestMux.Unlock()
	if !foundA {
		t.Fatal("expected host-A to be preserved after LRU eviction of oldest entries")
	}

	// 4. Age the other entries so host-B to host-P expire (>5s), but host-A and host-Q are retained
	gb.failedTestMux.Lock()
	// Set lastTriggerTime to 11s ago so cooldown has elapsed
	gb.lastTriggerTime = time.Now().Add(-11 * time.Second)
	// host-B through host-P are older than 5s
	for i := range gb.failedTargets {
		if gb.failedTargets[i].host != "host-a.com" && gb.failedTargets[i].host != "host-q.com" {
			gb.failedTargets[i].at = time.Now().Add(-6 * time.Second)
		}
	}
	gb.failedTestMux.Unlock()

	// 5. Host R fails -> now valid window has host-A, host-Q, host-R (3 targets) -> MUST trigger!
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "host-R.com"}, testErr, mockHealthCheck)

	time.Sleep(50 * time.Millisecond)
	if count := triggered.Load(); count != 2 {
		t.Fatalf("expected re-trigger after LRU-preserved host-A, host-Q, and host-R, got %d", count)
	}
}

type blockingMockProvider struct {
	P.ProxyProvider
	called atomic.Int32
	enter  chan struct{}
	block  chan struct{}
}

func (b *blockingMockProvider) HealthCheck() {
	b.called.Add(1)
	select {
	case b.enter <- struct{}{}:
	default:
	}
	<-b.block
}

func (b *blockingMockProvider) Touch() {}
func (b *blockingMockProvider) HealthCheckExpectedStatus() utils.IntRanges[uint16] { return nil }
func (b *blockingMockProvider) HealthCheckTimeout() time.Duration { return 5 * time.Second }

func TestProviderTestingMutualExclusionWithBlockingProvider(t *testing.T) {
	bp := &blockingMockProvider{
		enter: make(chan struct{}, 1),
		block: make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-bp.block:
		default:
			close(bp.block)
		}
	})

	gb := NewGroupBase(GroupBaseOption{
		Name:      "blocking-provider-test",
		Type:      C.LoadBalance,
		Providers: []P.ProxyProvider{bp},
	})

	var wg sync.WaitGroup
	// Launch the first call that will block in HealthCheck
	wg.Add(1)
	go func() {
		defer wg.Done()
		gb.healthCheck()
	}()

	// Wait until the first call enters HealthCheck
	select {
	case <-bp.enter:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first healthCheck to enter")
	}

	// Launch 2 concurrent calls in separate goroutines that should be immediately rejected by CAS
	rejectedWg := sync.WaitGroup{}
	for i := 0; i < 2; i++ {
		rejectedWg.Add(1)
		go func() {
			defer rejectedWg.Done()
			gb.healthCheck()
		}()
	}

	// Deterministically wait for the two rejected calls to complete
	rejectedDone := make(chan struct{})
	go func() {
		rejectedWg.Wait()
		close(rejectedDone)
	}()

	select {
	case <-rejectedDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for concurrent calls to be rejected")
	}

	// Only 1 should have been able to call bp.HealthCheck()
	if count := bp.called.Load(); count != 1 {
		t.Fatalf("expected exactly 1 Provider.HealthCheck call during concurrency, got %d", count)
	}

	// Unblock the first call
	close(bp.block)
	wg.Wait()

	// After release, a new call should succeed
	bp.block = make(chan struct{})
	bp.enter = make(chan struct{}, 1)
	go func() {
		gb.healthCheck()
	}()

	select {
	case <-bp.enter:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second healthCheck after release")
	}

	if count := bp.called.Load(); count != 2 {
		t.Fatalf("expected second Provider.HealthCheck call after release, got %d", count)
	}
	close(bp.block)
}

func TestGroupBasePassiveFailureDetectionHostDeduplication(t *testing.T) {
	gb := NewGroupBase(GroupBaseOption{
		Name:           "auto-test",
		Type:           C.URLTest,
		TestTimeout:    5000,
		MaxFailedTimes: 3,
	})

	var triggered atomic.Int32
	mockHealthCheck := func() {
		triggered.Add(1)
	}

	testErr := errors.New("i/o timeout")

	// 1. Same host fails 5 times with case and trailing dot variants -> only 1 distinct host recorded, NOT trigger
	hosts := []string{"dead-site.com", "DEAD-SITE.COM", "dead-site.com.", "Dead-Site.Com", "dead-site.com"}
	for _, h := range hosts {
		meta := &C.Metadata{Host: h}
		gb.onDialFailed(C.Shadowsocks, meta, testErr, mockHealthCheck)
	}

	time.Sleep(50 * time.Millisecond)
	if count := triggered.Load(); count != 0 {
		t.Fatalf("expected 0 triggers for same host variants, got %d", count)
	}

	// 2. IP-based destination failure -> count = 2, still NOT trigger
	ipMeta := &C.Metadata{DstIP: netip.MustParseAddr("1.2.3.4")}
	gb.onDialFailed(C.Shadowsocks, ipMeta, testErr, mockHealthCheck)
	time.Sleep(50 * time.Millisecond)
	if count := triggered.Load(); count != 0 {
		t.Fatalf("expected 0 triggers after 2 distinct destinations, got %d", count)
	}

	// 3. Third distinct destination fails -> count = 3 >= maxFailedTimes -> MUST trigger!
	meta3 := &C.Metadata{Host: "third-dead-site.com"}
	gb.onDialFailed(C.Shadowsocks, meta3, testErr, mockHealthCheck)
	time.Sleep(100 * time.Millisecond)
	if count := triggered.Load(); count != 1 {
		t.Fatalf("expected 1 trigger after 3 distinct hosts, got %d", count)
	}

	// 4. Within 10s cooldown, another failure should NOT trigger again
	meta4 := &C.Metadata{Host: "fourth-dead-site.com"}
	gb.onDialFailed(C.Shadowsocks, meta4, testErr, mockHealthCheck)
	time.Sleep(50 * time.Millisecond)
	if count := triggered.Load(); count != 1 {
		t.Fatalf("expected cooldown to prevent second trigger, got %d", count)
	}
}

func TestGroupBaseSuccessResetsFailures(t *testing.T) {
	gb := NewGroupBase(GroupBaseOption{
		Name:           "success-reset-test",
		Type:           C.URLTest,
		TestTimeout:    5000,
		MaxFailedTimes: 3,
	})

	var triggered atomic.Int32
	mockHealthCheck := func() {
		triggered.Add(1)
	}

	testErr := errors.New("timeout")

	// 2 failures
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "site-a.com"}, testErr, mockHealthCheck)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "site-b.com"}, testErr, mockHealthCheck)

	// Successful connection occurs
	gb.onDialSuccess()

	// 1 more failure should NOT trigger (since count was reset to 0)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "site-c.com"}, testErr, mockHealthCheck)
	time.Sleep(50 * time.Millisecond)
	if count := triggered.Load(); count != 0 {
		t.Fatalf("expected 0 triggers after success reset, got %d", count)
	}
}

func TestGroupBaseProbingMutualExclusion(t *testing.T) {
	gb := NewGroupBase(GroupBaseOption{
		Name:           "probing-test",
		Type:           C.URLTest,
		TestTimeout:    5000,
		MaxFailedTimes: 3,
	})

	var triggered atomic.Int32
	mockHealthCheck := func() {
		triggered.Add(1)
	}

	// Simulate that a probe is currently in flight
	gb.probing.Store(true)

	testErr := errors.New("timeout")
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "s1.com"}, testErr, mockHealthCheck)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "s2.com"}, testErr, mockHealthCheck)
	gb.onDialFailed(C.Shadowsocks, &C.Metadata{Host: "s3.com"}, testErr, mockHealthCheck)

	time.Sleep(50 * time.Millisecond)
	if count := triggered.Load(); count != 0 {
		t.Fatalf("expected 0 triggers while probing is active, got %d", count)
	}
}

type canceledMockAdapter struct {
	*outbound.Base
}

func (c *canceledMockAdapter) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	// Returns a stringified canceled error without %w, simulating legacy VMess %s format
	return nil, fmt.Errorf("proxy connect error: %s", context.Canceled.Error())
}

func (c *canceledMockAdapter) ListenPacketContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error) {
	return nil, fmt.Errorf("proxy connect error: %s", context.Canceled.Error())
}

func (c *canceledMockAdapter) SupportUDP() bool { return false }
func (c *canceledMockAdapter) MarshalJSON() ([]byte, error) { return nil, nil }
func (c *canceledMockAdapter) Unwrap(metadata *C.Metadata, touch bool) C.Proxy { return nil }
func (c *canceledMockAdapter) Close() error { return nil }

func TestProxyURLTestPreservesHealthStateOnStringifiedCancellation(t *testing.T) {
	// Verify that when context is canceled, even if adapter severing error chain with %s,
	// Proxy.URLTest does NOT mark the proxy as dead (alive=false) and does NOT record failed delay history.
	mockAdapter := &canceledMockAdapter{
		Base: outbound.NewBase(outbound.BaseOption{Name: "canceled-mock", Type: C.Vmess}),
	}
	proxy := adapter.NewProxy(mockAdapter)
	testURL := "http://cp.cloudflare.com/generate_204"

	// Pre-condition: proxy is alive and has no history
	if !proxy.AliveForTestUrl(testURL) {
		t.Fatal("expected initial proxy to be alive")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Actively cancel

	_, err := proxy.URLTest(ctx, testURL, nil)
	if err == nil {
		t.Fatal("expected URLTest to return error on canceled context")
	}

	// Verify: alive state remains true, and no delay record was stored
	if !proxy.AliveForTestUrl(testURL) {
		t.Errorf("expected proxy alive state to be preserved as true, got false")
	}
	if history := proxy.DelayHistory(); len(history) != 0 {
		t.Errorf("expected 0 history records recorded on cancellation, got %d", len(history))
	}
}


type concurrencyMockAdapter struct {
	*outbound.Base
	active *atomic.Int32
	peak   *atomic.Int32
}

func (c *concurrencyMockAdapter) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	curr := c.active.Add(1)
	for {
		oldPeak := c.peak.Load()
		if curr <= oldPeak || c.peak.CompareAndSwap(oldPeak, curr) {
			break
		}
	}
	defer c.active.Add(-1)
	time.Sleep(20 * time.Millisecond)
	return nil, errors.New("mock dial error")
}

func (c *concurrencyMockAdapter) ListenPacketContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func (c *concurrencyMockAdapter) SupportUDP() bool { return false }
func (c *concurrencyMockAdapter) MarshalJSON() ([]byte, error) { return nil, nil }
func (c *concurrencyMockAdapter) Unwrap(metadata *C.Metadata, touch bool) C.Proxy { return nil }
func (c *concurrencyMockAdapter) Close() error { return nil }

type concurrencyMockProvider struct {
	P.ProxyProvider
	proxies []C.Proxy
}

func (p *concurrencyMockProvider) Proxies() []C.Proxy {
	return p.proxies
}

func (p *concurrencyMockProvider) Touch() {}
func (p *concurrencyMockProvider) Version() uint32 { return 1 }
func (p *concurrencyMockProvider) HealthCheck() {}
func (p *concurrencyMockProvider) HealthCheckExpectedStatus() utils.IntRanges[uint16] { return nil }
func (p *concurrencyMockProvider) HealthCheckTimeout() time.Duration { return 5 * time.Second }

func TestGroupBaseURLTestConcurrencyBudget(t *testing.T) {
	var active atomic.Int32
	var peak atomic.Int32

	const totalProxies = 60
	proxies := make([]C.Proxy, totalProxies)
	for i := 0; i < totalProxies; i++ {
		name := fmt.Sprintf("concurrency-node-%02d", i)
		mock := &concurrencyMockAdapter{
			Base:   outbound.NewBase(outbound.BaseOption{Name: name, Type: C.Vmess}),
			active: &active,
			peak:   &peak,
		}
		proxies[i] = adapter.NewProxy(mock)
	}

	gb := NewGroupBase(GroupBaseOption{
		Name:      "concurrency-test",
		Type:      C.URLTest,
		Providers: []P.ProxyProvider{&concurrencyMockProvider{proxies: proxies}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = gb.URLTest(ctx, "http://test.com", nil)

	maxPeak := peak.Load()
	if maxPeak > maxConcurrentProxyProbes {
		t.Fatalf("expected peak concurrent probes <= %d, got %d", maxConcurrentProxyProbes, maxPeak)
	}
	t.Logf("Verified concurrency budget: total=%d, peakConcurrent=%d (budget=%d)", totalProxies, maxPeak, maxConcurrentProxyProbes)
}

type controllableMockAdapter struct {
	C.Proxy
	name      string
	delay     uint16
	probeHook func(ctx context.Context)
	callCount atomic.Int32
}

func (c *controllableMockAdapter) Name() string { return c.name }
func (c *controllableMockAdapter) Type() C.AdapterType { return C.Vmess }

func (c *controllableMockAdapter) URLTest(ctx context.Context, url string, expectedStatus utils.IntRanges[uint16]) (uint16, error) {
	c.callCount.Add(1)
	if c.probeHook != nil {
		c.probeHook(ctx)
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return c.delay, nil
}

func TestGroupBaseURLTestInstanceIsolation(t *testing.T) {
	p1Block := make(chan struct{})
	p1Started := make(chan struct{})

	mock1 := &controllableMockAdapter{
		name:  "same-node-name",
		delay: 111,
		probeHook: func(ctx context.Context) {
			close(p1Started)
			<-p1Block
		},
	}

	mock2 := &controllableMockAdapter{
		name:  "same-node-name",
		delay: 222,
	}

	gb1 := NewGroupBase(GroupBaseOption{
		Name:      "gb1",
		Type:      C.URLTest,
		Providers: []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{mock1}}},
	})
	gb2 := NewGroupBase(GroupBaseOption{
		Name:      "gb2",
		Type:      C.URLTest,
		Providers: []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{mock2}}},
	})

	var res1 map[string]uint16
	var err1 error
	done1 := make(chan struct{})
	go func() {
		res1, err1 = gb1.URLTest(context.Background(), "http://test.com", nil)
		close(done1)
	}()

	<-p1Started

	// Now probe gb2 with mock2 (same name, different instance)
	res2, err2 := gb2.URLTest(context.Background(), "http://test.com", nil)
	require.NoError(t, err2)
	require.Equal(t, uint16(222), res2["same-node-name"], "gb2 must probe mock2 independently and get 222")
	require.Equal(t, int32(1), mock2.callCount.Load(), "mock2 must have been tested")

	// Unblock mock1 and finish gb1
	close(p1Block)
	<-done1
	require.NoError(t, err1)
	require.Equal(t, uint16(111), res1["same-node-name"], "gb1 must get 111 from mock1")
	require.Equal(t, int32(1), mock1.callCount.Load(), "mock1 must have been tested")
}

func TestGroupBaseURLTestCallerCancelDoesNotBlock(t *testing.T) {
	slowGate := make(chan struct{})
	slowStarted := make(chan struct{})

	slowMock := &controllableMockAdapter{
		name:  "slow-node",
		delay: 99,
		probeHook: func(ctx context.Context) {
			select {
			case <-slowStarted:
			default:
				close(slowStarted)
			}
			select {
			case <-slowGate:
			case <-ctx.Done():
			}
		},
	}

	gb := NewGroupBase(GroupBaseOption{
		Name:      "caller-cancel-test",
		Type:      C.URLTest,
		Providers: []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{slowMock}}},
	})

	// Caller 1 starts long-running probe
	done1 := make(chan struct{})
	go func() {
		ctx1, cancel1 := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel1()
		_, _ = gb.URLTest(ctx1, "http://test.com", nil)
		close(done1)
	}()

	<-slowStarted

	// Caller 2 joins with short deadline (80ms)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel2()

	start := time.Now()
	_, err2 := gb.URLTest(ctx2, "http://test.com", nil)
	elapsed := time.Since(start)

	// Caller 2 must return promptly when ctx2 expires (under 300ms), not waiting for slowGate
	require.Error(t, err2, "caller 2 should report error when its context expires")
	require.Less(t, elapsed, 300*time.Millisecond, "caller 2 must not be blocked by slow probe")

	// Release slow probe
	close(slowGate)
	<-done1
}


func TestGroupBaseURLTestAllCallersCancelAbortsWorkAndAllowsNextRound(t *testing.T) {
	slowGate := make(chan struct{})
	probeStarted := make(chan struct{})
	var workCtxErr atomic.Pointer[error]

	mock := &controllableMockAdapter{
		name:  "reprobe-node",
		delay: 111,
		probeHook: func(ctx context.Context) {
			select {
			case <-probeStarted:
			default:
				close(probeStarted)
			}
			select {
			case <-slowGate:
			case <-ctx.Done():
				err := ctx.Err()
				workCtxErr.Store(&err)
			}
		},
	}

	gb := NewGroupBase(GroupBaseOption{
		Name:      "reprobe-gb",
		Type:      C.URLTest,
		Providers: []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{mock}}},
	})

	// Round 1: single caller with 60ms timeout
	ctx1, cancel1 := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel1()

	_, err1 := gb.URLTest(ctx1, "http://test.com", nil)
	require.Error(t, err1, "caller 1 should fail with timeout")

	// Wait up to 100ms for underlying workCtx to be canceled after caller abandoned
	time.Sleep(50 * time.Millisecond)
	pErr := workCtxErr.Load()
	require.NotNil(t, pErr, "underlying workCtx must have been canceled when caller abandoned")
	require.Equal(t, context.Canceled, *pErr)

	// Round 2: caller on SAME proxy instance with new delay
	mock.delay = 222
	mock.probeHook = nil // no more blocking

	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()

	res2, err2 := gb.URLTest(ctx2, "http://test.com", nil)
	require.NoError(t, err2)
	require.Equal(t, uint16(222), res2["reprobe-node"], "round 2 must perform fresh probe and return 222")
	require.Equal(t, int32(2), mock.callCount.Load(), "total probe count must be 2")
}

func TestGroupBaseURLTestQueuedNodesDoNotExecuteWhenCancelled(t *testing.T) {
	// Occupy all 10 semaphore slots
	occupySlots := make(chan struct{})
	for i := 0; i < maxConcurrentProxyProbes; i++ {
		globalProbeSem <- struct{}{}
	}
	defer func() {
		// Drain any remaining slots
		for len(globalProbeSem) > 0 {
			<-globalProbeSem
		}
	}()

	var node11Called atomic.Int32
	queuedMock := &controllableMockAdapter{
		name:  "queued-node-11",
		delay: 50,
		probeHook: func(ctx context.Context) {
			node11Called.Add(1)
		},
	}

	gb := NewGroupBase(GroupBaseOption{
		Name:      "queued-gb",
		Type:      C.URLTest,
		Providers: []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{queuedMock}}},
	})

	// Caller for node 11 with short 50ms timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := gb.URLTest(ctx, "http://test.com", nil)
	require.Error(t, err, "caller should fail with timeout while queued")

	// Now release the 10 slots
	for i := 0; i < maxConcurrentProxyProbes; i++ {
		<-globalProbeSem
	}

	// Give a moment to confirm node 11 was NOT executed
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(0), node11Called.Load(), "queued node must not execute after caller cancelled")
	_ = occupySlots
}


func TestCancellableProbePoolJoinRetiringRaceStress(t *testing.T) {
	pool := &cancellableProbePool{tasks: make(map[string]*probeTask)}
	const iterations = 1000

	for i := 0; i < iterations; i++ {
		key := fmt.Sprintf("stress-probe-%d", i)
		ctx1, cancel1 := context.WithCancel(context.Background())

		started := make(chan struct{})
		gate := make(chan struct{})

		go func() {
			_, _ = pool.Do(ctx1, key, 5*time.Second, func(workCtx context.Context) (uint16, error) {
				close(started)
				select {
				case <-workCtx.Done():
					return 0, workCtx.Err()
				case <-gate:
					return 100, nil
				}
			})
		}()

		<-started

		var wg sync.WaitGroup
		wg.Add(2)

		var res2 uint16
		var err2 error

		go func() {
			defer wg.Done()
			cancel1()
		}()

		go func() {
			defer wg.Done()
			res2, err2 = pool.Do(context.Background(), key, 5*time.Second, func(workCtx context.Context) (uint16, error) {
				select {
				case <-workCtx.Done():
					return 0, workCtx.Err()
				case <-gate:
					return 200, nil
				}
			})
		}()

		time.Sleep(100 * time.Microsecond)
		close(gate)
		wg.Wait()

		if errors.Is(err2, context.Canceled) {
			t.Fatalf("iteration %d: caller 2 with Background context was mis-canceled with context.Canceled! (res=%d)", i, res2)
		}
		if err2 != nil {
			t.Fatalf("iteration %d: caller 2 failed with unexpected error: %v", i, err2)
		}
		if res2 != 100 && res2 != 200 {
			t.Fatalf("iteration %d: unexpected result: %d", i, res2)
		}
	}
}

func TestGroupBaseURLTestJoinRetiringInterleaved(t *testing.T) {
	var caller2MisCanceled atomic.Int32
	const runs = 200

	for i := 0; i < runs; i++ {
		gate := make(chan struct{})
		started := make(chan struct{})

		mock := &controllableMockAdapter{
			name:  "race-node",
			delay: 88,
			probeHook: func(ctx context.Context) {
				select {
				case <-started:
				default:
					close(started)
				}
				select {
				case <-ctx.Done():
				case <-gate:
				}
			},
		}

		gb := NewGroupBase(GroupBaseOption{
			Name:      "race-gb",
			Type:      C.URLTest,
			Providers: []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{mock}}},
		})

		ctx1, cancel1 := context.WithCancel(context.Background())
		go func() {
			_, _ = gb.URLTest(ctx1, "http://test.com", nil)
		}()

		<-started

		var wg sync.WaitGroup
		wg.Add(2)

		var res2 map[string]uint16
		var err2 error

		go func() {
			defer wg.Done()
			cancel1()
		}()

		go func() {
			defer wg.Done()
			res2, err2 = gb.URLTest(context.Background(), "http://test.com", nil)
			if err2 != nil {
				caller2MisCanceled.Add(1)
			}
		}()

		time.Sleep(200 * time.Microsecond)
		close(gate)
		wg.Wait()

		_ = res2
	}

	require.Equal(t, int32(0), caller2MisCanceled.Load(), "caller 2 with Background must never be canceled across %d runs", runs)
}

func TestReview2QueuedHealthyNodesGetTheirOwnTimeout(t *testing.T) {
	proxies := make([]C.Proxy, 20)
	for i := range proxies {
		proxies[i] = &controllableMockAdapter{
			name: fmt.Sprintf("healthy-%d", i), delay: 200,
			probeHook: func(ctx context.Context) {
				select {
				case <-time.After(200 * time.Millisecond):
				case <-ctx.Done():
				}
			},
		}
	}
	gb := NewGroupBase(GroupBaseOption{
		Name: "queued", Type: C.URLTest, TestTimeout: 300,
		Providers: []P.ProxyProvider{&concurrencyMockProvider{proxies: proxies}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	results, err := gb.URLTest(ctx, "http://example.test", nil)
	if len(results) != len(proxies) {
		t.Fatalf("20 healthy nodes each need 200ms (<300ms timeout); only %d passed: %v", len(results), err)
	}
}

func TestReview2LongTimeoutDoesNotInheritShortProbe(t *testing.T) {
	started := make(chan struct{}, 1)
	proxy := &controllableMockAdapter{
		name: "shared", delay: 200,
		probeHook: func(ctx context.Context) {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-time.After(200 * time.Millisecond):
			case <-ctx.Done():
			}
		},
	}
	provider := &concurrencyMockProvider{proxies: []C.Proxy{proxy}}
	short := NewGroupBase(GroupBaseOption{Name: "short", Type: C.URLTest, TestTimeout: 100, Providers: []P.ProxyProvider{provider}})
	long := NewGroupBase(GroupBaseOption{Name: "long", Type: C.URLTest, TestTimeout: 1000, Providers: []P.ProxyProvider{provider}})
	done := make(chan struct{})
	go func() {
		_, _ = short.URLTest(context.Background(), "http://example.test", nil)
		close(done)
	}()
	<-started
	results, err := long.URLTest(context.Background(), "http://example.test", nil)
	<-done
	if len(results) != 1 {
		t.Fatalf("200ms healthy proxy failed under 1000ms policy; actual probes=%d, error=%v", proxy.callCount.Load(), err)
	}
}

type review3HealthyProxy struct {
	C.Proxy
	name       string
	successful *atomic.Int32
}

func (p *review3HealthyProxy) Name() string                     { return p.name }
func (p *review3HealthyProxy) Type() C.AdapterType              { return C.Vmess }
func (p *review3HealthyProxy) AliveForTestUrl(string) bool      { return true }
func (p *review3HealthyProxy) LastDelayForTestUrl(string) uint16 { return 200 }
func (p *review3HealthyProxy) URLTest(ctx context.Context, url string, status utils.IntRanges[uint16]) (uint16, error) {
	select {
	case <-time.After(200 * time.Millisecond):
		p.successful.Add(1)
		return 200, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func TestReview3PassiveHealthCheckQueueBudget(t *testing.T) {
	for _, groupType := range []string{"fallback", "url-test"} {
		t.Run(groupType, func(t *testing.T) {
			var successful atomic.Int32
			proxies := make([]C.Proxy, 20)
			for i := range proxies {
				proxies[i] = &review3HealthyProxy{name: fmt.Sprintf("healthy-%d", i), successful: &successful}
			}
			providers := []P.ProxyProvider{&concurrencyMockProvider{proxies: proxies}}
			options := GroupCommonOption{Name: "review3", TestTimeout: 300, URL: "http://example.test", MaxFailedTimes: 3}
			if groupType == "fallback" {
				group, err := NewFallback(options, FallbackOption{}, namedProxy("DIRECT"), providers)
				if err != nil {
					t.Fatal(err)
				}
				group.healthCheck()
			} else {
				group, err := NewURLTest(options, URLTestOption{}, namedProxy("DIRECT"), providers)
				if err != nil {
					t.Fatal(err)
				}
				group.healthCheck()
			}
			if got := successful.Load(); got != 20 {
				t.Fatalf("real %s healthCheck: only %d/20 healthy nodes completed; each takes 200ms under 300ms node timeout", groupType, got)
			}
		})
	}
}

func TestCancellableProbePoolSameRoundResultReuse(t *testing.T) {
	pool := &cancellableProbePool{
		tasks:   make(map[string]*probeTask),
		results: make(map[string]uint16),
	}

	round1 := "round-1"
	round2 := "round-2"
	key := "node-key"

	var callCount atomic.Int32
	runner := func(workCtx context.Context) (uint16, error) {
		callCount.Add(1)
		return 120, nil
	}

	ctx1 := WithProbeRound(context.Background(), round1)

	// 1. First probe in round 1 -> executes runner
	val1, err1 := pool.Do(ctx1, key, 1*time.Second, runner)
	require.NoError(t, err1)
	require.Equal(t, uint16(120), val1)
	require.Equal(t, int32(1), callCount.Load())

	// 2. Second probe in round 1 with same key -> reuses cached result without calling runner
	val2, err2 := pool.Do(ctx1, key, 1*time.Second, runner)
	require.NoError(t, err2)
	require.Equal(t, uint16(120), val2)
	require.Equal(t, int32(1), callCount.Load(), "runner must not be invoked again in same round")

	// 3. Third probe in round 2 -> new round invalidates old cache, runner must be invoked
	ctx2 := WithProbeRound(context.Background(), round2)
	val3, err3 := pool.Do(ctx2, key, 1*time.Second, runner)
	require.NoError(t, err3)
	require.Equal(t, uint16(120), val3)
	require.Equal(t, int32(2), callCount.Load(), "runner must be invoked for new round")

	// 4. Failed probe is not cached
	failKey := "fail-node"
	var failCount atomic.Int32
	failRunner := func(workCtx context.Context) (uint16, error) {
		failCount.Add(1)
		return 0, errors.New("dial error")
	}

	_, fErr1 := pool.Do(ctx2, failKey, 1*time.Second, failRunner)
	require.Error(t, fErr1)
	require.Equal(t, int32(1), failCount.Load())

	// Calling again should re-invoke failRunner because error was not cached
	_, fErr2 := pool.Do(ctx2, failKey, 1*time.Second, failRunner)
	require.Error(t, fErr2)
	require.Equal(t, int32(2), failCount.Load(), "failed probe must not be cached")

	// 5. CleanupRound releases round memory
	pool.CleanupRound(round2)
	val4, err4 := pool.Do(ctx2, key, 1*time.Second, runner)
	require.NoError(t, err4)
	require.Equal(t, uint16(120), val4)
	require.Equal(t, int32(3), callCount.Load(), "runner must be called again after round cleanup")
}

func TestPassiveConsecutiveAllFailuresBackoff(t *testing.T) {
	gb := NewGroupBase(GroupBaseOption{
		Name:           "pwr07-test",
		Type:           C.URLTest,
		TestTimeout:    5000,
		MaxFailedTimes: 3,
	})

	// 1. Initial state: 0 consecutive failures, 10s cooldown
	require.Equal(t, 0, gb.ConsecutiveFailures())
	require.Equal(t, failoverCooldown, gb.currentCooldown())

	// 2. 1 consecutive all-failure -> 30s cooldown
	gb.UpdateConsecutiveFailures(true)
	require.Equal(t, 1, gb.ConsecutiveFailures())
	require.Equal(t, 30*time.Second, gb.currentCooldown())

	// 3. 2 consecutive all-failures -> 60s cooldown
	gb.UpdateConsecutiveFailures(true)
	require.Equal(t, 2, gb.ConsecutiveFailures())
	require.Equal(t, 60*time.Second, gb.currentCooldown())

	// 4. 3+ consecutive all-failures -> capped at 60s cooldown
	gb.UpdateConsecutiveFailures(true)
	require.Equal(t, 3, gb.ConsecutiveFailures())
	require.Equal(t, 60*time.Second, gb.currentCooldown())

	// 5. Candidate succeeded -> resets to 0
	gb.UpdateConsecutiveFailures(false)
	require.Equal(t, 0, gb.ConsecutiveFailures())
	require.Equal(t, 10*time.Second, gb.currentCooldown())

	// 6. ResetConsecutiveFailures (network reset) -> resets to 0
	gb.UpdateConsecutiveFailures(true)
	gb.UpdateConsecutiveFailures(true)
	require.Equal(t, 2, gb.ConsecutiveFailures())
	gb.ResetConsecutiveFailures()
	require.Equal(t, 0, gb.ConsecutiveFailures())
	require.Equal(t, 10*time.Second, gb.currentCooldown())

	// 7. onDialSuccess -> resets to 0
	gb.UpdateConsecutiveFailures(true)
	require.Equal(t, 1, gb.ConsecutiveFailures())
	gb.onDialSuccess()
	require.Equal(t, 0, gb.ConsecutiveFailures())
	require.Equal(t, 10*time.Second, gb.currentCooldown())
}

func TestRoundBudgetBounds(t *testing.T) {
	// 1 node with default (0) timeout: 1 batch * 5s + 500ms = 5500ms
	b1 := RoundBudget(1, 0)
	require.Equal(t, 5500*time.Millisecond, b1)

	// 20 nodes with default timeout: 2 batches * 5s + 500ms = 10500ms
	b20 := RoundBudget(20, 0)
	require.Equal(t, 10500*time.Millisecond, b20)

	// 100 nodes with default timeout: 10 batches * 5s + 500ms = 50.5s -> bounded at 30s
	b100 := RoundBudget(100, 0)
	require.Equal(t, 30*time.Second, b100)

	// Custom timeout 8s: 1 batch * 8s + 500ms = 8500ms
	bCustom := RoundBudget(1, 8*time.Second)
	require.Equal(t, 8500*time.Millisecond, bCustom)

	// Custom timeout 300ms: 1 batch * 300ms + 500ms = 800ms
	bFast := RoundBudget(1, 300*time.Millisecond)
	require.Equal(t, 800*time.Millisecond, bFast)
}

type r2MockProxy struct {
	C.Proxy
	name  string
	delay time.Duration
	err   error
	alive bool
}

func (p *r2MockProxy) Name() string                     { return p.name }
func (p *r2MockProxy) Type() C.AdapterType              { return C.Vmess }
func (p *r2MockProxy) AliveForTestUrl(string) bool      { return p.alive }
func (p *r2MockProxy) LastDelayForTestUrl(string) uint16 { return 100 }
func (p *r2MockProxy) URLTest(ctx context.Context, url string, status utils.IntRanges[uint16]) (uint16, error) {
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if p.err != nil {
		return 0, p.err
	}
	p.alive = true
	return 100, nil
}

func TestHealthCheckContextDeadlineWithPartialSuccess(t *testing.T) {
	var switched atomic.Bool
	oldCallback := OnGroupNowChanged
	OnGroupNowChanged = func(g C.ProxyAdapter, groupName, before, after string) {
		switched.Store(true)
	}
	t.Cleanup(func() { OnGroupNowChanged = oldCallback })

	nodeSlow := &r2MockProxy{name: "node-slow", delay: 2 * time.Second}
	nodeFast := &r2MockProxy{name: "node-fast", delay: 20 * time.Millisecond}
	providers := []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{nodeSlow, nodeFast}}}

	options := GroupCommonOption{Name: "partial-group", TestTimeout: 300, URL: "http://example.test"}
	group, err := NewFallback(options, FallbackOption{}, namedProxy("DIRECT"), providers)
	require.NoError(t, err)

	// 100ms deadline: nodeA (20ms) succeeds, nodeB (2s) expires
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	results, err := group.HealthCheckContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded, "deadline exceeded must return DeadlineExceeded error")
	require.Equal(t, 1, len(results))
	require.Contains(t, results, "node-fast")
	require.True(t, switched.Load(), "valid switch must be triggered for verified alive candidate")
	require.Equal(t, 0, group.ConsecutiveFailures(), "partial success must not increment consecutive failures")
}

func TestHealthCheckContextCanceledNoSwitch(t *testing.T) {
	var switched atomic.Bool
	oldCallback := OnGroupNowChanged
	OnGroupNowChanged = func(g C.ProxyAdapter, groupName, before, after string) {
		switched.Store(true)
	}
	t.Cleanup(func() { OnGroupNowChanged = oldCallback })

	nodeA := &r2MockProxy{name: "node-1", delay: 200 * time.Millisecond}
	providers := []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{nodeA}}}

	options := GroupCommonOption{Name: "canceled-group", TestTimeout: 300, URL: "http://example.test"}
	group, err := NewFallback(options, FallbackOption{}, namedProxy("DIRECT"), providers)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	_, err = group.HealthCheckContext(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, switched.Load(), "actively canceled probe round must not trigger switch")
	require.Equal(t, 0, group.ConsecutiveFailures())
}

func TestHealthCheckContextIncompleteNoConsecutiveFailures(t *testing.T) {
	nodeA := &r2MockProxy{name: "node-1", delay: 2 * time.Second}
	nodeB := &r2MockProxy{name: "node-2", delay: 2 * time.Second}
	providers := []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{nodeA, nodeB}}}

	options := GroupCommonOption{Name: "incomplete-group", TestTimeout: 300, URL: "http://example.test"}
	group, err := NewFallback(options, FallbackOption{}, namedProxy("DIRECT"), providers)
	require.NoError(t, err)

	// 50ms deadline: neither node completes
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = group.HealthCheckContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 0, group.ConsecutiveFailures(), "incomplete check truncated by deadline must not increment consecutive failures")
}

type urlTestMockProxy struct {
	C.Proxy
	name      string
	alive     atomic.Bool
	lastDelay atomic.Uint32
}

func (p *urlTestMockProxy) Name() string                      { return p.name }
func (p *urlTestMockProxy) Type() C.AdapterType               { return C.Vmess }
func (p *urlTestMockProxy) AliveForTestUrl(string) bool       { return p.alive.Load() }
func (p *urlTestMockProxy) LastDelayForTestUrl(string) uint16 { return uint16(p.lastDelay.Load()) }

func TestURLTestFastDistinguishesHealthySwitchFromFailure(t *testing.T) {
	failoverCh := make(chan string, 4)
	selectCh := make(chan string, 4)

	oldNowCb := OnGroupNowChanged
	oldSelCb := OnGroupSelectionChanged
	OnGroupNowChanged = func(g C.ProxyAdapter, groupName, before, after string) {
		if m, ok := g.(interface{ MarshalJSON() ([]byte, error) }); ok {
			_, _ = m.MarshalJSON()
		}
		failoverCh <- before + "->" + after
	}
	OnGroupSelectionChanged = func(groupName, before, after string) {
		selectCh <- before + "->" + after
	}
	t.Cleanup(func() {
		OnGroupNowChanged = oldNowCb
		OnGroupSelectionChanged = oldSelCb
	})

	nodeA := &urlTestMockProxy{name: "node-a"}
	nodeA.alive.Store(true)
	nodeA.lastDelay.Store(80)

	nodeB := &urlTestMockProxy{name: "node-b"}
	nodeB.alive.Store(true)
	nodeB.lastDelay.Store(100)

	providers := []P.ProxyProvider{&concurrencyMockProvider{proxies: []C.Proxy{nodeA, nodeB}}}
	options := GroupCommonOption{Name: "urltest-fast", TestTimeout: 300, URL: "http://example.test"}
	group, err := NewURLTest(options, URLTestOption{Tolerance: 50}, namedProxy("DIRECT"), providers)
	require.NoError(t, err)

	require.Equal(t, "node-a", group.Now())

	// Scenario 1: node-b becomes >50ms faster (20ms vs 200ms) while node-a is STILL ALIVE.
	nodeA.lastDelay.Store(200)
	nodeB.lastDelay.Store(20)
	group.fastSingle.Reset()
	require.Equal(t, "node-b", group.Now())

	select {
	case msg := <-selectCh:
		require.Equal(t, "node-a->node-b", msg)
	case msg := <-failoverCh:
		t.Fatalf("healthy latency reselection must NOT invoke OnGroupNowChanged, got %s", msg)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for OnGroupSelectionChanged")
	}

	// Scenario 2: node-b DIES -> switches back to node-a (must invoke OnGroupNowChanged).
	nodeB.alive.Store(false)
	group.fastSingle.Reset()
	require.Equal(t, "node-a", group.Now())

	select {
	case msg := <-failoverCh:
		require.Equal(t, "node-b->node-a", msg)
	case msg := <-selectCh:
		t.Fatalf("dead node switch must invoke OnGroupNowChanged, got OnGroupSelectionChanged: %s", msg)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for OnGroupNowChanged")
	}
}
