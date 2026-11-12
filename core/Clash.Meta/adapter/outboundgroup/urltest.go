package outboundgroup

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/metacubex/mihomo/common/callback"
	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/common/singledo"
	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
)

type URLTestOption struct {
	Tolerance uint16 `group:"tolerance,omitempty"`
}

type URLTest struct {
	*GroupBase
	selected       string
	selectedMu     sync.RWMutex
	testUrl        string
	expectedStatus string
	tolerance      uint16
	disableUDP     bool
	fastNode       C.Proxy
	fastMu         sync.Mutex
	fastSingle     *singledo.Single[C.Proxy]
}

func (u *URLTest) Now() string {
	fastProxy := u.fast(false)
	if fastProxy == nil {
		return ""
	}
	return fastProxy.Name()
}

func (u *URLTest) TestURL() string {
	return u.testUrl
}

func (u *URLTest) Set(name string) error {
	var p C.Proxy
	for _, proxy := range u.GetProxies(false) {
		if proxy.Name() == name {
			p = proxy
			break
		}
	}
	if p == nil {
		return errors.New("proxy not exist")
	}
	u.ForceSet(name)
	return nil
}

func (u *URLTest) ForceSet(name string) {
	u.selectedMu.Lock()
	u.selected = name
	u.selectedMu.Unlock()
	u.fastSingle.Reset()
}

// DialContext implements C.ProxyAdapter
func (u *URLTest) DialContext(ctx context.Context, metadata *C.Metadata) (c C.Conn, err error) {
	isLatencyProbe := C.IsLatencyTestContext(ctx)
	if !isLatencyProbe {
		u.markTrafficUsed(ctx)
	}
	proxy := u.fast(true)
	c, err = proxy.DialContext(ctx, metadata)
	if err == nil {
		c.AppendToChains(u)
		if !N.NeedHandshake(c) {
			u.onDialSuccess()
		}
	} else if !isLatencyProbe {
		u.onDialFailed(proxy.Type(), metadata, err, u.healthCheck)
	}

	if N.NeedHandshake(c) {
		c = callback.NewFirstWriteCallBackConn(c, func(err error) {
			if err == nil {
				u.onDialSuccess()
			} else if !isLatencyProbe {
				u.onDialFailed(proxy.Type(), metadata, err, u.healthCheck)
			}
		})
	}

	return c, err
}

// ListenPacketContext implements C.ProxyAdapter
func (u *URLTest) ListenPacketContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error) {
	isLatencyProbe := C.IsLatencyTestContext(ctx)
	if !isLatencyProbe {
		u.markTrafficUsed(ctx)
	}
	proxy := u.fast(true)
	pc, err := proxy.ListenPacketContext(ctx, metadata)
	if err == nil {
		pc.AppendToChains(u)
	} else if !isLatencyProbe {
		u.onDialFailed(proxy.Type(), metadata, err, u.healthCheck)
	}

	return pc, err
}

// Unwrap implements C.ProxyAdapter
func (u *URLTest) Unwrap(metadata *C.Metadata, touch bool) C.Proxy {
	return u.fast(touch)
}

func (u *URLTest) healthCheck() {
	timeout := time.Duration(u.GroupBase.testTimeout) * time.Millisecond
	roundTimeout := roundBudget(len(u.GetProxies(false)), timeout)
	ctx, cancel := context.WithTimeout(context.Background(), roundTimeout)
	defer cancel()
	_, _ = u.HealthCheckContext(ctx)
}

func (u *URLTest) fast(touch bool) C.Proxy {
	u.forgetUnmeasuredPick()
	var (
		notifyChanged bool
		oldNodeAlive  bool
		oldNodeName   string
		newNodeName   string
	)
	elm, _, shared := u.fastSingle.Do(func() (C.Proxy, error) {
		u.fastMu.Lock()
		defer u.fastMu.Unlock()

		proxies := u.GetProxies(touch)
		u.selectedMu.RLock()
		selected := u.selected
		u.selectedMu.RUnlock()
		if selected != "" {
			for _, proxy := range proxies {
				if !proxy.AliveForTestUrl(u.testUrl) {
					continue
				}
				if proxy.Name() == selected {
					u.fastNode = proxy
					return proxy, nil
				}
			}
		}

		fast := proxies[0]
		minDelay := fast.LastDelayForTestUrl(u.testUrl)
		fastNotExist := u.fastNode == nil || fast.Name() != u.fastNode.Name()

		for _, proxy := range proxies[1:] {
			if u.fastNode != nil && proxy.Name() == u.fastNode.Name() {
				fastNotExist = false
			}

			if !proxy.AliveForTestUrl(u.testUrl) {
				continue
			}

			delay := proxy.LastDelayForTestUrl(u.testUrl)
			if delay < minDelay {
				fast = proxy
				minDelay = delay
			}

		}
		oldFast := u.fastNode
		// tolerance
		if u.fastNode == nil || fastNotExist || !u.fastNode.AliveForTestUrl(u.testUrl) || u.fastNode.LastDelayForTestUrl(u.testUrl) > fast.LastDelayForTestUrl(u.testUrl)+u.tolerance {
			u.fastNode = fast
		}
		if !u.probing.Load() && oldFast != nil && u.fastNode != nil && oldFast.Name() != u.fastNode.Name() {
			notifyChanged = true
			oldExists := !fastNotExist
			oldNodeAlive = oldExists && oldFast.AliveForTestUrl(u.testUrl)
			oldNodeName = oldFast.Name()
			newNodeName = u.fastNode.Name()
		}
		return u.fastNode, nil
	})
	if shared && touch { // a shared fastSingle.Do() may cause providers untouched, so we touch them again
		u.Touch()
	}

	if notifyChanged {
		if !oldNodeAlive {
			if OnGroupNowChanged != nil {
				go OnGroupNowChanged(u, u.Name(), oldNodeName, newNodeName)
			}
		} else if OnGroupSelectionChanged != nil {
			go OnGroupSelectionChanged(u.Name(), oldNodeName, newNodeName)
		}
	}

	return elm
}

