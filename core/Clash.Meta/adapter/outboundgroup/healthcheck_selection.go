package outboundgroup

import C "github.com/metacubex/mihomo/constant"

// notifyHealthCheckSelection distinguishes recovery/latency reselection from a
// failed node. Only the latter may close existing connections and count as a
// failover. The selected replacement must have succeeded in this probe round.
func (gb *GroupBase) notifyHealthCheckSelection(group C.ProxyAdapter, testURL, before, after string, result map[string]uint16) {
	if before == "" || after == "" || before == after {
		return
	}
	if _, verified := result[after]; !verified {
		return
	}
	for _, proxy := range gb.GetProxies(false) {
		if proxy.Name() == before && proxy.AliveForTestUrl(testURL) {
			if OnGroupSelectionChanged != nil {
				OnGroupSelectionChanged(gb.Name(), before, after)
			}
			return
		}
	}
	if OnGroupNowChanged != nil {
		OnGroupNowChanged(group, gb.Name(), before, after)
	}
}
