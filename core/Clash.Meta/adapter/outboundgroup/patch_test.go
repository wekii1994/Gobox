package outboundgroup

import (
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
)

type fakeProvider struct {
	P.ProxyProvider
	name    string
	proxies []C.Proxy
}

func (f *fakeProvider) Name() string                                        { return f.name }
func (f *fakeProvider) VehicleType() P.VehicleType                          { return P.File }
func (f *fakeProvider) Proxies() []C.Proxy                                  { return f.proxies }
func (f *fakeProvider) Version() uint32                                     { return 1 }
func (f *fakeProvider) Touch()                                              {}
func (f *fakeProvider) HealthCheckURL() string                              { return "" }
func (f *fakeProvider) HealthCheckExpectedStatus() utils.IntRanges[uint16] { return nil }
func (f *fakeProvider) HealthCheckTimeout() time.Duration                  { return 5 * time.Second }

func namedProxy(name string) C.Proxy {
	return adapter.NewProxy(outbound.NewDirectWithOption(outbound.DirectOption{Name: name}))
}
