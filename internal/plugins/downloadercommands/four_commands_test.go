package downloadercommands

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

// Full original source output, not a target-generated oracle. This draft must
// not be installed until the actual raw and its independent admission arrive.
//
//go:embed testdata/source-four-leaves.json
var sourceFourLeaves []byte

type fourSourceCase struct {
	ID    string `json:"id"`
	Input struct {
		Kind          string         `json:"kind"`
		Method        string         `json:"method"`
		Args          map[string]any `json:"args"`
		ClientPresent bool           `json:"client_present"`
		VendorMethod  string         `json:"vendor_method"`
		VendorReply   any            `json:"vendor_reply"`
		VendorRaises  *struct {
			Class   string `json:"class"`
			Message string `json:"message"`
		} `json:"vendor_raises"`
	} `json:"input"`
	Outcome     map[string]any `json:"outcome"`
	VendorCalls int            `json:"vendor_calls"`
	Trace       []any          `json:"trace"`
}

type fourRecordingPort struct {
	t      *testing.T
	method string
	reply  any
	err    error
	calls  int
	trace  []any
	during func()
}

func (p *fourRecordingPort) invoke(method string, kwargs map[string]any) (any, error) {
	p.t.Helper()
	if method != p.method {
		p.t.Fatalf("unselected fixed port %s, want %s", method, p.method)
	}
	p.calls++
	if p.calls > 1 {
		p.t.Fatal("unexpected repeat")
	}
	p.trace = append(p.trace, map[string]any{"event": "vendor_call", "method": method, "kwargs": kwargs})
	if p.during != nil {
		p.during()
	}
	if p.err != nil {
		var source *SourceException
		if errors.As(p.err, &source) {
			p.trace = append(p.trace, map[string]any{"event": "vendor_exception", "class": source.Class, "message": source.Message})
		}
		return nil, p.err
	}
	p.trace = append(p.trace, map[string]any{"event": "vendor_return", "value": p.reply})
	return p.reply, nil
}
func (p *fourRecordingPort) log(_ context.Context, level, message string) {
	p.trace = append(p.trace, map[string]any{"event": "log", "level": level, "message": message})
}

// Embedded old recording clients make accidental old-operation dispatch fail;
// no old interface, implementation or fixture is extended with new methods.
type fourRecordingQB struct {
	recordingQB
	p *fourRecordingPort
}

func (q fourRecordingQB) AddTags(_ context.Context, ids, tags any) (any, error) {
	return q.p.invoke("torrents_add_tags", map[string]any{"tags": tags, "torrent_hashes": ids})
}
func (q fourRecordingQB) SetCategory(_ context.Context, hash, category any) (any, error) {
	return q.p.invoke("torrents_set_category", map[string]any{"category": category, "torrent_hashes": hash})
}
func (q fourRecordingQB) Stop(_ context.Context, ids any) (any, error) {
	return q.p.invoke("torrents_pause", map[string]any{"torrent_hashes": ids})
}

type fourRecordingTR struct {
	recordingTR
	p *fourRecordingPort
}

func (tr fourRecordingTR) Stop(_ context.Context, ids any) (any, error) {
	return tr.p.invoke("stop_torrent", map[string]any{"ids": ids})
}

func fourLeaves(t *testing.T, p *fourRecordingPort) (QBittorrent, Transmission) {
	t.Helper()
	old := &recordingPort{t: t, method: "no old operation selected"}
	return QBittorrent{Client: fourRecordingQB{recordingQB{old}, p}, Log: p.log}, Transmission{Client: fourRecordingTR{recordingTR{old}, p}, Log: p.log}
}
func invokeFourLeaf(ctx context.Context, q QBittorrent, tr Transmission, kind, method string, args map[string]any) (any, error) {
	if kind == "tr" && method == "stop_torrents" {
		return tr.StopTorrents(ctx, args["ids"])
	}
	if kind != "qb" {
		panic("unselected source kind")
	}
	switch method {
	case "set_torrents_tag":
		return nil, q.SetTorrentsTag(ctx, args["ids"], args["tags"])
	case "set_torrent_category":
		return q.SetTorrentCategory(ctx, args["hash_string"], args["category"])
	case "stop_torrents":
		return q.StopTorrents(ctx, args["ids"])
	default:
		panic("unselected source method")
	}
}

