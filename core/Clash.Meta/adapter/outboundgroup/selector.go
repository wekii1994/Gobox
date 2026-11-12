package outboundgroup

import (
	"context"
	"encoding/json"
	"errors"

	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
)

type SelectorOption struct {
	DefaultSelected string `group:"default-selected,omitempty"`
}

type Selector struct {
	*GroupBase
	disableUDP bool
	selected   string
	testUrl    string
}

// DialContext implements C.ProxyAdapter
func (s *Selector) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	dialCtx := WithTrafficSelectorContext(ctx, s)
	c, err := s.selectedProxy(true).DialContext(dialCtx, metadata)
	if err == nil {
		c.AppendToChains(s)
	}
	return c, err
}

// ListenPacketContext implements C.ProxyAdapter
func (s *Selector) ListenPacketContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error) {
	dialCtx := WithTrafficSelectorContext(ctx, s)
	pc, err := s.selectedProxy(true).ListenPacketContext(dialCtx, metadata)
	if err == nil {
		pc.AppendToChains(s)
	}
	return pc, err
}

// SupportUDP implements C.ProxyAdapter
func (s *Selector) SupportUDP() bool {
	if s.disableUDP {
		return false
	}

	return s.selectedProxy(false).SupportUDP()
}

// IsL3Protocol implements C.ProxyAdapter
func (s *Selector) IsL3Protocol(metadata *C.Metadata) bool {
	return s.selectedProxy(false).IsL3Protocol(metadata)
}

// MarshalJSON implements C.ProxyAdapter
func (s *Selector) MarshalJSON() ([]byte, error) {
	all := []string{}
	for _, proxy := range s.GetProxies(false) {
		all = append(all, proxy.Name())
	}
	// When testurl is the default value
	// do not append a value to ensure that the web dashboard follows the settings of the dashboard
	var url string
	if s.testUrl != C.DefaultTestURL {
		url = s.testUrl
	}

	return json.Marshal(map[string]any{
		"type":          s.Type().String(),
		"now":           s.Now(),
		"all":           all,
		"testUrl":       url,
		"hidden":        s.Hidden(),
		"icon":          s.Icon(),
		"emptyFallback": s.EmptyFallback().Name(),
	})
}

func (s *Selector) Now() string {
	return s.selectedProxy(false).Name()
}

func (s *Selector) TestURL() string {
	return s.testUrl
}

func resetDeselectedChainTraffic(proxy C.Proxy, source *Selector) {
	visited := make(map[string]struct{})
	var walk func(curr C.Proxy)
	walk = func(curr C.Proxy) {
		if curr == nil {
			return
		}
		name := curr.Name()
		if name != "" {
			if _, seen := visited[name]; seen {
				return
			}
			visited[name] = struct{}{}
		}
		wrapper, ok := curr.(interface{ Adapter() C.ProxyAdapter })
		if !ok {
			return
		}
		inner := wrapper.Adapter()
		if inner == nil {
			return
		}
		if resetter, ok := inner.(interface{ resetTrafficActivityForSelector(*Selector) }); ok {
			resetter.resetTrafficActivityForSelector(source)
		} else if resetter, ok := inner.(interface{ ResetTrafficActivity() }); ok {
			resetter.ResetTrafficActivity()
		}
		if sel, ok := inner.(*Selector); ok {
			walk(sel.selectedProxy(false))
			return
		}
		if gp, ok := inner.(interface{ GetProxies(bool) []C.Proxy }); ok {
			for _, child := range gp.GetProxies(false) {
				walk(child)
			}
		}
	}
	walk(proxy)
}

func (s *Selector) switchSelection(name string) {
	prev := s.selectedProxy(false)
	s.selected = name
	if prev != nil && prev.Name() != name {
		resetDeselectedChainTraffic(prev, s)
	}
}

func (s *Selector) Set(name string) error {
	for _, proxy := range s.GetProxies(false) {
		if proxy.Name() == name {
			s.switchSelection(name)
			return nil
		}
	}

	return errors.New("proxy not exist")
}

func (s *Selector) ForceSet(name string) {
	s.switchSelection(name)
}

// Unwrap implements C.ProxyAdapter
func (s *Selector) Unwrap(metadata *C.Metadata, touch bool) C.Proxy {
	return s.selectedProxy(touch)
}

func (s *Selector) selectedProxy(touch bool) C.Proxy {
	proxies := s.GetProxies(touch)
	for _, proxy := range proxies {
		if proxy.Name() == s.selected {
			return proxy
		}
	}

	return proxies[0]
}

func (s *Selector) Providers() []P.ProxyProvider {
	return s.providers
}

func (s *Selector) Proxies() []C.Proxy {
	return s.GetProxies(false)
}

func NewSelector(option GroupCommonOption, selectorOption SelectorOption, emptyFallback C.Proxy, providers []P.ProxyProvider) (*Selector, error) {
	return &Selector{
		GroupBase: NewGroupBase(GroupBaseOption{
			Name:           option.Name,
			Type:           C.Selector,
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
		selected:   selectorOption.DefaultSelected,
		disableUDP: option.DisableUDP,
		testUrl:    option.URL,
	}, nil
}
