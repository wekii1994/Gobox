package outboundgroup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"

	"github.com/stretchr/testify/require"
)

type urlTestFakeProvider struct {
	P.ProxyProvider
	proxies []C.Proxy
}

func (f *urlTestFakeProvider) Name() string               { return "p" }
func (f *urlTestFakeProvider) VehicleType() P.VehicleType { return P.File }
func (f *urlTestFakeProvider) Proxies() []C.Proxy         { return f.proxies }
func (f *urlTestFakeProvider) Version() uint32            { return 1 }
func (f *urlTestFakeProvider) Touch()                     {}
func (f *urlTestFakeProvider) HealthCheckURL() string     { return "" }

func urlTestProxy(name string) C.Proxy {
	return adapter.NewProxy(outbound.NewDirectWithOption(outbound.DirectOption{Name: name}))
}

func TestURLTestLeavesAnUnmeasuredPickOnceAMemberIsMeasured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	first, measured := urlTestProxy("first"), urlTestProxy("measured")
	group, err := NewURLTest(
		GroupCommonOption{Name: "auto", URL: server.URL},
		URLTestOption{},
		urlTestProxy("COMPATIBLE"),
		[]P.ProxyProvider{&urlTestFakeProvider{proxies: []C.Proxy{first, measured}}},
	)
	require.NoError(t, err)
	require.Equal(t, "first", group.Now())

	_, err = measured.URLTest(context.Background(), server.URL, nil)
	require.NoError(t, err)
	require.Equal(t, "first", group.Now())

	time.Sleep(unmeasuredPickHold)
	require.Equal(t, "measured", group.Now())
}

func TestURLTestKeepsAMeasuredPick(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	first, second := urlTestProxy("first"), urlTestProxy("second")
	_, err := first.URLTest(context.Background(), server.URL, nil)
	require.NoError(t, err)
	group, err := NewURLTest(
		GroupCommonOption{Name: "auto", URL: server.URL},
		URLTestOption{},
		urlTestProxy("COMPATIBLE"),
		[]P.ProxyProvider{&urlTestFakeProvider{proxies: []C.Proxy{first, second}}},
	)
	require.NoError(t, err)
	require.Equal(t, "first", group.Now())

	_, err = second.URLTest(context.Background(), server.URL, nil)
	require.NoError(t, err)
	time.Sleep(unmeasuredPickHold)
	require.Equal(t, "first", group.Now())
}

func TestURLTestToleranceHonorsFirstProxy(t *testing.T) {
	firstServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(60 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer firstServer.Close()

	secondServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer secondServer.Close()

	first, second := urlTestProxy("first"), urlTestProxy("second")
	_, err := first.URLTest(context.Background(), firstServer.URL, nil)
	require.NoError(t, err)

	group, err := NewURLTest(
		GroupCommonOption{Name: "auto", URL: firstServer.URL},
		URLTestOption{Tolerance: 200},
		urlTestProxy("COMPATIBLE"),
		[]P.ProxyProvider{&urlTestFakeProvider{proxies: []C.Proxy{first, second}}},
	)
	require.NoError(t, err)
	require.Equal(t, "first", group.Now())

	// Make second faster than first, but well within tolerance (200ms).
	// Previously fastNotExist remained true for proxies[0], bypassing tolerance.
	_, err = second.URLTest(context.Background(), secondServer.URL, nil)
	require.NoError(t, err)
	group.fastSingle.Reset()
	require.Equal(t, "first", group.Now())
}

func TestURLTestConcurrentSelectedAndForceSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	first, second := urlTestProxy("first"), urlTestProxy("second")
	_, err := first.URLTest(context.Background(), server.URL, nil)
	require.NoError(t, err)
	_, err = second.URLTest(context.Background(), server.URL, nil)
	require.NoError(t, err)

	group, err := NewURLTest(
		GroupCommonOption{Name: "auto", URL: server.URL},
		URLTestOption{},
		urlTestProxy("COMPATIBLE"),
		[]P.ProxyProvider{&urlTestFakeProvider{proxies: []C.Proxy{first, second}}},
	)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if (id+i)%2 == 0 {
					_ = group.Set("second")
				} else {
					group.ForceSet("")
				}
				_ = group.Now()
				_, _ = group.MarshalJSON()
			}
		}(g)
	}
	wg.Wait()

	group.ForceSet("")
	raw, err := group.MarshalJSON()
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, "", payload["fixed"])
}