// SupportUDP implements C.ProxyAdapter
func (u *URLTest) SupportUDP() bool {
	if u.disableUDP {
		return false
	}
	return u.fast(false).SupportUDP()
}

// IsL3Protocol implements C.ProxyAdapter
func (u *URLTest) IsL3Protocol(metadata *C.Metadata) bool {
	return u.fast(false).IsL3Protocol(metadata)
}

// MarshalJSON implements C.ProxyAdapter
func (u *URLTest) MarshalJSON() ([]byte, error) {
	all := []string{}
	for _, proxy := range u.GetProxies(false) {
		all = append(all, proxy.Name())
	}
	u.selectedMu.RLock()
	selected := u.selected
	u.selectedMu.RUnlock()
	return json.Marshal(map[string]any{
		"type":           u.Type().String(),
		"now":            u.Now(),
		"all":            all,
		"testUrl":        u.testUrl,
		"expectedStatus": u.expectedStatus,
		"fixed":          selected,
		"hidden":         u.Hidden(),
		"icon":           u.Icon(),
		"emptyFallback":  u.EmptyFallback().Name(),
	})
}

func (u *URLTest) Providers() []P.ProxyProvider {
	return u.providers
}

func (u *URLTest) Proxies() []C.Proxy {
	return u.GetProxies(false)
}

func (u *URLTest) URLTest(ctx context.Context, url string, expectedStatus utils.IntRanges[uint16]) (map[string]uint16, error) {
	return u.GroupBase.URLTest(ctx, u.testUrl, expectedStatus)
}

func NewURLTest(option GroupCommonOption, urlTestOption URLTestOption, emptyFallback C.Proxy, providers []P.ProxyProvider) (*URLTest, error) {
	if emptyFallback == nil {
		return nil, errors.New("empty fallback proxy not exist")
	}
	urlTest := &URLTest{
		GroupBase: NewGroupBase(GroupBaseOption{
			Name:           option.Name,
			Type:           C.URLTest,
			Hidden:         option.Hidden,
			Icon:           option.Icon,
			Filter:         option.Filter,
			ExcludeFilter:  option.ExcludeFilter,
			ExcludeType:    option.ExcludeType,
			TestTimeout:    option.TestTimeout,
			MaxFailedTimes: option.MaxFailedTimes,
			EmptyFallback:  emptyFallback,
			Providers:      providers,
		}),
		fastSingle:     singledo.NewSingle[C.Proxy](time.Second * 10),
		disableUDP:     option.DisableUDP,
		testUrl:        option.URL,
		expectedStatus: option.ExpectedStatus,
		tolerance:      urlTestOption.Tolerance,
	}

	return urlTest, nil
}

// HealthCheckContext refreshes member health using this group's configured URL
// and expected-status policy, and invalidates its cached selection afterwards.
func (u *URLTest) HealthCheckContext(ctx context.Context) (map[string]uint16, error) {
	if !u.probing.CompareAndSwap(false, true) {
		return nil, ErrHealthCheckInFlight
	}
	defer u.probing.Store(false)

	before := u.Now()
	expected, err := utils.NewUnsignedRanges[uint16](u.expectedStatus)
	if err != nil {
		return nil, err
	}
	u.fastSingle.Reset()
	result, err := u.GroupBase.URLTest(ctx, u.testUrl, expected)
	u.fastSingle.Reset()

	// If the entire probe task was actively canceled (e.g. route epoch changed or reset network),
	// never apply stale switch results or update failure state.
	if errors.Is(ctx.Err(), context.Canceled) {
		return result, ctx.Err()
	}

	// If parent deadline expired before all candidates finished:
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// If at least one candidate succeeded before deadline, we have verified alive proxy(ies).
		if len(result) > 0 {
			u.GroupBase.UpdateConsecutiveFailures(false)
			after := u.Now()
			if _, verifiedAlive := result[after]; verifiedAlive {
				if before != "" && after != "" && before != after && OnGroupNowChanged != nil {
					OnGroupNowChanged(u, u.Name(), before, after)
				}
			}
		}
		// In both partial success and zero success, the probe was truncated by parent deadline.
		// Return result along with ctx.Err() (context.DeadlineExceeded) so callers can distinguish
		// truncated/incomplete checks from complete ones while still accessing verified alive results.
		return result, ctx.Err()
	}

	// Normal complete check (ctx.Err() == nil):
	allFailed := (err != nil || len(result) == 0)
	u.GroupBase.UpdateConsecutiveFailures(allFailed)

	// If at least one candidate succeeded, evaluate valid switch
	if len(result) > 0 {
		after := u.Now()
		if _, verifiedAlive := result[after]; verifiedAlive {
			if before != "" && after != "" && before != after && OnGroupNowChanged != nil {
				OnGroupNowChanged(u, u.Name(), before, after)
			}
		}
	}

	return result, err
}
