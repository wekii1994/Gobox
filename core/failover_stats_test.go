package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outboundgroup"
	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
)

func mustGetFailoverStats(t *testing.T) FailoverStats {
	t.Helper()
	stats, err := handleGetFailoverStats()
	if err != nil {
		t.Fatalf("unexpected error getting failover stats: %v", err)
	}
	return *stats
}

func TestFailoverStatsRecordAndRollover(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "failover_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	oldHome := constant.Path.HomeDir()
	constant.SetHomeDir(tempDir)
	defer constant.SetHomeDir(oldHome)

	oldTimeNow := timeNowFunc
	defer func() { timeNowFunc = oldTimeNow }()

	// Clean initial state
	if _, err := handleResetFailoverStats(); err != nil {
		t.Fatal(err)
	}

	// 1. Same node or invalid parameters should be ignored
	recordFailover("AutoGroup", "NodeA", "NodeA")
	recordFailover("", "NodeA", "NodeB")
	recordFailover("AutoGroup", "", "NodeB")
	recordFailover("AutoGroup", "NodeA", "")

	stats := mustGetFailoverStats(t)
	if stats.Total != 0 || stats.Today != 0 || len(stats.Records) != 0 {
		t.Fatalf("expected 0 stats for invalid/same-node failover, got total=%d today=%d", stats.Total, stats.Today)
	}

	// 2. Day 1: 3 failover events
	day1, _ := time.Parse("2006-01-02 15:04:05", "2026-11-04 10:00:00")
	timeNowFunc = func() time.Time { return day1 }

	recordFailover("AutoGroup", "NodeA", "NodeB")
	recordFailover("AutoGroup", "NodeB", "NodeC")
	recordFailover("FallbackGroup", "Node1", "Node2")

	stats = mustGetFailoverStats(t)
	if stats.Total != 3 {
		t.Fatalf("expected total=3 on day 1, got %d", stats.Total)
	}
	if stats.Today != 3 {
		t.Fatalf("expected today=3 on day 1, got %d", stats.Today)
	}
	if stats.Date != "2026-11-04" {
		t.Fatalf("expected date=2026-11-04, got %s", stats.Date)
	}
	if len(stats.Records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(stats.Records))
	}
	// Newest first
	if stats.Records[0].GroupName != "FallbackGroup" || stats.Records[0].Before != "Node1" || stats.Records[0].After != "Node2" {
		t.Fatalf("expected newest record first, got %+v", stats.Records[0])
	}

	// 3. Day 2: date rollover
	day2, _ := time.Parse("2006-01-02 15:04:05", "2026-11-05 08:30:00")
	timeNowFunc = func() time.Time { return day2 }

	// First query on day 2 before any new event should reflect today=0, total=3
	stats = mustGetFailoverStats(t)
	if stats.Today != 0 {
		t.Fatalf("expected today=0 after day rollover query, got %d", stats.Today)
	}
	if stats.Total != 3 {
		t.Fatalf("expected total=3, got %d", stats.Total)
	}

	// Record 1 event on day 2
	recordFailover("AutoGroup", "NodeC", "NodeA")
	stats = mustGetFailoverStats(t)
	if stats.Today != 1 {
		t.Fatalf("expected today=1 on day 2, got %d", stats.Today)
	}
	if stats.Total != 4 {
		t.Fatalf("expected total=4 on day 2, got %d", stats.Total)
	}
	if stats.Date != "2026-11-05" {
		t.Fatalf("expected date=2026-11-05, got %s", stats.Date)
	}

	// 4. Persistence verification: wait for async save worker then verify failover_stats.json content
	time.Sleep(50 * time.Millisecond)
	filePath := filepath.Join(tempDir, failoverStatsFile)
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read persisted failover_stats.json: %v", err)
	}
	var loaded FailoverStats
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("failed to unmarshal persisted failover_stats.json: %v", err)
	}
	if loaded.Total != 4 || loaded.Today != 1 || len(loaded.Records) != 4 {
		t.Fatalf("persisted data mismatch: %+v", loaded)
	}

	// 5. Test Cap at maxFailoverRecords (50)
	for i := 0; i < 60; i++ {
		recordFailover("StressGroup", fmt.Sprintf("N%d", i), fmt.Sprintf("N%d", i+1))
	}
	stats = mustGetFailoverStats(t)
	if len(stats.Records) != maxFailoverRecords {
		t.Fatalf("expected capped records at %d, got %d", maxFailoverRecords, len(stats.Records))
	}
	if stats.Total != 64 {
		t.Fatalf("expected total=64, got %d", stats.Total)
	}

	// 6. Reset
	if _, err := handleResetFailoverStats(); err != nil {
		t.Fatal(err)
	}
	stats = mustGetFailoverStats(t)
	if stats.Total != 0 || stats.Today != 0 || len(stats.Records) != 0 {
		t.Fatalf("expected reset stats to be all 0, got %+v", stats)
	}
}