func TestDownloaderFourLeafSourceObservations(t *testing.T) {
	digest := sha256.Sum256(sourceFourLeaves)
	if hex.EncodeToString(digest[:]) != "de761347f6ba64f9d74eafaf0309c99b5a0fbab9aa6d2ad17ccdaa6ea2920c7c" {
		t.Fatal("not the actual complete original source raw")
	}
	var oracle struct {
		SourceCommit string           `json:"source_commit"`
		CaseCount    int              `json:"case_count"`
		Cases        []fourSourceCase `json:"cases"`
	}
	d := json.NewDecoder(bytes.NewReader(sourceFourLeaves))
	d.UseNumber()
	if err := d.Decode(&oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || oracle.CaseCount != 17 || len(oracle.Cases) != 17 {
		t.Fatal("unexpected source scope")
	}
	for _, c := range oracle.Cases {
		t.Run(c.ID, func(t *testing.T) {
			p := &fourRecordingPort{t: t, method: c.Input.VendorMethod, reply: c.Input.VendorReply, trace: []any{}}
			if c.Input.VendorRaises != nil {
				p.err = &SourceException{c.Input.VendorRaises.Class, c.Input.VendorRaises.Message}
			}
			q, tr := fourLeaves(t, p)
			if !c.Input.ClientPresent {
				q.Client = nil
				tr.Client = nil
			}
			result, err := invokeFourLeaf(context.Background(), q, tr, c.Input.Kind, c.Input.Method, c.Input.Args)
			if err != nil {
				t.Fatal("unexpected platform failure on original observation", err)
			}
			actual := map[string]any{"kind": "return", "value": result, "python_type": observedType(result), "python_truthy": result == true}
			sameJSON(t, "original result/type/truth", actual, c.Outcome)
			if p.calls != c.VendorCalls {
				t.Fatal("original call count differs", p.calls, c.VendorCalls)
			}
			sameJSON(t, "original ordered port/exception/log trace", p.trace, c.Trace)
		})
	}
}

// These are target boundary assertions, not additional original observations.
func TestDownloaderFourLeafBoundaries(t *testing.T) {
	for _, c := range []struct{ kind, method, port, prefix string }{
		{"qb", "set_torrents_tag", "torrents_add_tags", "设置种子Tag出错："},
		{"qb", "set_torrent_category", "torrents_set_category", "设置种子分类出错："},
		{"qb", "stop_torrents", "torrents_pause", "暂停种子出错："},
		{"tr", "stop_torrents", "stop_torrent", "停止种子出错："},
	} {
		t.Run(c.kind+"-"+c.method, func(t *testing.T) {
			args := map[string]any{"ids": []any{"owned-a", "owned-a"}, "tags": []any{}, "hash_string": "owned-a", "category": nil}
			p := &fourRecordingPort{t: t, method: c.port, reply: false, trace: []any{}}
			q, tr := fourLeaves(t, p)
			// Existing clients with no optional new port remain valid old clients;
			// selecting a new method must not silently fall back to False/None.
			old := &recordingPort{t: t, method: "no old operation selected"}
			q.Client = recordingQB{old}
			tr.Client = recordingTR{old}
			if _, err := invokeFourLeaf(context.Background(), q, tr, c.kind, c.method, args); err == nil {
				t.Fatal("missing new port was swallowed")
			}
			if p.calls != 0 || old.calls != 0 || len(p.trace) != 0 {
				t.Fatal("unwired method touched port/log")
			}
			for _, marker := range []error{&BoundaryError{"owned current configuration changed"}, context.Canceled, errors.New("owned unknown effect")} {
				p = &fourRecordingPort{t: t, method: c.port, err: marker, trace: []any{}}
				q, tr = fourLeaves(t, p)
				if _, err := invokeFourLeaf(context.Background(), q, tr, c.kind, c.method, args); !errors.Is(err, marker) || p.calls != 1 {
					t.Fatal("boundary swallowed/retried", err, p.calls)
				}
				if len(p.trace) != 1 {
					t.Fatal("boundary became a source business log", p.trace)
				}
			}
			p = &fourRecordingPort{t: t, method: c.port, err: &SourceException{"RuntimeError", "owned source error"}, trace: []any{}}
			q, tr = fourLeaves(t, p)
			result, err := invokeFourLeaf(context.Background(), q, tr, c.kind, c.method, args)
			if err != nil || p.calls != 1 || len(p.trace) != 3 {
				t.Fatal("source exception not preserved", result, err, p.trace)
			}
			if c.method == "set_torrents_tag" {
				if result != nil {
					t.Fatal("source None became bool")
				}
			} else if result != false {
				t.Fatal("source exception did not return False")
			}
			sameJSON(t, "source log prefix", p.trace[2], map[string]any{"event": "log", "level": "ERROR", "message": c.prefix + "owned source error"})
			ctx, cancel := context.WithCancel(context.Background())
			p = &fourRecordingPort{t: t, method: c.port, err: &SourceException{"RuntimeError", "owned source error"}, during: cancel, trace: []any{}}
			q, tr = fourLeaves(t, p)
			if _, err := invokeFourLeaf(ctx, q, tr, c.kind, c.method, args); !errors.Is(err, context.Canceled) || p.calls != 1 || len(p.trace) != 2 {
				t.Fatal("cancel converted to source result/log", err, p.trace)
			}
			p = &fourRecordingPort{t: t, method: c.port, trace: []any{}}
			q, tr = fourLeaves(t, p)
			if _, err := invokeFourLeaf(nil, q, tr, c.kind, c.method, args); err == nil {
				t.Fatal("nil context accepted")
			}
			if _, err := invokeFourLeaf(ctx, q, tr, c.kind, c.method, args); !errors.Is(err, context.Canceled) {
				t.Fatal("pre-cancel lost", err)
			}
			if p.calls != 0 {
				t.Fatal("invalid context dispatched")
			}
		})
	}
	// Static-source-derived finite guard checks, not extra Python executions.
	p := &fourRecordingPort{t: t, method: "torrents_set_category", reply: false, trace: []any{}}
	q, _ := fourLeaves(t, p)
	for _, hash := range []any{nil, "", false, json.Number("0"), []any{}} {
		if got, err := q.SetTorrentCategory(context.Background(), hash, "owned-category"); err != nil || got || p.calls != 0 {
			t.Fatal("falsey hash dispatched", got, err)
		}
	}
	if got, err := q.SetTorrentCategory(context.Background(), "owned-a", json.Number("-0.0")); err != nil || !got || p.calls != 1 {
		t.Fatal("falsey category/ignored return changed", got, err)
	}
	sameJSON(t, "falsey category passed empty string", p.trace[0], map[string]any{"event": "vendor_call", "method": "torrents_set_category", "kwargs": map[string]any{"torrent_hashes": "owned-a", "category": ""}})
}
