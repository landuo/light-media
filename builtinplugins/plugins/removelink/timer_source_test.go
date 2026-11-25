package removelink

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type slowUnlinkOps struct {
	Ops
	now      *time.Time
	slowPath string
}

func (o *slowUnlinkOps) Unlink(path string) error {
	if path == o.slowPath {
		*o.now = o.now.Add(20 * time.Second)
	}
	return o.Ops.Unlink(path)
}
func TestTimerRescheduleMatchesExecutedSourceWithSlowEffect(t *testing.T) {
	raw, e := os.ReadFile("reference/timer-source312.json")
	if e != nil {
		t.Fatal(e)
	}
	var bound struct {
		Observation struct {
			Captured float64  `json:"captured_tick"`
			After    float64  `json:"now_after_effects"`
			Wait     float64  `json:"next_wait"`
			Due      float64  `json:"next_virtual_due"`
			Queue    []string `json:"queue_paths"`
		} `json:"observation"`
	}
	if json.Unmarshal(raw, &bound) != nil || len(bound.Observation.Queue) != 1 {
		t.Fatal("invalid actual source timer fixture")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "m")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"a1", "a2"} {
		a := filepath.Join(dir, name+".mkv")
		if e := os.WriteFile(a, []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.Link(a, filepath.Join(dir, "b"+name[1:]+".mkv")); e != nil {
			t.Fatal(e)
		}
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start
	ops := &fixtureOps{root: root, trace: []map[string]any{}, failUnlink: map[string]error{}}
	m := New(&slowUnlinkOps{Ops: ops, now: &now, slowPath: filepath.Join(dir, "b1.mkv")})
	m.Now = func() time.Time { return now }
	config, _ := json.Marshal(map[string]any{"enabled": true, "monitor_dirs": dir, "delayed_deletion": true, "delay_seconds": 30})
	if e := m.Init(config); e != nil {
		t.Fatal(e)
	}
	for i, name := range []string{"a1", "a2"} {
		now = start.Add(time.Duration(100+i*10) * time.Second)
		path := filepath.Join(dir, name+".mkv")
		if e := os.Remove(path); e != nil {
			t.Fatal(e)
		}
		if e := m.Deleted(path, "hardlink", false); e != nil {
			t.Fatal(e)
		}
	}
	now = start.Add(time.Duration(bound.Observation.Captured) * time.Second)
	m.Tick()
	if now.Sub(start).Seconds() != bound.Observation.After || m.TimerDue == nil || m.TimerDue.Sub(start).Seconds() != bound.Observation.Due || m.TimerDue.Sub(now).Seconds() != bound.Observation.Wait {
		t.Fatal("captured-now relative source timer changed", now, m.TimerDue, bound.Observation)
	}
	if len(m.Queue) != 1 || m.Queue[0].Path != filepath.Join(dir, "a2.mkv") {
		t.Fatal("remaining source task changed", m.Queue)
	}
}

func TestTimerDSTMatchesExecutedSourceNaiveElapsedAndPhysicalTimestamp(t *testing.T) {
	raw, err := os.ReadFile("reference/timer-dst-source312.json")
	if err != nil {
		t.Fatal(err)
	}
	var bound struct {
		Observation struct {
			Event  float64 `json:"event_epoch"`
			Tick   float64 `json:"tick_epoch"`
			Wait   int     `json:"next_wait"`
			Queue  int     `json:"queue_size"`
			Linked bool    `json:"linked_exists"`
		} `json:"observation"`
	}
	if json.Unmarshal(raw, &bound) != nil || bound.Observation.Wait != 1 {
		t.Fatal("invalid executed source DST fixture")
	}
	tzif, err := os.ReadFile("reference/image-new-york.tzif")
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocationFromTZData("owned-image-NY", tzif)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	a := filepath.Join(root, "a.mkv")
	b := filepath.Join(root, "b.mkv")
	if err := os.WriteFile(a, []byte("source fold"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(int64(bound.Observation.Event), 0).In(loc)
	ops := &fixtureOps{root: root, trace: []map[string]any{}, failUnlink: map[string]error{}}
	m := New(ops)
	m.Now = func() time.Time { return now }
	config, _ := json.Marshal(map[string]any{"enabled": true, "monitor_dirs": root, "delayed_deletion": true, "delay_seconds": 30})
	if err := m.Init(config); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := m.Deleted(a, "hardlink", false); err != nil {
		t.Fatal(err)
	}
	now = time.Unix(int64(bound.Observation.Tick), 0).In(loc)
	m.Tick()
	_, err = os.Stat(b)
	linked := err == nil
	if linked != bound.Observation.Linked || len(m.Queue) != bound.Observation.Queue || m.TimerDue == nil || m.TimerDue.Sub(now) != time.Duration(bound.Observation.Wait)*time.Second {
		t.Fatal("Python naive elapsed / timestamp fold distinction lost", m.Queue, m.TimerDue, now, linked)
	}
}