func TestFailoverStatsIPCMethodCalls(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "failover_ipc_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	oldHome := constant.Path.HomeDir()
	constant.SetHomeDir(tempDir)
	defer constant.SetHomeDir(oldHome)

	if _, err := handleResetFailoverStats(); err != nil {
		t.Fatal(err)
	}
	recordFailover("Group1", "A", "B")

	stats, err := handleGetFailoverStats()
	if err != nil {
		t.Fatalf("expected handleGetFailoverStats to succeed, got error: %v", err)
	}
	if stats == nil || stats.Total != 1 || stats.Today != 1 {
		t.Fatalf("unexpected data from handleGetFailoverStats: %+v", stats)
	}

	statsReset, err := handleResetFailoverStats()
	if err != nil {
		t.Fatalf("expected handleResetFailoverStats to succeed, got error: %v", err)
	}
	if statsReset == nil || statsReset.Total != 0 || statsReset.Today != 0 {
		t.Fatalf("expected reset to return zeroed FailoverStats snapshot, got: %+v", statsReset)
	}

	statsAfter, err := handleGetFailoverStats()
	if err != nil {
		t.Fatalf("expected second handleGetFailoverStats to succeed, got: %v", err)
	}
	if statsAfter == nil || statsAfter.Total != 0 || statsAfter.Today != 0 {
		t.Fatalf("expected 0 stats after reset, got: %+v", statsAfter)
	}
}
func TestFailoverStatsHomeDirReloadOnLateInit(t *testing.T) {
	// Point 8: Verify that early query before HomeDir is set does NOT lock wrong state
	dir1, err := os.MkdirTemp("", "failover_dir1_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir1)

	dir2, err := os.MkdirTemp("", "failover_dir2_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir2)

	oldHome := constant.Path.HomeDir()
	defer constant.SetHomeDir(oldHome)

	// Step 1: In dir2, write a pre-existing stats file with Total=42
	existingStats := FailoverStats{
		Total:   42,
		Today:   5,
		Date:    todayDateString(),
		Records: []FailoverRecord{{Time: timeNowFunc().UnixMilli(), GroupName: "G", Before: "A", After: "B"}},
	}
	data, _ := json.Marshal(existingStats)
	_ = os.WriteFile(filepath.Join(dir2, failoverStatsFile), data, 0o644)

	// Step 2: Simulate early query in dir1 (or empty home)
	constant.SetHomeDir(dir1)
	earlyStats := mustGetFailoverStats(t)
	if earlyStats.Total != 0 {
		t.Fatalf("expected early query in empty dir to return 0, got %d", earlyStats.Total)
	}

	// Step 3: Now app initializes real home dir (dir2)
	constant.SetHomeDir(dir2)

	// Step 4: Next query must load real stats from dir2!
	loaded := mustGetFailoverStats(t)
	if loaded.Total != 42 {
		t.Fatalf("expected reload from new HomeDir to find Total=42, got %d", loaded.Total)
	}

	// Step 5: Recording an event should advance to 43 and not overwrite with 1!
	recordFailover("G", "B", "C")
	afterEvent := mustGetFailoverStats(t)
	if afterEvent.Total != 43 {
		t.Fatalf("expected total to advance to 43, got %d", afterEvent.Total)
	}
}

type mockProxyWithAlive struct {
	constant.Proxy
	name  string
	alive bool
}

func (m *mockProxyWithAlive) Name() string                    { return m.name }
func (m *mockProxyWithAlive) AliveForTestUrl(url string) bool { return m.alive }

type mockGroupWithProxies struct {
	constant.ProxyAdapter
	name    string
	proxies []constant.Proxy
}

func (m *mockGroupWithProxies) Name() string                           { return m.name }
func (m *mockGroupWithProxies) GetProxies(touch bool) []constant.Proxy { return m.proxies }
func (m *mockGroupWithProxies) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"name": m.name,
	})
}

