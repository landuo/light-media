package downloadercommands

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

//go:embed testdata/source-qb-tags.json
var sourceQBTags []byte

type qbTagStep struct {
	Method string `json:"method"`
	Reply  any    `json:"reply"`
	Raises *struct {
		Class   string `json:"class"`
		Message string `json:"message"`
	} `json:"raises"`
}
type qbTagCase struct {
	ID    string `json:"id"`
	Input struct {
		Method        string         `json:"method"`
		Args          map[string]any `json:"args"`
		ClientPresent bool           `json:"client_present"`
		Steps         []qbTagStep    `json:"steps"`
	} `json:"input"`
	Outcome               map[string]any `json:"outcome"`
	Trace                 []any          `json:"trace"`
	VendorCalls           int            `json:"vendor_calls"`
	SleepRequests         []any          `json:"sleep_requests"`
	RequestedSleepSeconds int            `json:"requested_sleep_seconds"`
	DependencyValues      []any          `json:"dependency_return_values_after_source"`
}
type qbTagReturned struct {
	method  string
	ordinal int
	value   any
	rows    *QBTagRows
}
type qbTagRecording struct {
	t                     *testing.T
	steps                 []qbTagStep
	trace                 []any
	returned              []qbTagReturned
	calls, scopes, sleeps int
	sleepSeconds          []any
	forced                error
	cancel                func()
}

func (p *qbTagRecording) step(method string, kwargs map[string]any) (any, error) {
	p.t.Helper()
	if p.calls >= len(p.steps) {
		p.t.Fatal("unplanned repeated primitive")
	}
	s := p.steps[p.calls]
	p.calls++
	if s.Method != method {
		p.t.Fatalf("primitive order %s != %s", method, s.Method)
	}
	p.trace = append(p.trace, map[string]any{"event": "vendor_call", "method": method, "ordinal": p.calls, "kwargs": kwargs})
	if p.forced != nil {
		return nil, p.forced
	}
	if s.Raises != nil {
		p.trace = append(p.trace, map[string]any{"event": "vendor_exception", "method": method, "class": s.Raises.Class, "message": s.Raises.Message})
		return nil, &SourceException{Class: s.Raises.Class, Message: s.Raises.Message}
	}
	p.trace = append(p.trace, map[string]any{"event": "vendor_return", "method": method, "value": s.Reply})
	p.returned = append(p.returned, qbTagReturned{method: method, ordinal: p.calls, value: s.Reply})
	return s.Reply, nil
}
func (p *qbTagRecording) Info(_ context.Context, status any) (*QBTagRows, error) {
	v, err := p.step("torrents_info", map[string]any{"torrent_hashes": nil, "status_filter": status})
	if err != nil {
		return nil, err
	}
	values, ok := v.([]any)
	if !ok {
		p.t.Fatal("source test dependency was not a list")
	}
	rows := &QBTagRows{Values: values}
	p.returned[len(p.returned)-1].rows = rows
	return rows, nil
}
func (p *qbTagRecording) RemoveTags(_ context.Context, ids, tag any) (any, error) {
	return p.step("torrents_remove_tags", map[string]any{"torrent_hashes": ids, "tags": tag})
}
func (p *qbTagRecording) DeleteTags(_ context.Context, tag any) (any, error) {
	return p.step("torrents_delete_tags", map[string]any{"tags": tag})
}
func (p *qbTagRecording) Sleep(ctx context.Context, d time.Duration) error {
	if d != 3*time.Second {
		p.t.Fatal("source sleep changed", d)
	}
	p.sleeps++
	p.sleepSeconds = append(p.sleepSeconds, 3)
	p.trace = append(p.trace, map[string]any{"event": "sleep_request", "seconds": 3, "ordinal": p.sleeps, "actual_wall_sleep": false})
	if p.cancel != nil {
		p.cancel()
	}
	return ctx.Err()
}
func (p *qbTagRecording) log(_ context.Context, level, message string) {
	p.trace = append(p.trace, map[string]any{"event": "log", "level": level, "message": message})
}

type qbTagScopedRecording struct {
	recordingQB
	p *qbTagRecording
}

func (q qbTagScopedRecording) WithTagScope(ctx context.Context, fn func(context.Context, QBTagPrimitives) (any, error)) (any, error) {
	q.p.scopes++
	if q.p.scopes != 1 {
		q.p.t.Fatal("nested/repeated aggregate scope")
	}
	return fn(ctx, q.p)
}
func makeQBTagRecording(t *testing.T, p *qbTagRecording) QBTagCommands {
	return QBTagCommands{QBittorrent: QBittorrent{Client: qbTagScopedRecording{recordingQB{&recordingPort{t: t, method: "old ports forbidden"}}, p}, Log: p.log}, Sleep: p.Sleep}
}

