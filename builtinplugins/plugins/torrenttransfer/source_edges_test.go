package torrenttransfer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type injectedHost struct {
	*oracleHost
	completed func(context.Context, Service) ([]Torrent, error)
	query     func(context.Context, Service, []string) ([]Torrent, error)
	add       func(context.Context, Service, AddSpec) (AddResult, error)
	start     func(context.Context, Service, []string) (EffectResult, error)
	save      func(context.Context, string, any) error
	read      func(context.Context, string) ([]byte, error)
}

func (h *injectedHost) Completed(ctx context.Context, s Service) ([]Torrent, error) {
	if h.completed != nil {
		return h.completed(ctx, s)
	}
	return h.oracleHost.Completed(ctx, s)
}
func (h *injectedHost) Torrents(ctx context.Context, s Service, ids []string) ([]Torrent, error) {
	if h.query != nil {
		return h.query(ctx, s, ids)
	}
	return h.oracleHost.Torrents(ctx, s, ids)
}
func (h *injectedHost) AddPaused(ctx context.Context, s Service, a AddSpec) (AddResult, error) {
	if h.add != nil {
		return h.add(ctx, s, a)
	}
	return h.oracleHost.AddPaused(ctx, s, a)
}
func (h *injectedHost) Start(ctx context.Context, s Service, ids []string) (EffectResult, error) {
	if h.start != nil {
		return h.start(ctx, s, ids)
	}
	return h.oracleHost.Start(ctx, s, ids)
}
func (h *injectedHost) SaveData(ctx context.Context, k string, v any) error {
	if h.save != nil {
		return h.save(ctx, k, v)
	}
	return h.oracleHost.SaveData(ctx, k, v)
}
func (h *injectedHost) ReadFile(ctx context.Context, p string) ([]byte, error) {
	if h.read != nil {
		return h.read(ctx, p)
	}
	return h.oracleHost.ReadFile(ctx, p)
}
func sourceConfig() Config {
	c := NewConfig()
	c.values = map[string]any{"fromdownloader": "source", "todownloader": "destination", "fromtorrentpath": "/backup", "frompath": "/source", "topath": "/destination", "add_torrent_tags": "a", "autostart": false, "deletesource": true, "deleteduplicate": false, "notify": true}
	c.tags = []string{"a"}
	return c
}
func TestSourceQueryNoneEmptyAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rows          []Torrent
		err           error
		clear, wedged bool
	}{{"none", nil, nil, false, false}, {"empty-error-flag-ignored", []Torrent{}, nil, true, false}, {"raised", nil, errors.New("synthetic query exception"), false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			h := &injectedHost{oracleHost: newOracleHost(nil, map[string]any{}), query: func(context.Context, Service, []string) ([]Torrent, error) { return tc.rows, tc.err }}
			c, _ := NewCore(sourceConfig(), h)
			c.state.Recheck["destination"] = []string{sourceHash}
			e := c.CheckRecheck(context.Background())
			if (e != nil) != (tc.err != nil) || c.state.recheckRunning != tc.wedged || (len(c.state.Recheck["destination"]) == 0) != tc.clear {
				t.Fatal(e, c.state)
			}
		})
	}
}
func TestSourceRaisedStartWedgesGateAndFalseStartRemovesIDs(t *testing.T) {
	for _, raises := range []bool{false, true} {
		h := &injectedHost{oracleHost: newOracleHost(nil, map[string]any{}), query: func(context.Context, Service, []string) ([]Torrent, error) {
			return []Torrent{qbSource(sourceHash)}, nil
		}, start: func(context.Context, Service, []string) (EffectResult, error) {
			if raises {
				return EffectResult{}, errors.New("synthetic start exception")
			}
			return EffectResult{Accepted: false}, nil
		}}
		c, _ := NewCore(sourceConfig(), h)
		c.state.Recheck["destination"] = []string{sourceHash, sourceHash, addedHash}
		e := c.CheckRecheck(context.Background())
		if raises {
			if e == nil || !c.state.recheckRunning || len(c.state.Recheck["destination"]) != 3 {
				t.Fatal("source no-finally behavior lost")
			}
		}
		if !raises {
			if e != nil || c.state.recheckRunning || !reflect.DeepEqual(c.state.Recheck["destination"], []string{addedHash}) {
				t.Fatal("false start was not ignored")
			}
		}
	}
}
func TestSourceStopSignalOnlyDuringSelection(t *testing.T) {
	h := &injectedHost{oracleHost: newOracleHost(nil, map[string]any{})}
	c, _ := NewCore(sourceConfig(), h)
	h.add = func(ctx context.Context, s Service, a AddSpec) (AddResult, error) {
		c.Stop()
		return h.oracleHost.AddPaused(ctx, s, a)
	}
	counts, e := c.Transfer(context.Background())
	if e != nil || counts.Success != 1 || len(h.history) != 1 {
		t.Fatal("stop in side-effect phase changed source behavior", counts, e)
	}
	var ops []string
	for _, v := range h.calls {
		ops = append(ops, v.(map[string]any)["op"].(string))
	}
	if !contains(ops, "delete") || !contains(ops, "recheck") {
		t.Fatal("source stopped after selection", ops)
	}
}
func TestSourceFailuresKeepCompletedExternalEffects(t *testing.T) {
	h := &injectedHost{oracleHost: newOracleHost(nil, map[string]any{}), save: func(context.Context, string, any) error { return errors.New("synthetic DB failure") }}
	c, _ := NewCore(sourceConfig(), h)
	counts, e := c.Transfer(context.Background())
	if e == nil || counts.Success != 1 || len(h.messages) != 0 || len(h.history) != 0 {
		t.Fatal("source save failure semantics", e, counts)
	}
	var ops []string
	for _, v := range h.calls {
		ops = append(ops, v.(map[string]any)["op"].(string))
	}
	if !contains(ops, "delete") || !contains(ops, "add") {
		t.Fatal("source effects rolled back", ops)
	}
	h = &injectedHost{oracleHost: newOracleHost(nil, map[string]any{}), read: func(context.Context, string) ([]byte, error) { return nil, errors.New("synthetic read failure") }}
	c, _ = NewCore(sourceConfig(), h)
	counts, e = c.Transfer(context.Background())
	if e == nil || counts.Fail != 0 || len(h.messages) != 0 {
		t.Fatal("uncaught backup read turned into per-item failure")
	}
}
func TestSourceTransferHasNoWholeJobLock(t *testing.T) {
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	// Two invocations share one persistent object's state but independent
	// current-call hosts. Both must reach completed query before release.
	state := newRuntimeState()
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			h := &injectedHost{oracleHost: newOracleHost(nil, map[string]any{}), completed: func(context.Context, Service) ([]Torrent, error) {
				arrived <- struct{}{}
				<-release
				return []Torrent{}, nil
			}}
			c := &Core{Config: sourceConfig(), Host: h, state: state}
			_, _ = c.Transfer(context.Background())
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(time.Second):
			close(release)
			workers.Wait()
			t.Fatal("transfer acquired a whole-job lock")
		}
	}
	close(release)
	workers.Wait()
}
func TestRestartLosesPendingButDoesNotReadHistory(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "transmission")
	a := New()
	invoke(t, a, s, "init", s.config())
	invoke(t, a, s, "transfer", nil)
	if len(s.history) != 1 || len(a.state.Recheck["Target"]) != 1 {
		t.Fatal("setup")
	}
	b := New()
	invoke(t, b, s, "init", s.config())
	invoke(t, b, s, "check_recheck", nil)
	if len(b.state.Recheck) != 0 || len(s.history) != 1 {
		t.Fatal("source queue reconstructed/replayed on restart")
	}
}

func TestSourceNonStringNameDoesNotResolveService(t *testing.T) {
	for _, name := range []any{true, json.Number("1"), []any{"source"}, map[string]any{"name": "source"}} {
		h := newOracleHost(nil, map[string]any{})
		c, _ := NewCore(sourceConfig(), h)
		got, e := c.service(context.Background(), name)
		if e != nil || got != nil || len(h.calls) != 0 || len(h.logs) != 1 {
			t.Fatal("nonstring name lookup", name, got, e)
		}
	}
}