func TestNetworkProbingHealthyReselectionSuppressed(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "probe_suppress_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)
	oldHome := constant.Path.HomeDir()
	constant.SetHomeDir(tempDir)
	defer constant.SetHomeDir(oldHome)

	_, _ = handleResetFailoverStats()

	nodeA := &mockProxyWithAlive{name: "NodeA", alive: true}
	nodeB := &mockProxyWithAlive{name: "NodeB", alive: true}
	group := &mockGroupWithProxies{name: "TestGroup", proxies: []constant.Proxy{nodeA, nodeB}}

	tunnel.UpdateProxies(map[string]constant.Proxy{
		"TestGroup": adapter.NewProxy(group),
	}, nil)
	t.Cleanup(func() { tunnel.UpdateProxies(nil, nil) })

	// Case 1: Passive failover (not in network probing) -> MUST record!
	outboundgroup.OnGroupNowChanged(group, "TestGroup", "NodeA", "NodeB")
	stats := mustGetFailoverStats(t)
	if stats.Total != 1 {
		t.Fatalf("expected passive failover to be recorded (Total=1), got %d", stats.Total)
	}

	// Case 2: During network probe, both NodeA and NodeB are healthy -> MUST BE SUPPRESSED!
	done := markNetworkProbing("TestGroup")
	outboundgroup.OnGroupNowChanged(group, "TestGroup", "NodeB", "NodeA")
	done()

	stats = mustGetFailoverStats(t)
	if stats.Total != 1 {
		t.Fatalf("expected healthy node latency reselection during network probe to be suppressed (Total=1), got %d", stats.Total)
	}

	// Case 3: During network probe, but NodeA is dead (alive=false) -> MUST record!
	nodeA.alive = false
	done = markNetworkProbing("TestGroup")
	outboundgroup.OnGroupNowChanged(group, "TestGroup", "NodeA", "NodeB")
	done()

	stats = mustGetFailoverStats(t)
	if stats.Total != 2 {
		t.Fatalf("expected dead node switch during network probe to be recorded (Total=2), got %d", stats.Total)
	}
}

func TestFailoverStatsResetWinsOverStaleSave(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "stale_save_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)
	oldHome := constant.Path.HomeDir()
	constant.SetHomeDir(tempDir)
	defer constant.SetHomeDir(oldHome)

	// Step 1: Record 1 event
	recordFailover("G", "A", "B")

	// Step 2: Prepare a stale save task with seq=1 (simulating worker holding old snapshot)
	staleTask := pendingSaveTask{
		homeDir: tempDir,
		seq:     1,
		stats:   FailoverStats{Total: 1, Today: 1, Date: todayDateString()},
	}

	// Step 3: Now reset is called (advances seq and commits 0 to disk)
	if _, err := handleResetFailoverStats(); err != nil {
		t.Fatal(err)
	}

	// Step 4: Now the delayed worker commits its stale task
	commitSaveTask(staleTask)

	// Step 5: Verify disk file remains 0 and was NOT overwritten by stale task!
	data, err := os.ReadFile(filepath.Join(tempDir, failoverStatsFile))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk FailoverStats
	_ = json.Unmarshal(data, &onDisk)
	if onDisk.Total != 0 {
		t.Fatalf("stale save task overwrote reset on disk! expected Total=0, got %d", onDisk.Total)
	}
}

