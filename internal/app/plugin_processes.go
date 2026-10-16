package app

import (
	"context"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"light-media/internal/plugins/builtin"
)

type pluginCPUSample struct {
	created int64
	seconds float64
	at      time.Time
}

type pluginProcessSnapshotter struct {
	mu      sync.Mutex
	samples map[int32]pluginCPUSample
}

// Like psutil's process_iter, sample CPU across queries, never with a blocking
// sleep. A recycled PID must not inherit the previous process's CPU counters.
func processSampleCPU(previous, current pluginCPUSample) float64 {
	if previous.created != current.created || previous.at.IsZero() {
		return 0
	}
	seconds := current.at.Sub(previous.at).Seconds()
	if seconds <= 0 || current.seconds < previous.seconds {
		return 0
	}
	return math.RoundToEven((current.seconds-previous.seconds)/seconds*1000) / 10
}

func (s *pluginProcessSnapshotter) snapshot(ctx context.Context) ([]builtin.ProcessInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pids, err := process.PidsWithContext(ctx)
	if err != nil {
		return nil, bad("PLUGIN_PROCESS_READ_FAILED", "读取当前容器进程失败")
	}
	if len(pids) > 1024 {
		return nil, bad("PLUGIN_PROCESS_LIMIT", "当前可见进程超过1024个，请检查容器进程隔离设置")
	}
	items := []builtin.ProcessInfo{}
	current := make(map[int32]pluginCPUSample, len(pids))
	for _, pid := range pids {
		if ctx.Err() != nil {
			return nil, bad("PLUGIN_PROCESS_READ_FAILED", "读取当前容器进程超时")
		}
		proc, err := process.NewProcessWithContext(ctx, pid)
		if err != nil {
			continue // Exited, disappeared, or inaccessible: original skips them.
		}
		status, err := proc.StatusWithContext(ctx)
		if err != nil || slices.Contains(status, "zombie") {
			continue
		}
		created, err := proc.CreateTimeWithContext(ctx)
		if err != nil {
			continue
		}
		name, err := proc.NameWithContext(ctx)
		if err != nil {
			continue
		}
		memory, err := proc.MemoryInfoWithContext(ctx)
		if err != nil {
			continue
		}
		cpu, err := proc.TimesWithContext(ctx)
		if err != nil {
			continue
		}
		now := time.Now()
		sample := pluginCPUSample{created: created, seconds: cpu.User + cpu.System, at: now}
		current[pid] = sample
		// timedelta.seconds in the installed original wraps at 24 hours.
		runTime := int64(math.Floor(now.Sub(time.UnixMilli(created)).Seconds())) % 86400
		if runTime < 0 {
			runTime += 86400
		}
		items = append(items, builtin.ProcessInfo{PID: pid, Name: name, CPU: processSampleCPU(s.samples[pid], sample), Memory: math.RoundToEven(float64(memory.RSS)/(1024*1024)*10) / 10, CreateTime: float64(created) / 1000, RunTime: runTime})
	}
	s.samples = current
	slices.SortFunc(items, func(a, b builtin.ProcessInfo) int {
		if a.PID > b.PID {
			return -1
		}
		if a.PID < b.PID {
			return 1
		}
		return 0
	})
	return items, nil
}
