package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"
)

type FailoverRecord struct {
	Time      int64  `json:"time"`
	GroupName string `json:"group"`
	Before    string `json:"before"`
	After     string `json:"after"`
}

type FailoverStats struct {
	Total   int64            `json:"total"`
	Today   int64            `json:"today"`
	Date    string           `json:"date"`
	Records []FailoverRecord `json:"records"`
}

type pendingSaveTask struct {
	homeDir string
	seq     uint64
	stats   FailoverStats
}

var (
	failoverMu      sync.Mutex
	failoverStats   = FailoverStats{Records: make([]FailoverRecord, 0)}
	failoverHomeDir string
	stateSeq        uint64
	timeNowFunc     = time.Now

	pendingTask    pendingSaveTask
	hasPendingTask bool
	saveSignal     = make(chan struct{}, 1)
	saveInitOnce   sync.Once

	statsWriteMu     sync.Mutex
	lastCommittedSeq uint64
	lastCommittedDir string

	errHomeDirNotSet   = errors.New("home directory not set")
	errHomeDirMismatch = errors.New("home directory changed during operation")
)

const (
	maxFailoverRecords = 50
	failoverStatsFile  = "failover_stats.json"
)

func initAsyncSave() {
	saveInitOnce.Do(func() {
		go func() {
			for range saveSignal {
				drainPendingSaves()
			}
		}()
	})
}

func drainPendingSaves() {
	for {
		failoverMu.Lock()
		if !hasPendingTask {
			failoverMu.Unlock()
			return
		}
		task := pendingTask
		hasPendingTask = false
		failoverMu.Unlock()

		_ = commitSaveTask(task)
	}
}

func commitSaveTask(task pendingSaveTask) error {
	statsWriteMu.Lock()
	defer statsWriteMu.Unlock()

	// If the active HomeDir has changed away from the task's HomeDir, or a newer
	// state (e.g. a subsequent reset or newer save) has already been committed: drop it!
	currentHome := constant.Path.HomeDir()
	if currentHome == "" {
		return errHomeDirNotSet
	}
	if task.homeDir != currentHome {
		return errHomeDirMismatch
	}
	if task.homeDir == lastCommittedDir && task.seq <= lastCommittedSeq {
		return nil
	}

	path := filepath.Join(task.homeDir, failoverStatsFile)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(25 * time.Millisecond)
			if task.homeDir != constant.Path.HomeDir() || (task.homeDir == lastCommittedDir && task.seq <= lastCommittedSeq) {
				return nil
			}
		}
		if err := writeFailoverStatsAtomic(path, task.stats); err == nil {
			lastCommittedSeq = task.seq
			lastCommittedDir = task.homeDir
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		log.Warnln("[Failover] Failed to persist failover stats to %s after 3 attempts: %v", path, lastErr)
		return lastErr
	}
	return nil
}

func copyStatsLocked() FailoverStats {
	res := failoverStats
	res.Records = make([]FailoverRecord, len(failoverStats.Records))
	copy(res.Records, failoverStats.Records)
	return res
}

func writeFailoverStatsAtomic(path string, stats FailoverStats) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "failover_stats_*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	cleanup = false

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func ensureFailoverLoadedLocked() error {
	currentHome := constant.Path.HomeDir()
	if currentHome == "" {
		failoverHomeDir = ""
		failoverStats = FailoverStats{Records: make([]FailoverRecord, 0)}
		return errHomeDirNotSet
	}
	if failoverHomeDir == currentHome {
		return nil
	}

	path := filepath.Join(currentHome, failoverStatsFile)
	data, err := os.ReadFile(path)
	if err == nil {
		var loaded FailoverStats
		if err := json.Unmarshal(data, &loaded); err == nil {
			failoverStats = loaded
			if failoverStats.Records == nil {
				failoverStats.Records = make([]FailoverRecord, 0)
			}
		} else {
			// Corrupted file: initialize empty state for this directory
			failoverStats = FailoverStats{
				Total:   0,
				Today:   0,
				Date:    todayDateString(),
				Records: make([]FailoverRecord, 0),
			}
		}
	} else if errors.Is(err, os.ErrNotExist) {
		// New directory without existing stats file: start with fresh zero state
		failoverStats = FailoverStats{
			Total:   0,
			Today:   0,
			Date:    todayDateString(),
			Records: make([]FailoverRecord, 0),
		}
	} else {
		// Unexpected I/O error: do not use old directory's state and invalidate cache identity!
		failoverStats = FailoverStats{Records: make([]FailoverRecord, 0)}
		failoverHomeDir = ""
		return fmt.Errorf("failed to read failover stats from %s: %w", currentHome, err)
	}

	failoverHomeDir = currentHome
	stateSeq++
	return nil
}

func todayDateString() string {
	return timeNowFunc().Format("2006-01-02")
}

func checkRolloverLocked() {
	today := todayDateString()
	if failoverStats.Date == "" {
		failoverStats.Date = today
	} else if failoverStats.Date != today {
		failoverStats.Today = 0
		failoverStats.Date = today
	}
}

func recordFailover(groupName, before, after string) {
	if groupName == "" || before == "" || after == "" || before == after {
		return
	}

	initAsyncSave()

	failoverMu.Lock()
	if err := ensureFailoverLoadedLocked(); err != nil {
		failoverMu.Unlock()
		log.Warnln("[Failover] Suppressed recordFailover due to home load error: %v", err)
		return
	}
	checkRolloverLocked()

	failoverStats.Total++
	failoverStats.Today++

	record := FailoverRecord{
		Time:      timeNowFunc().UnixMilli(),
		GroupName: groupName,
		Before:    before,
		After:     after,
	}

	failoverStats.Records = append([]FailoverRecord{record}, failoverStats.Records...)
	if len(failoverStats.Records) > maxFailoverRecords {
		failoverStats.Records = failoverStats.Records[:maxFailoverRecords]
	}

	stateSeq++
	pendingTask = pendingSaveTask{
		homeDir: failoverHomeDir,
		seq:     stateSeq,
		stats:   copyStatsLocked(),
	}
	hasPendingTask = true
	failoverMu.Unlock()

	select {
	case saveSignal <- struct{}{}:
	default:
	}
}

func handleGetFailoverStats() (*FailoverStats, error) {
	failoverMu.Lock()
	defer failoverMu.Unlock()

	if err := ensureFailoverLoadedLocked(); err != nil {
		return nil, err
	}
	checkRolloverLocked()

	res := copyStatsLocked()
	return &res, nil
}

func handleResetFailoverStats() (*FailoverStats, error) {
	initAsyncSave()

	failoverMu.Lock()
	if err := ensureFailoverLoadedLocked(); err != nil {
		failoverMu.Unlock()
		return nil, err
	}

	failoverStats = FailoverStats{
		Total:   0,
		Today:   0,
		Date:    todayDateString(),
		Records: make([]FailoverRecord, 0),
	}

	stateSeq++
	task := pendingSaveTask{
		homeDir: failoverHomeDir,
		seq:     stateSeq,
		stats:   copyStatsLocked(),
	}
	hasPendingTask = false
	failoverMu.Unlock()

	if err := commitSaveTask(task); err != nil {
		return nil, err
	}

	failoverMu.Lock()
	res := copyStatsLocked()
	failoverMu.Unlock()

	return &res, nil
}
