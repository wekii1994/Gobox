package outboundgroup

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/metacubex/mihomo/common/callback"
	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
)

type FallbackOption struct{}

type Fallback struct {
	*GroupBase
	disableUDP     bool
	testUrl        string
	selected       string
	expectedStatus string
}

func (f *Fallback) Now() string {
	proxy := f.findAliveProxy(false)
	return proxy.Name()
}

func (f *Fallback) TestURL() string {
	return f.testUrl
}

// DialContext implements C.ProxyAdapter
func (f *Fallback) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	isLatencyProbe := C.IsLatencyTestContext(ctx)
	if !isLatencyProbe {
		f.markTrafficUsed(ctx)
	}
	proxy := f.findAliveProxy(true)
	c, err := proxy.DialContext(ctx, metadata)
	if err == nil {
		c.AppendToChains(f)
		if !isLatencyProbe && !N.NeedHandshake(c) {
			f.onDialSuccess()
		}
	} else if !isLatencyProbe {
		f.onDialFailed(proxy.Type(), metadata, err, f.healthCheck)
	}

	if N.NeedHandshake(c) {
		c = callback.NewFirstWriteCallBackConn(c, func(err error) {
			if isLatencyProbe {
				return
			}
			if err == nil {
				f.onDialSuccess()
			} else {
				f.onDialFailed(proxy.Type(), metadata, err, f.healthCheck)
			}
		})
	}

	return c, err
}

// ListenPacketContext implements C.ProxyAdapter
func (f *Fallback) ListenPacketContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error) {
	if !C.IsLatencyTestContext(ctx) {
		f.markTrafficUsed(ctx)
	}
	proxy := f.findAliveProxy(true)
	pc, err := proxy.ListenPacketContext(ctx, metadata)
	if err == nil {
		pc.AppendToChains(f)
	}

	return pc, err
}

// SupportUDP implements C.ProxyAdapter
func (f *Fallback) SupportUDP() bool {
	if f.disableUDP {
		return false
	}

	proxy := f.findAliveProxy(false)
	return proxy.SupportUDP()
}

// IsL3Protocol implements C.ProxyAdapter
func (f *Fallback) IsL3Protocol(metadata *C.Metadata) bool {
	return f.findAliveProxy(false).IsL3Protocol(metadata)
}

// MarshalJSON implements C.ProxyAdapter
func (f *Fallback) MarshalJSON() ([]byte, error) {
	all := []string{}
	for _, proxy := range f.GetProxies(false) {
		all = append(all, proxy.Name())
	}
	return json.Marshal(map[string]any{
		"type":           f.Type().String(),
		"now":            f.Now(),
		"all":            all,
		"testUrl":        f.testUrl,
		"expectedStatus": f.expectedStatus,
		"fixed":          f.selected,
		"hidden":         f.Hidden(),
		"icon":           f.Icon(),
		"emptyFallback":  f.EmptyFallback().Name(),
	})
}

// Unwrap implements C.ProxyAdapter
func (f *Fallback) Unwrap(metadata *C.Metadata, touch bool) C.Proxy {
	proxy := f.findAliveProxy(touch)
	return proxy
}

func (f *Fallback) findAliveProxy(touch bool) C.Proxy {
	proxies := f.GetProxies(touch)
	for _, proxy := range proxies {
		if len(f.selected) == 0 {
			if proxy.AliveForTestUrl(f.testUrl) {
				return proxy
			}
		} else {
			if proxy.Name() == f.selected {
				if proxy.AliveForTestUrl(f.testUrl) {
					return proxy
				} else {
					f.selected = ""
				}
			}
		}
	}

	return proxies[0]
}

func (f *Fallback) Set(name string) error {
	var p C.Proxy
	for _, proxy := range f.GetProxies(false) {
		if proxy.Name() == name {
			p = proxy
			break
		}
	}

	if p == nil {
		return errors.New("proxy not exist")
	}

	f.selected = name
	if !p.AliveForTestUrl(f.testUrl) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*time.Duration(5000))
		defer cancel()
		expectedStatus, _ := utils.NewUnsignedRanges[uint16](f.expectedStatus)
		_, _ = p.URLTest(ctx, f.testUrl, expectedStatus)
	}

	return nil
}

func (f *Fallback) ForceSet(name string) {
	f.selected = name
}

func (f *Fallback) Providers() []P.ProxyProvider {
	return f.providers
}

func (f *Fallback) Proxies() []C.Proxy {
	return f.GetProxies(false)
}

func NewFallback(option GroupCommonOption, fallbackOption FallbackOption, emptyFallback C.Proxy, providers []P.ProxyProvider) (*Fallback, error) {
	return &Fallback{
		GroupBase: NewGroupBase(GroupBaseOption{
			Name:           option.Name,
			Type:           C.Fallback,
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
		disableUDP:     option.DisableUDP,
		testUrl:        option.URL,
		expectedStatus: option.ExpectedStatus,
	}, nil
}

func (f *Fallback) healthCheck() {
	timeout := time.Duration(f.GroupBase.testTimeout) * time.Millisecond
	roundTimeout := roundBudget(len(f.GetProxies(false)), timeout)
	ctx, cancel := context.WithTimeout(context.Background(), roundTimeout)
	defer cancel()
	_, _ = f.HealthCheckContext(ctx)
}

// HealthCheckContext refreshes all candidates, including currently unselected ones.
func (f *Fallback) HealthCheckContext(ctx context.Context) (map[string]uint16, error) {
	if !f.probing.CompareAndSwap(false, true) {
		return nil, ErrHealthCheckInFlight
	}
	defer f.probing.Store(false)

	before := f.Now()
	expected, err := utils.NewUnsignedRanges[uint16](f.expectedStatus)
	if err != nil {
		return nil, err
	}
	result, err := f.GroupBase.URLTest(ctx, f.testUrl, expected)

	// If the entire probe task was actively canceled (e.g. route epoch changed or reset network),
	// never apply stale switch results or update failure state.
	if errors.Is(ctx.Err(), context.Canceled) {
		return result, ctx.Err()
	}

	// If parent deadline expired before all candidates finished:
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// If at least one candidate succeeded before deadline, we have verified alive proxy(ies).
		if len(result) > 0 {
			f.GroupBase.UpdateConsecutiveFailures(false)
			after := f.Now()
			f.notifyHealthCheckSelection(f, f.testUrl, before, after, result)
		}
		// In both partial success and zero success, the probe was truncated by parent deadline.
		// Return result along with ctx.Err() (context.DeadlineExceeded) so callers can distinguish
		// truncated/incomplete checks from complete ones while still accessing verified alive results.
		return result, ctx.Err()
	}

	// Normal complete check (ctx.Err() == nil):
	allFailed := (err != nil || len(result) == 0)
	f.GroupBase.UpdateConsecutiveFailures(allFailed)

	// If at least one candidate succeeded, evaluate valid switch
	if len(result) > 0 {
		after := f.Now()
		f.notifyHealthCheckSelection(f, f.testUrl, before, after, result)
	}

	return result, err
}
