package app

import (
	"context"
	"errors"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
	"reflect"
	"testing"
	"time"
)

type ownedProcessReader struct {
	calls  []string
	times  []float64
	status []string
	errAt  string
}

func (p *ownedProcessReader) mark(name string) error {
	p.calls = append(p.calls, name)
	if p.errAt == name {
		return errors.New("owned process disappeared")
	}
	return nil
}
func (p *ownedProcessReader) StatusWithContext(context.Context) ([]string, error) {
	return p.status, p.mark("status")
}
func (p *ownedProcessReader) MemoryInfoWithContext(context.Context) (*process.MemoryInfoStat, error) {
	return &process.MemoryInfoStat{RSS: 12845056}, p.mark("memory")
}
func (p *ownedProcessReader) CreateTimeWithContext(context.Context) (int64, error) {
	return 100000, p.mark("created")
}
func (p *ownedProcessReader) NameWithContext(context.Context) (string, error) {
	return "owned <process>", p.mark("name")
}
func (p *ownedProcessReader) TimesWithContext(context.Context) (*cpu.TimesStat, error) {
	err := p.mark("cpu")
	x := p.times[0]
	p.times = p.times[1:]
	return &cpu.TimesStat{User: x}, err
}
func TestPluginProcessSourceDoubleSampleEveryQuery(t *testing.T) {
	// A long gap between refreshes must not become the measured CPU interval.
	p := &ownedProcessReader{times: []float64{100, 100.25, 1000, 1000.125}}
	clock := []time.Time{time.Unix(90184, 0), time.Unix(90184, 0), time.Unix(90184, 200000000), time.Unix(100000, 0), time.Unix(100000, 0), time.Unix(100000, 100000000)}
	s := pluginProcessSnapshotter{pids: func(context.Context) ([]int32, error) { return []int32{42}, nil }, open: func(context.Context, int32) (pluginProcessReader, error) { return p, nil }, now: func() time.Time { x := clock[0]; clock = clock[1:]; return x }}
	for i := 0; i < 2; i++ {
		got, err := s.snapshot(context.Background())
		if err != nil || len(got) != 1 || got[0].CPU != 125 {
			t.Fatal("not the source intra-query sample", i, err, got)
		}
		if i == 0 && got[0].RunTime != 3684 {
			t.Fatal("source timedelta.seconds must wrap at 24h", got[0].RunTime)
		}
	}
	want := []string{"cpu", "status", "memory", "created", "name", "cpu", "cpu", "status", "memory", "created", "name", "cpu"}
	if !reflect.DeepEqual(p.calls, want) {
		t.Fatal("source prefetch/second CPU sequence differs", p.calls)
	}
}
func TestPluginProcessSourceZombieAndDisappeared(t *testing.T) {
	p := &ownedProcessReader{times: []float64{1}, status: []string{"zombie"}}
	s := pluginProcessSnapshotter{pids: func(context.Context) ([]int32, error) { return []int32{1, 2}, nil }, open: func(_ context.Context, pid int32) (pluginProcessReader, error) {
		if pid == 1 {
			return nil, process.ErrorProcessNotRunning
		}
		return p, nil
	}}
	got, err := s.snapshot(context.Background())
	if err != nil || len(got) != 0 || !reflect.DeepEqual(p.calls, []string{"cpu", "status"}) {
		t.Fatal("zombie/disappeared handling differs", err, p.calls)
	}
}
func TestPluginProcessSourceDecimalRound(t *testing.T) {
	for _, c := range []struct{ v, w float64 }{{2.25, 2.2}, {2.35, 2.4}, {2.65, 2.6}, {2.75, 2.8}, {125.05, 125.0}} {
		if got := processRound(c.v); got != c.w {
			t.Fatal("decimal binary rounding differs", c.v, got, c.w)
		}
	}
}

func TestPluginProcessUnexpectedNativeFailureIsNotEmptySuccess(t *testing.T) {
	s := pluginProcessSnapshotter{pids: func(context.Context) ([]int32, error) { return []int32{42}, nil }, open: func(context.Context, int32) (pluginProcessReader, error) {
		return nil, errors.New("owned protocol failure")
	}}
	if items, err := s.snapshot(context.Background()); err == nil || items != nil {
		t.Fatal("unexpected collector error was hidden as an empty table")
	}
}

func TestPluginProcessSourceNaiveLocalDurationAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		start, end string
		seconds    int64
	}{{"2024-03-10T01:30:00-05:00", "2024-03-10T03:30:00-04:00", 7200}, {"2024-11-03T01:30:00-04:00", "2024-11-03T02:30:00-05:00", 3600}} {
		start, _ := time.Parse(time.RFC3339, c.start)
		end, _ := time.Parse(time.RFC3339, c.end)
		if got := processRunTime(end.In(loc), start.UnixMilli()); got != c.seconds {
			t.Fatal("source naive datetime subtraction differs", got, c.seconds)
		}
	}
}
