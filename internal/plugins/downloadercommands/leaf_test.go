package downloadercommands

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

//go:embed testdata/source-leaves.json
var sourceLeaves []byte

type sourceCase struct {
	ID    string `json:"id"`
	Input struct {
		Kind          string         `json:"kind"`
		Method        string         `json:"method"`
		Args          map[string]any `json:"args"`
		ClientPresent bool           `json:"client_present"`
		Instance      map[string]any `json:"instance"`
		VendorMethod  string         `json:"vendor_method"`
		VendorReply   any            `json:"vendor_reply"`
		VendorRaises  *string        `json:"vendor_raises"`
	} `json:"input"`
	Outcome     map[string]any `json:"outcome"`
	VendorCalls int            `json:"vendor_calls"`
	Trace       []any          `json:"trace"`
}

type recordingPort struct {
	t         *testing.T
	method    string
	reply     any
	raises    *string
	hostError error
	calls     int
	trace     []any
}

func (p *recordingPort) invoke(method string, kwargs map[string]any) (any, error) {
	p.t.Helper()
	if method != p.method {
		p.t.Fatalf("unexpected vendor operation %s, want %s", method, p.method)
	}
	p.calls++
	p.trace = append(p.trace, map[string]any{"event": "vendor_call", "method": method, "kwargs": encodeObservation(kwargs)})
	if p.hostError != nil {
		return nil, p.hostError
	}
	if p.raises != nil {
		p.trace = append(p.trace, map[string]any{"event": "vendor_exception", "class": "RuntimeError", "message": *p.raises})
		return nil, &SourceException{"RuntimeError", *p.raises}
	}
	p.trace = append(p.trace, map[string]any{"event": "vendor_return", "value": encodeObservation(p.reply)})
	return p.reply, nil
}
func (p *recordingPort) log(_ context.Context, level, message string) {
	p.trace = append(p.trace, map[string]any{"event": "log", "level": level, "message": message})
}

type recordingQB struct{ *recordingPort }

func (q recordingQB) Add(_ context.Context, c QBAddCall) (any, error) {
	kw := map[string]any{"urls": c.URLs, "torrent_files": c.TorrentFiles, "save_path": c.SavePath, "is_paused": c.Paused, "tags": c.Tags, "use_auto_torrent_management": c.AutoManagement, "is_sequential_download": c.Sequential, "is_first_last_piece_priority": c.FirstLastPiece, "cookie": c.Cookie, "category": c.Category}
	for key, value := range c.Extra {
		if _, exists := kw[key]; exists {
			q.t.Fatal("duplicate vendor keyword reached recording call", key)
		}
		kw[key] = value
	}
	return q.invoke("torrents_add", kw)
}
func (q recordingQB) Start(_ context.Context, ids any) (any, error) {
	return q.invoke("torrents_resume", map[string]any{"torrent_hashes": ids})
}
func (q recordingQB) Recheck(_ context.Context, ids any) (any, error) {
	return q.invoke("torrents_recheck", map[string]any{"torrent_hashes": ids})
}
func (q recordingQB) DeleteKeepFiles(_ context.Context, ids any) (any, error) {
	return q.invoke("torrents_delete", map[string]any{"delete_files": false, "torrent_hashes": ids})
}

type recordingTR struct{ *recordingPort }

func (t recordingTR) Add(_ context.Context, c TRAddCall) (any, error) {
	return t.invoke("add_torrent", map[string]any{"torrent": c.Content, "download_dir": c.DownloadDir, "paused": c.Paused, "labels": c.Labels, "cookies": c.Cookies})
}
func (t recordingTR) Start(_ context.Context, ids any) (any, error) {
	return t.invoke("start_torrent", map[string]any{"ids": ids})
}
func (t recordingTR) Recheck(_ context.Context, ids any) (any, error) {
	return t.invoke("verify_torrent", map[string]any{"ids": ids})
}
func (t recordingTR) DeleteKeepFiles(_ context.Context, ids any) (any, error) {
	return t.invoke("remove_torrent", map[string]any{"delete_data": false, "ids": ids})
}