func TestQBTagLeafActualSourceObservations(t *testing.T) {
	digest := sha256.Sum256(sourceQBTags)
	if hex.EncodeToString(digest[:]) != "267d2e4fa62ed38ce53e4e6a6bf8ebfc5676058b413adbc072e74aa81c9c8617" {
		t.Fatal("not complete original raw")
	}
	var oracle struct {
		SourceCommit string      `json:"source_commit"`
		CaseCount    int         `json:"case_count"`
		Cases        []qbTagCase `json:"cases"`
	}
	d := json.NewDecoder(bytes.NewReader(sourceQBTags))
	d.UseNumber()
	if err := d.Decode(&oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || oracle.CaseCount != 16 || len(oracle.Cases) != 16 {
		t.Fatal("source scope differs")
	}
	for _, c := range oracle.Cases {
		t.Run(c.ID, func(t *testing.T) {
			p := &qbTagRecording{t: t, steps: c.Input.Steps, trace: []any{}, returned: []qbTagReturned{}, sleepSeconds: []any{}}
			q := makeQBTagRecording(t, p)
			if !c.Input.ClientPresent {
				q.Client = nil
			}
			var result any
			var err error
			switch c.Input.Method {
			case "delete_torrents_tag":
				result, err = q.DeleteTorrentsTag(context.Background(), c.Input.Args["ids"], c.Input.Args["tag"])
			case "get_torrent_id_by_tag":
				result, err = q.GetTorrentIDByTag(context.Background(), c.Input.Args["tags"], c.Input.Args["status"])
			default:
				t.Fatal("unselected source method")
			}
			var outcome map[string]any
			if err != nil {
				source, ok := err.(*SourceException)
				if !ok {
					t.Fatal("unexpected platform error", err)
				}
				outcome = map[string]any{"kind": "uncaught_exception", "class": source.Class, "message": source.Message}
			} else {
				outcome = map[string]any{"kind": "return", "value": result, "python_type": observedType(result), "python_truthy": truth(result)}
			}
			sameJSON(t, "source outcome", outcome, c.Outcome)
			sameJSON(t, "full ordered source trace", p.trace, c.Trace)
			if p.calls != c.VendorCalls || p.calls != len(p.steps) || p.sleeps*3 != c.RequestedSleepSeconds {
				t.Fatal("source call/sleep/step counts differ", p.calls, p.sleeps)
			}
			sameJSON(t, "source sleep requests", p.sleepSeconds, c.SleepRequests)
			after := []any{}
			for _, r := range p.returned {
				v := r.value
				if r.rows != nil {
					v = r.rows.Values
				}
				after = append(after, map[string]any{"method": r.method, "ordinal": r.ordinal, "value": v})
			}
			sameJSON(t, "actual original dependency list ownership/clear", after, c.DependencyValues)
			wantScopes := 0
			if c.Input.ClientPresent {
				wantScopes = 1
			}
			if p.scopes != wantScopes {
				t.Fatal("not one aggregate", p.scopes)
			}
		})
	}
}

func TestQBTagLeafAggregateBoundaries(t *testing.T) {
	// A wrapper containing a SourceException is NOT a classified business
	// failure: it must escape with no synthetic source log or second write.
	for _, lookup := range []bool{false, true} {
		marker := fmt.Errorf("owned delegated boundary: %w", &SourceException{Class: "RuntimeError", Message: "owned error"})
		steps := []qbTagStep{{Method: "torrents_remove_tags"}}
		if lookup {
			steps = []qbTagStep{{Method: "torrents_info"}}
		}
		p := &qbTagRecording{t: t, steps: steps, forced: marker, trace: []any{}}
		q := makeQBTagRecording(t, p)
		var err error
		if lookup {
			_, err = q.GetTorrentIDByTag(context.Background(), "owned", nil)
		} else {
			_, err = q.DeleteTorrentsTag(context.Background(), "owned", nil)
		}
		if !errors.Is(err, marker) || p.calls != 1 || p.scopes != 1 {
			t.Fatal("wrapped boundary swallowed/replayed", err, p.calls, p.scopes)
		}
		want := 1
		if lookup {
			want = 2
		}
		if len(p.trace) != want {
			t.Fatal("boundary converted into source error log", p.trace)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &qbTagRecording{t: t, cancel: cancel, trace: []any{}}
	q := makeQBTagRecording(t, p)
	if _, err := q.GetTorrentIDByTag(ctx, "owned", nil); !errors.Is(err, context.Canceled) || p.calls != 0 || p.sleeps != 1 || p.scopes != 1 {
		t.Fatal("sleep cancellation lost", err)
	}
	q = QBTagCommands{QBittorrent: QBittorrent{Client: recordingQB{&recordingPort{t: t}}}}
	if _, err := q.DeleteTorrentsTag(context.Background(), nil, nil); err == nil {
		t.Fatal("missing optional scope accepted")
	}
	if _, err := q.GetTorrentIDByTag(context.Background(), nil, nil); err == nil {
		t.Fatal("missing optional scope accepted")
	}
	if _, err := q.GetTorrentIDByTag(nil, nil, nil); err == nil {
		t.Fatal("nil context accepted")
	}
}
