package main

import (
	"testing"

	"github.com/metacubex/mihomo/common/structure"
	"github.com/metacubex/mihomo/common/utils"
)

func TestReviewPatchPreservesNumericExpectedStatus(t *testing.T) {
	raw := parsePatchProfile(t, `
proxies:
  - {name: Node1, type: direct}
proxy-groups:
  - {name: Auto, type: url-test, proxies: [Node1], expected-status: 418}
proxy-providers:
  custom:
    type: http
    url: https://example.com/provider
    health-check: {enable: true, expected-status: 204}
rules:
  - MATCH,Auto
`)
	patchProxyGroups(raw, patchParams(t, `{}`))
	checks := []struct {
		name    string
		mapping map[string]any
		want    string
	}{
		{"group", raw.ProxyGroup[0], "418"},
		{"provider", raw.ProxyProvider["custom"]["health-check"].(map[string]any), "204"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			var options struct {
				ExpectedStatus string `review:"expected-status,omitempty"`
			}
			decoder := structure.NewDecoder(structure.Option{TagName: "review", WeaklyTypedInput: true})
			if err := decoder.Decode(check.mapping, &options); err != nil {
				t.Fatal(err)
			}
			if options.ExpectedStatus != check.want {
				t.Fatalf("configured HTTP status changed: got %q, want %q", options.ExpectedStatus, check.want)
			}
			if _, err := utils.NewUnsignedRanges[uint16](options.ExpectedStatus); err != nil {
				t.Fatal(err)
			}
		})
	}
}