func decodeOwned(t *testing.T, value any) any {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		if encoded, ok := v["$bytes_base64"]; ok && len(v) == 1 {
			raw, err := base64.StdEncoding.DecodeString(encoded.(string))
			if err != nil {
				t.Fatal(err)
			}
			return raw
		}
		out := map[string]any{}
		for k, item := range v {
			out[k] = decodeOwned(t, item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = decodeOwned(t, item)
		}
		return out
	default:
		return value
	}
}
func encodeObservation(value any) any {
	switch v := value.(type) {
	case []byte:
		h := sha256.Sum256(v)
		return map[string]any{"kind": "bytes", "base64": base64.StdEncoding.EncodeToString(v), "length": len(v), "sha256": hex.EncodeToString(h[:])}
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = encodeObservation(item)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, item := range v {
			out[k] = encodeObservation(item)
		}
		return out
	default:
		return value
	}
}
func sameJSON(t *testing.T, label string, got, want any) {
	t.Helper()
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(g, w) {
		t.Fatalf("%s differs\ngot %s\nwant%s", label, g, w)
	}
}
func ordinaryIdentity(got, want any) bool {
	switch v := got.(type) {
	case map[string]any:
		w, ok := want.(map[string]any)
		return ok && fmt.Sprintf("%p", v) == fmt.Sprintf("%p", w)
	case []any:
		w, ok := want.([]any)
		return ok && fmt.Sprintf("%p", v) == fmt.Sprintf("%p", w)
	default:
		return reflect.DeepEqual(got, want)
	}
}
func observedType(value any) string {
	switch value.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case map[string]any:
		return "dict"
	case []any:
		return "list"
	case string:
		return "str"
	}
	return "unobserved"
}