type mockProxyWithURLAlive struct {
	constant.Proxy
	name         string
	aliveByURL   map[string]bool
	defaultAlive bool
}

func (m *mockProxyWithURLAlive) Name() string { return m.name }
func (m *mockProxyWithURLAlive) AliveForTestUrl(url string) bool {
	if val, ok := m.aliveByURL[url]; ok {
		return val
	}
	return m.defaultAlive
}

type mockGroupWithCustomURL struct {
	constant.ProxyAdapter
	name    string
	testURL string
	proxies []constant.Proxy
}

func (m *mockGroupWithCustomURL) Name() string                           { return m.name }
func (m *mockGroupWithCustomURL) GetProxies(touch bool) []constant.Proxy { return m.proxies }
func (m *mockGroupWithCustomURL) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"name":    m.name,
		"testUrl": m.testURL,
	})
}

func TestFailoverStatsURLSpecificAliveCheck(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "url_check_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)
	oldHome := constant.Path.HomeDir()
	constant.SetHomeDir(tempDir)
	defer constant.SetHomeDir(oldHome)

	_, _ = handleResetFailoverStats()

	customGroupURL := "https://group-custom.com/generate_204"

	// NodeA: alive on default URL (""), but DEAD on group custom URL!
	nodeA := &mockProxyWithURLAlive{
		name:         "NodeA",
		defaultAlive: true,
		aliveByURL: map[string]bool{
			customGroupURL: false,
		},
	}
	nodeB := &mockProxyWithURLAlive{
		name:         "NodeB",
		defaultAlive: true,
		aliveByURL: map[string]bool{
			customGroupURL: true,
		},
	}
	group := &mockGroupWithCustomURL{
		name:    "CustomGroup",
		testURL: customGroupURL,
		proxies: []constant.Proxy{nodeA, nodeB},
	}

	tunnel.UpdateProxies(map[string]constant.Proxy{
		"CustomGroup": adapter.NewProxy(group),
	}, nil)
	t.Cleanup(func() { tunnel.UpdateProxies(nil, nil) })

	// Even during network probe: since NodeA is dead on customGroupURL, it MUST be counted as failover!
	done := markNetworkProbing("CustomGroup")
	outboundgroup.OnGroupNowChanged(group, "CustomGroup", "NodeA", "NodeB")
	done()

	stats := mustGetFailoverStats(t)
	if stats.Total != 1 {
		t.Fatalf("expected dead node on group-url to be recorded as failover (Total=1), got %d", stats.Total)
	}
}

func TestFailoverStatsDirectorySwitchDoesNotPolluteEmptyDir(t *testing.T) {
	dir1, err := os.MkdirTemp("", "pollute_dir1_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir1)

	dir2, err := os.MkdirTemp("", "pollute_dir2_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir2)

	oldHome := constant.Path.HomeDir()
	defer constant.SetHomeDir(oldHome)

	// Step 1: Record 1 failover in dir1
	constant.SetHomeDir(dir1)
	recordFailover("G", "A", "B")
	stats1 := mustGetFailoverStats(t)
	if stats1.Total != 1 {
		t.Fatalf("expected Total=1 in dir1, got %d", stats1.Total)
	}

	// Step 2: Switch HomeDir to fresh empty dir2 (no file exists)
	constant.SetHomeDir(dir2)

	// Step 3: Querying dir2 must return 0, NOT dir1's 1!
	stats2 := mustGetFailoverStats(t)
	if stats2.Total != 0 {
		t.Fatalf("switching to empty dir2 must return Total=0, got %d", stats2.Total)
	}
}

func TestFailoverStatsAtomicRenamePreservesOldFileOnFailure(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "atomic_rename_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, failoverStatsFile)

	// Step 1: Write an existing valid file with Total=100
	initialStats := FailoverStats{Total: 100, Today: 10, Date: todayDateString()}
	if err := writeFailoverStatsAtomic(filePath, initialStats); err != nil {
		t.Fatalf("initial write failed: %v", err)
	}

	// Step 2: Lock the target file by opening it without sharing
	lockHandle, err := os.OpenFile(filePath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Step 3: Try to write new stats while locked -> must fail
	newStats := FailoverStats{Total: 999, Today: 99, Date: todayDateString()}
	err = writeFailoverStatsAtomic(filePath, newStats)
	// On Windows, rename over locked file fails; on Unix it might succeed, so only assert preservation
	lockHandle.Close()

	// Step 4: Regardless of whether err occurred or not, the old file must NEVER have been destroyed!
	data, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatalf("file was deleted or became missing! %v", readErr)
	}
	var current FailoverStats
	if err := json.Unmarshal(data, &current); err != nil {
		t.Fatalf("file content corrupted: %v", err)
	}
	if err != nil && current.Total != 100 {
		t.Fatalf("failed rename corrupted or wiped previous file! got Total=%d", current.Total)
	}

	// Step 5: Verify no temporary .tmp files leaked in directory
	entries, _ := os.ReadDir(tempDir)
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Fatalf("leaked temporary file: %s", entry.Name())
		}
	}
}

