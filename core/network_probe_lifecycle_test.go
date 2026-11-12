package main

import (
	"context"
	"sync"
	"testing"
)

func resetProbeLifecycleForTest(t *testing.T) {
	t.Helper()
	setNetworkProbeSession(false)
	setNetworkProbeSuspended(false)
	t.Cleanup(func() {
		setNetworkProbeSession(false)
		setNetworkProbeSuspended(false)
	})
}

func assertNoNetworkReset(t *testing.T) {
	t.Helper()
	round := networkProbeRound.Load()
	handleResetNetwork()
	if got := networkProbeRound.Load(); got != round {
		t.Fatalf("inactive network callback created a probe round: before=%d after=%d", round, got)
	}
	resetNetworkMu.Lock()
	hasCancel := resetNetworkCancel != nil
	resetNetworkMu.Unlock()
	if hasCancel {
		t.Fatal("inactive network callback retained a probe context")
	}
}

func TestNetworkProbeLifecycleRejectsStoppedCallbacks(t *testing.T) {
	resetProbeLifecycleForTest(t)
	assertNoNetworkReset(t)
	setNetworkProbeSession(true)
	ctx, cancel := beginResetNetwork(func() {})
	if ctx == nil {
		t.Fatal("active service rejected a network reset")
	}
	defer cancel()
	setNetworkProbeSession(false)
	if ctx.Err() != context.Canceled {
		t.Fatal("stopping the service did not cancel its outstanding probe")
	}
	assertNoNetworkReset(t)

	// A delayed resume callback must not reactivate an explicitly stopped VPN.
	setNetworkProbeSuspended(false)
	assertNoNetworkReset(t)

	setNetworkProbeSession(true)
	next, nextCancel := beginResetNetwork(func() {})
	if next == nil || next.Err() != nil {
		t.Fatal("a newly started service could not create a fresh probe")
	}
	nextCancel()
}

func TestNetworkProbeLifecycleSuspendsAndResumesActiveSession(t *testing.T) {
	resetProbeLifecycleForTest(t)
	setNetworkProbeSession(true)
	ctx, cancel := beginResetNetwork(func() {})
	defer cancel()
	setNetworkProbeSuspended(true)
	if ctx.Err() != context.Canceled {
		t.Fatal("suspension did not cancel the outstanding probe")
	}
	assertNoNetworkReset(t)
	cleanupCalled := false
	blocked, blockedCancel := beginResetNetwork(func() { cleanupCalled = true })
	if blocked != nil || blockedCancel != nil || cleanupCalled {
		t.Fatal("suspended callback performed cleanup or created a probe")
	}
	setNetworkProbeSuspended(false)
	resumed, resumedCancel := beginResetNetwork(func() {})
	if resumed == nil || resumed.Err() != nil {
		t.Fatal("resuming an active service did not reopen the probe gate")
	}
	resumedCancel()
}

func TestNetworkProbeLifecycleCancelsPreviousRoundBeforeCleanup(t *testing.T) {
	resetProbeLifecycleForTest(t)
	setNetworkProbeSession(true)
	previous, previousCancel := beginResetNetwork(func() {})
	defer previousCancel()
	current, currentCancel := beginResetNetwork(func() {
		if previous.Err() != context.Canceled {
			t.Fatal("old-network HTTP probes remained live during connection cleanup")
		}
	})
	defer currentCancel()
	if current.Err() != nil {
		t.Fatal("replacement network probe was canceled with the previous round")
	}
}

func TestNetworkProbeLifecycleConcurrentCallbacksCannotOutliveStop(t *testing.T) {
	resetProbeLifecycleForTest(t)
	setNetworkProbeSession(true)
	var wg sync.WaitGroup
	contexts := make(chan context.Context, 32)
	start := make(chan struct{})
	for i := 0; i < cap(contexts); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if ctx, _ := beginResetNetwork(func() {}); ctx != nil {
				contexts <- ctx
			}
		}()
	}
	close(start)
	setNetworkProbeSession(false)
	wg.Wait()
	close(contexts)
	for ctx := range contexts {
		if ctx.Err() != context.Canceled {
			t.Fatal("a racing network callback created a live context after stop")
		}
	}
	assertNoNetworkReset(t)
}