func TestDownloaderLeafSourceObservations(t *testing.T) {
	digest := sha256.Sum256(sourceLeaves)
	if hex.EncodeToString(digest[:]) != "8de99e30850e6a111b9b97de08de755b50452bbd6da463567111e11872952e26" {
		t.Fatal("not the original complete source raw")
	}
	var oracle struct {
		SourceCommit string       `json:"source_commit"`
		CaseCount    int          `json:"case_count"`
		Cases        []sourceCase `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(sourceLeaves))
	decoder.UseNumber()
	if err := decoder.Decode(&oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || oracle.CaseCount != 34 || len(oracle.Cases) != 34 {
		t.Fatal("unexpected source set")
	}
	for _, c := range oracle.Cases {
		t.Run(c.ID, func(t *testing.T) {
			args := Arguments(decodeOwned(t, c.Input.Args).(map[string]any))
			if c.Input.Method == "delete_torrents" && args["delete_file"] != false {
				t.Fatal("source delete input is not exact false")
			}
			port := &recordingPort{t: t, method: c.Input.VendorMethod, reply: decodeOwned(t, c.Input.VendorReply), raises: c.Input.VendorRaises, trace: []any{}}
			var result any
			var err error
			var outcome map[string]any
			if c.Input.Kind == "qb" {
				q := QBittorrent{Config: QBConfig{c.Input.Instance["category"], c.Input.Instance["sequential"], c.Input.Instance["first_last_piece"]}, Log: port.log}
				if c.Input.ClientPresent {
					q.Client = recordingQB{port}
				}
				switch c.Input.Method {
				case "add_torrent":
					r, e := q.AddTorrent(context.Background(), args)
					err = e
					outcome = map[string]any{"kind": "return", "value": map[string]any{"kind": "tuple", "items": []any{r.Success, r.IDs}}, "python_truthy": r.SourceTruthy(), "python_type": "tuple"}
				case "start_torrents":
					result, err = q.StartTorrents(context.Background(), args["ids"])
				case "recheck_torrents":
					result, err = q.RecheckTorrents(context.Background(), args["ids"])
				case "delete_torrents":
					result, err = q.DeleteTorrentsKeepFiles(context.Background(), args["ids"])
				default:
					t.Fatal("unknown source method")
				}
			} else {
				tr := Transmission{Log: port.log}
				if c.Input.ClientPresent {
					tr.Client = recordingTR{port}
				}
				switch c.Input.Method {
				case "add_torrent":
					result, err = tr.AddTorrent(context.Background(), args)
				case "start_torrents":
					result, err = tr.StartTorrents(context.Background(), args["ids"])
				case "recheck_torrents":
					result, err = tr.RecheckTorrents(context.Background(), args["ids"])
				case "delete_torrents":
					result, err = tr.DeleteTorrentsKeepFiles(context.Background(), args["ids"])
				default:
					t.Fatal("unknown source method")
				}
			}
			if err != nil {
				t.Fatalf("unexpected host/boundary error on observed source domain: %v", err)
			}
			if outcome == nil {
				outcome = map[string]any{"kind": "return", "value": encodeObservation(result), "python_truthy": truth(result), "python_type": observedType(result)}
			}
			if c.Input.Kind == "tr" && c.Input.Method == "add_torrent" {
				outcome["same_recording_reply_identity"] = ordinaryIdentity(result, port.reply)
				returned := false
				for _, item := range port.trace {
					if item.(map[string]any)["event"] == "vendor_return" {
						returned = true
					}
				}
				outcome["vendor_return_observed"] = returned
			}
			sameJSON(t, "original source outcome", outcome, c.Outcome)
			sameJSON(t, "original ordered vendor/log trace", port.trace, c.Trace)
			if port.calls != c.VendorCalls {
				t.Fatalf("vendor calls %d want %d", port.calls, c.VendorCalls)
			}
		})
	}
}

func TestDownloaderLeafBoundaryErrors(t *testing.T) {
	// Small ordinary spellings exercise the non-expanding number guard. No
	// huge-exponent input or memory-exhaustion probe is executed.
	for _, n := range []json.Number{"0", "-0", "0.0e-9"} {
		if valid(n) != nil || truth(n) {
			t.Fatal("zero number truth differs", n)
		}
	}
	if n := json.Number("1.5e2"); valid(n) != nil || !truth(n) {
		t.Fatal("ordinary exponent truth differs", n)
	}
	if valid(json.Number("1/3")) == nil || valid(json.Number("null")) == nil {
		t.Fatal("non-number accepted as JSON number")
	}

	hostDenied := errors.New("owned host authority denied")
	port := &recordingPort{t: t, method: "torrents_add", hostError: hostDenied, trace: []any{}}
	q := QBittorrent{Client: recordingQB{port}, Config: DefaultQBConfig(), Log: port.log}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.AddTorrent(ctx, Arguments{"content": []byte("owned")}); !errors.Is(err, context.Canceled) || port.calls != 0 {
		t.Fatal("cancellation became source false", err, port.calls)
	}
	if _, err := q.AddTorrent(context.Background(), Arguments{"content": []byte("owned")}); !errors.Is(err, hostDenied) || port.calls != 1 || len(port.trace) != 1 {
		t.Fatal("authority became source false/log", err, port.trace)
	}
	port.calls = 0
	port.trace = []any{}
	for _, args := range []Arguments{{"content": "https://owned.invalid/a", "cookie": "owned=synthetic"}, {"content": []byte("owned"), "urls": "owned-duplicate"}} {
		_, err := q.AddTorrent(context.Background(), args)
		var boundary *BoundaryError
		if !errors.As(err, &boundary) || port.calls != 0 || len(port.trace) != 0 {
			t.Fatal("pending branch became vendor success", err, port.trace)
		}
	}
	trPort := &recordingPort{t: t, method: "start_torrent", hostError: hostDenied, trace: []any{}}
	tr := Transmission{Client: recordingTR{trPort}, Log: trPort.log}
	if _, err := tr.StartTorrents(context.Background(), []any{"Owned-A"}); !errors.Is(err, hostDenied) || trPort.calls != 1 || len(trPort.trace) != 1 {
		t.Fatal("TR authority became source false", err, trPort.trace)
	}
}