func TestFailoverStatsCoalescingPreservesLatestSnapshot(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "coalesce_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)
	oldHome := constant.Path.HomeDir()
	constant.SetHomeDir(tempDir)
	defer constant.SetHomeDir(oldHome)

	_, _ = handleResetFailoverStats()

	// Simulate disk congestion by holding statsWriteMu
	statsWriteMu.Lock()

	// Record 25 events in rapid succession while disk worker is blocked
	for i := 0; i < 25; i++ {
		recordFailover("CoalesceGroup", fmt.Sprintf("Node%d", i), fmt.Sprintf("Node%d", i+1))
	}

	// Unblock disk worker
	statsWriteMu.Unlock()

	// Wait briefly for worker to drain
	time.Sleep(100 * time.Millisecond)

	// Memory must be 25
	stats := mustGetFailoverStats(t)
	if stats.Total != 25 {
		t.Fatalf("expected memory Total=25, got %d", stats.Total)
	}

	// Disk must ALSO be 25 (not stuck at older snapshot)!
	data, err := os.ReadFile(filepath.Join(tempDir, failoverStatsFile))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk FailoverStats
	_ = json.Unmarshal(data, &onDisk)
	if onDisk.Total != 25 {
		t.Fatalf("persisted snapshot was lost during congestion! expected Total=25 on disk, got %d", onDisk.Total)
	}
}

func TestFailoverStatsPostResetNewEventNotOverwrittenByReset(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "post_reset_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)
	oldHome := constant.Path.HomeDir()
	constant.SetHomeDir(tempDir)
	defer constant.SetHomeDir(oldHome)

	// 1. Initial state: 1 failover
	recordFailover("G", "A", "B")
	time.Sleep(50 * time.Millisecond)

	// 2. Prepare an older reset task at seq=2 (Total=0)
	staleResetTask := pendingSaveTask{
		homeDir: tempDir,
		seq:     2,
		stats:   FailoverStats{Total: 0, Today: 0, Date: todayDateString()},
	}

	// 3. Meanwhile, a new post-reset event arrives and commits with seq=3 (Total=1)
	statsWriteMu.Lock()
	lastCommittedSeq = 3
	lastCommittedDir = tempDir
	postResetStats := FailoverStats{Total: 1, Today: 1, Date: todayDateString()}
	_ = writeFailoverStatsAtomic(filepath.Join(tempDir, failoverStatsFile), postResetStats)
	statsWriteMu.Unlock()

	// 4. Now the delayed reset tries to commit its older seq=2 task
	err = commitSaveTask(staleResetTask)
	if err != nil {
		t.Fatal(err)
	}

	// 5. Verify disk still has Total=1 (seq=3 was NOT overwritten by older seq=2 reset)
	data, err := os.ReadFile(filepath.Join(tempDir, failoverStatsFile))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk FailoverStats
	_ = json.Unmarshal(data, &onDisk)
	if onDisk.Total != 1 {
		t.Fatalf("older reset task rolled back disk to Total=0! expected Total=1, got %d", onDisk.Total)
	}
}

