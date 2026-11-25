package systemprocess

import (
	"context"
	"errors"
	"math"
	"os"
	"runtime"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type ProcessInfo struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	CPU        float64 `json:"cpu"`
	Memory     float64 `json:"memory"`
	CreateTime float64 `json:"create_time"`
	RunTime    int64   `json:"run_time"`
}

func sampleProcesses(ctx context.Context) ([]ProcessInfo, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("system process sampling requires Linux")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pids, err := process.PidsWithContext(ctx)
	if err != nil {
		return nil, err
	}
	// psutil.process_iter samples in ascending PID order. Display order is
	// applied separately by the renderer after collecting all readable rows.
	slices.Sort(pids)
	items := make([]ProcessInfo, 0, len(pids))
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item, err := sampleProcess(ctx, pid)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if processSkippable(err) {
				continue
			}
			return nil, err
		}
		if item != nil {
			items = append(items, *item)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func sampleProcess(ctx context.Context, pid int32) (*ProcessInfo, error) {
	proc, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return nil, err
	}

	// The ignored process_iter CPU prefetch is the first reading. Both times
	// are sampled before reading CPU ticks, as in psutil.cpu_percent().
	firstAt := time.Now()
	first, err := proc.TimesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	status, err := proc.StatusWithContext(ctx)
	if err != nil || slices.Contains(status, "zombie") {
		return nil, err
	}
	memory, err := proc.MemoryInfoWithContext(ctx)
	if err != nil {
		return nil, err
	}
	created, err := proc.CreateTimeWithContext(ctx)
	if err != nil {
		return nil, err
	}
	runTime := processRunTime(time.Now(), created)
	name, err := proc.NameWithContext(ctx)
	if err != nil {
		return nil, err
	}
	secondAt := time.Now()
	second, err := proc.TimesWithContext(ctx)
	if err != nil {
		return nil, err
	}

	cpuPercent := 0.0
	if elapsed := secondAt.Sub(firstAt).Seconds(); elapsed != 0 {
		cpuPercent = processRound(((second.User - first.User) + (second.System - first.System)) / elapsed * 100)
	}
	rss := 0.0
	if memory != nil {
		rss = processRound(float64(memory.RSS) / (1024 * 1024))
	}
	return &ProcessInfo{
		PID: pid, Name: name, CPU: cpuPercent, Memory: rss,
		CreateTime: float64(created) / 1000, RunTime: runTime,
	}, nil
}

func processSkippable(err error) bool {
	return errors.Is(err, process.ErrorProcessNotRunning) || errors.Is(err, process.ErrorNotPermitted) ||
		errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ESRCH)
}

func processRound(value float64) float64 {
	// Python rounds the binary float directly, avoiding an intermediate x*10.
	rounded, _ := strconv.ParseFloat(strconv.FormatFloat(value, 'f', 1, 64), 64)
	return rounded
}

func processRunTime(at time.Time, created int64) int64 {
	// Subtract naive local datetimes, including across a DST transition.
	// timedelta.seconds is the nonnegative residue after whole days.
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
