package app

import (
	"context"
	"errors"
	"math"
	"os"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
	"light-media/internal/plugins/builtin"
)

type pluginCPUSample struct {
	created int64
	user    float64
	system  float64
	at      time.Time
}

type pluginProcessReader interface {
	StatusWithContext(context.Context) ([]string, error)
	MemoryInfoWithContext(context.Context) (*process.MemoryInfoStat, error)
	CreateTimeWithContext(context.Context) (int64, error)
	NameWithContext(context.Context) (string, error)
	TimesWithContext(context.Context) (*cpu.TimesStat, error)
}

type pluginProcessSnapshotter struct {
	mu sync.Mutex
	// Owned fixtures inject only these read operations; production never executes
	// commands, reads command lines/environment, or changes a process.
	pids func(context.Context) ([]int32, error)
	open func(context.Context, int32) (pluginProcessReader, error)
	now  func() time.Time
}

func processRound(value float64) float64 {
	// Python round(x,1) rounds the actual binary value to decimal, rather than
	// rounding an already rounded x*10 intermediate.
	value, _ = strconv.ParseFloat(strconv.FormatFloat(value, 'f', 1, 64), 64)
	return value
}

func processSampleCPU(previous, current pluginCPUSample) float64 {
	if previous.created != current.created || previous.at.IsZero() {
		return 0
	}
	elapsed := current.at.Sub(previous.at).Seconds()
	if elapsed == 0 {
		return 0
	}
	return processRound(((current.user - previous.user) + (current.system - previous.system)) / elapsed * 100)
}

func processReadCPU(ctx context.Context, proc pluginProcessReader, created int64, now func() time.Time) (pluginCPUSample, error) {
	// psutil.cpu_percent records the clock before reading process CPU times.
	at := now()
	times, err := proc.TimesWithContext(ctx)
	if err != nil {
		return pluginCPUSample{}, err
	}
	return pluginCPUSample{created: created, user: times.User, system: times.System, at: at}, nil
}

func processSkippable(err error) bool {
	return errors.Is(err, process.ErrorProcessNotRunning) || errors.Is(err, process.ErrorNotPermitted) || errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ESRCH)
}
func processReadFailed() error {
	return bad("PLUGIN_PROCESS_READ_FAILED", "读取当前容器进程失败")
}

// The source subtracts two naive local datetimes, then reads timedelta.seconds.
// Strip timezone/monotonic state before subtracting, including DST transitions.
func processRunTime(at time.Time, created int64) int64 {
	start := time.UnixMilli(created).In(at.Location())
	wall := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	}
	seconds := int64(math.Floor(wall(at).Sub(wall(start)).Seconds())) % 86400
	if seconds < 0 {
		seconds += 86400
	}
	return seconds
}

func (s *pluginProcessSnapshotter) snapshot(ctx context.Context) ([]builtin.ProcessInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pidsFn := s.pids
	if pidsFn == nil {
		pidsFn = process.PidsWithContext
	}
	open := s.open
	if open == nil {
		open = openNativePluginProcess
	}
	now := s.now
	if now == nil {
		now = time.Now
	}
	pids, err := pidsFn(ctx)
	if err != nil {
		return nil, bad("PLUGIN_PROCESS_READ_FAILED", "读取当前容器进程失败")
	}
	slices.Sort(pids)
	items := []builtin.ProcessInfo{}
	for _, pid := range pids {
		if ctx.Err() != nil {
			return nil, bad("PLUGIN_PROCESS_READ_FAILED", "读取当前容器进程超时")
		}
		proc, err := open(ctx, pid)
		if err != nil {
			if processSkippable(err) {
				continue
			}
			return nil, processReadFailed()
		}
		// process_iter(attrs) prefetches cpu_percent once. The plugin ignores its
		// value, and calls cpu_percent again while constructing this very row.
		// Never average CPU between separate HTTP/dashboard refreshes.
		first, err := processReadCPU(ctx, proc, 0, now)
		if err != nil {
			if processSkippable(err) {
				continue
			}
			return nil, processReadFailed()
		}
		status, err := proc.StatusWithContext(ctx)
		if err != nil {
			if processSkippable(err) {
				continue
			}
			return nil, processReadFailed()
		}
		if slices.Contains(status, "zombie") {
			continue
		}
		memory, err := proc.MemoryInfoWithContext(ctx)
		if err != nil {
			if processSkippable(err) {
				continue
			}
			return nil, processReadFailed()
		}
		created, err := proc.CreateTimeWithContext(ctx)
		if err != nil {
			if processSkippable(err) {
				continue
			}
			return nil, processReadFailed()
		}
		at := now()
		runTime := processRunTime(at, created)
		name, err := proc.NameWithContext(ctx)
		if err != nil {
			if processSkippable(err) {
				continue
			}
			return nil, processReadFailed()
		}
		second, err := processReadCPU(ctx, proc, 0, now)
		if err != nil {
			if processSkippable(err) {
				continue
			}
			return nil, processReadFailed()
		}
		rss := 0.0
		if memory != nil {
			rss = processRound(float64(memory.RSS) / (1024 * 1024))
		}
		items = append(items, builtin.ProcessInfo{PID: pid, Name: name, CPU: processSampleCPU(first, second), Memory: rss, CreateTime: float64(created) / 1000, RunTime: runTime})
	}
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