func TestFailoverStatsUnreadableHomeDoesNotWipePreviousHome(t *testing.T) {
	dir1, err := os.MkdirTemp("", "valid_home_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir1)

	dir2, err := os.MkdirTemp("", "unreadable_home_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir2)

	oldHome := constant.Path.HomeDir()
	defer constant.SetHomeDir(oldHome)

	// 1. In dir1: record a failover event, verify Total=42
	constant.SetHomeDir(dir1)
	initStats := FailoverStats{Total: 42, Today: 5, Date: todayDateString()}
	data, _ := json.Marshal(initStats)
	_ = os.WriteFile(filepath.Join(dir1, failoverStatsFile), data, 0o644)

	stats := mustGetFailoverStats(t)
	if stats.Total != 42 {
		t.Fatalf("expected Total=42 in dir1, got %d", stats.Total)
	}

	// 2. In dir2: create a directory with the same name as failover_stats.json so os.ReadFile fails
	badPath := filepath.Join(dir2, failoverStatsFile)
	if err := os.Mkdir(badPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// 3. Switch to dir2
	constant.SetHomeDir(dir2)

	// 4. Calling get or reset must return an error!
	if _, err := handleGetFailoverStats(); err == nil {
		t.Fatalf("expected handleGetFailoverStats on unreadable home to fail with error")
	}
	if _, err := handleResetFailoverStats(); err == nil {
		t.Fatalf("expected handleResetFailoverStats on unreadable home to fail with error")
	}

	// 5. Verify dir1's file was NEVER wiped or touched!
	data1, err := os.ReadFile(filepath.Join(dir1, failoverStatsFile))
	if err != nil {
		t.Fatalf("dir1 stats file missing! %v", err)
	}
	var loaded1 FailoverStats
	_ = json.Unmarshal(data1, &loaded1)
	if loaded1.Total != 42 {
		t.Fatalf("previous home was wiped or corrupted! expected Total=42, got %d", loaded1.Total)
	}

	// 6. Switch back to dir1: must properly reload dir1 and restore Total=42!
	constant.SetHomeDir(dir1)
	statsBack := mustGetFailoverStats(t)
	if statsBack.Total != 42 {
		t.Fatalf("expected returning to dir1 to reload Total=42, got %d", statsBack.Total)
	}

	// 7. Record a new event in dir1: must advance to 43 (NOT 1!)
	recordFailover("G", "B", "C")
	time.Sleep(50 * time.Millisecond)

	statsAfter := mustGetFailoverStats(t)
	if statsAfter.Total != 43 {
		t.Fatalf("expected Total=43 after recording event in dir1, got %d", statsAfter.Total)
	}

	dataAfter, err := os.ReadFile(filepath.Join(dir1, failoverStatsFile))
	if err != nil {
		t.Fatal(err)
	}
	var onDiskAfter FailoverStats
	_ = json.Unmarshal(dataAfter, &onDiskAfter)
	if onDiskAfter.Total != 43 {
		t.Fatalf("disk in dir1 corrupted after returning from unreadable home! expected Total=43 on disk, got %d", onDiskAfter.Total)
	}
}

func TestFailoverStatsEmptyHomeReturnsError(t *testing.T) {
	oldHome := constant.Path.HomeDir()
	defer constant.SetHomeDir(oldHome)

	constant.SetHomeDir("")
	if _, err := handleGetFailoverStats(); err == nil {
		t.Fatalf("expected handleGetFailoverStats to return error when HomeDir is empty")
	}
	if _, err := handleResetFailoverStats(); err == nil {
		t.Fatalf("expected handleResetFailoverStats to return error when HomeDir is empty")
	}
}
