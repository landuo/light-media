package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func downloaderSDKPacket(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var packet map[string]json.RawMessage
	if !json.Valid(raw) || json.Unmarshal(raw, &packet) != nil {
		t.Fatal("invalid actual SDK packet", string(raw))
	}
	return packet
}
func downloaderSDKString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v string
	if json.Unmarshal(raw, &v) != nil {
		t.Fatal("expected actual SDK string", string(raw))
	}
	return v
}
func downloaderSDKEqual(t *testing.T, label string, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	gd := json.NewDecoder(bytes.NewReader(got))
	gd.UseNumber()
	wd := json.NewDecoder(strings.NewReader(want))
	wd.UseNumber()
	if gd.Decode(&g) != nil || wd.Decode(&w) != nil || !reflect.DeepEqual(g, w) {
		t.Fatalf("%s got %s want %s", label, got, want)
	}
}

func TestSharedDownloaderCommandsWireAndSourceValues(t *testing.T) {
	var caps []string
	id := " Owned-config-ID "
	host := func(_ context.Context, cap string, raw json.RawMessage) (json.RawMessage, error) {
		caps = append(caps, cap)
		p := downloaderSDKPacket(t, raw)
		if downloaderSDKString(t, p["op"]) != "invoke" || downloaderSDKString(t, p["downloader_id"]) != id || len(p) != 3 {
			t.Fatal("SDK changed binding/invoke shape", string(raw))
		}
		switch cap {
		case QBittorrentAddCapability:
			downloaderSDKEqual(t, "qB original args", p["args"], `{"content":null,"is_paused":null,"tag":[],"is_skip_checking":true}`)
			return json.RawMessage(`{"success":false,"ids":[]}`), nil
		case TransmissionAddCapability:
			downloaderSDKEqual(t, "TR original args", p["args"], `{"content":"","labels":[],"download_dir":"","is_paused":null}`)
			return json.RawMessage(`{"value":{"id":9007199254740993,"hashString":"Owned-TR","opaque":"\ud800"}}`), nil
		default:
			if _, bad := p["args"]; bad {
				t.Fatal("control acquired args")
			}
			if _, bad := p["delete_file"]; bad {
				t.Fatal("delete-file boolean exposed")
			}
			return json.RawMessage(`{"value":false}`), nil
		}
	}
	q := SharedQBittorrentCommands{Host: host, DownloaderID: id}
	tr := SharedTransmissionCommands{Host: host, DownloaderID: id}
	qResult, err := q.AddTorrent(context.Background(), DownloaderCommandArgs{"content": nil, "is_paused": nil, "tag": []string{}, "is_skip_checking": true})
	if err != nil || qResult.Success || qResult.IDs == nil || len(qResult.IDs) != 0 || !qResult.SourceTruthy() {
		t.Fatal("source false tuple was coerced", qResult, err)
	}
	trResult, err := tr.AddTorrent(context.Background(), DownloaderCommandArgs{"content": "", "labels": []string{}, "download_dir": "", "is_paused": nil})
	if err != nil || string(trResult.Value) != `{"id":9007199254740993,"hashString":"Owned-TR","opaque":"\ud800"}` {
		t.Fatal("TR raw value changed or fake fields supplied", string(trResult.Value), err)
	}
	controls := []struct {
		cap  string
		call func(context.Context, any) (bool, error)
		ids  any
		want string
	}{
		{QBittorrentStartCapability, q.StartTorrents, nil, `null`},
		{QBittorrentRecheckCapability, q.RecheckTorrents, []string{}, `[]`},
		{QBittorrentDeleteKeepFilesCapability, q.DeleteTorrentsKeepFiles, "Exact Case", `"Exact Case"`},
		{TransmissionStartCapability, tr.StartTorrents, json.Number("9007199254740993"), `9007199254740993`},
		{TransmissionRecheckCapability, tr.RecheckTorrents, []any{json.Number("7"), "Owned"}, `[7,"Owned"]`},
		{TransmissionDeleteKeepFilesCapability, tr.DeleteTorrentsKeepFiles, nil, `null`},
	}
	for _, c := range controls {
		before := len(caps)
		// Same public method, observing its exact required ids field.
		inspect := func(ctx context.Context, cap string, raw json.RawMessage) (json.RawMessage, error) {
			p := downloaderSDKPacket(t, raw)
			downloaderSDKEqual(t, "source ids", p["ids"], c.want)
			return host(ctx, cap, raw)
		}
		qq := SharedQBittorrentCommands{Host: inspect, DownloaderID: id}
		tt := SharedTransmissionCommands{Host: inspect, DownloaderID: id}
		var got bool
		switch c.cap {
		case QBittorrentStartCapability:
			got, err = qq.StartTorrents(context.Background(), c.ids)
		case QBittorrentRecheckCapability:
			got, err = qq.RecheckTorrents(context.Background(), c.ids)
		case QBittorrentDeleteKeepFilesCapability:
			got, err = qq.DeleteTorrentsKeepFiles(context.Background(), c.ids)
		case TransmissionStartCapability:
			got, err = tt.StartTorrents(context.Background(), c.ids)
		case TransmissionRecheckCapability:
			got, err = tt.RecheckTorrents(context.Background(), c.ids)
		case TransmissionDeleteKeepFilesCapability:
			got, err = tt.DeleteTorrentsKeepFiles(context.Background(), c.ids)
		}
		if err != nil || got || len(caps) != before+1 || caps[before] != c.cap {
			t.Fatal("fixed control/value changed", c.cap, got, err, caps)
		}
	}
	if len(caps) != 8 {
		t.Fatal("not exactly eight public operations", caps)
	}
	// Source None is present raw JSON, not a missing result or a fake Torrent.
	tr.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":null}`), nil
	}
	trResult, err = tr.AddTorrent(context.Background(), DownloaderCommandArgs{"content": nil})
	if err != nil || string(trResult.Value) != "null" {
		t.Fatal("TR None lost", trResult, err)
	}
}

func TestSharedDownloaderCommandsPagedAdd(t *testing.T) {
	for _, sample := range []struct {
		name, cap string
		size      int
	}{{"typed-nil-empty", QBittorrentAddCapability, 0}, {"two-pages", QBittorrentAddCapability, TorrentMaterialChunkBytes + 3}, {"full-existing-bound", TransmissionAddCapability, TorrentMaterialMaxBytes}} {
		t.Run(sample.name, func(t *testing.T) {
			var original []byte
			if sample.size > 0 {
				original = make([]byte, sample.size)
				for i := range original {
					original[i] = byte(i % 251)
				}
			}
			expected := append([]byte{}, original...)
			digest := sha256.Sum256(expected)
			handle := "u_" + strings.Repeat("a", 64)
			ops := []string{}
			received := []byte{}
			total := -1
			submitted := false
			host := func(_ context.Context, cap string, raw json.RawMessage) (json.RawMessage, error) {
				if cap != sample.cap {
					t.Fatal("capability changed between upload frames", cap)
				}
				p := downloaderSDKPacket(t, raw)
				op := downloaderSDKString(t, p["op"])
				ops = append(ops, op)
				switch op {
				case "upload_begin":
					if len(p) != 5 || len(raw) > downloaderCommandRequestMaxBytes || downloaderSDKString(t, p["downloader_id"]) != "Owned" {
						t.Fatal("bad begin", string(raw))
					}
					var args map[string]json.RawMessage
					if json.Unmarshal(p["args"], &args) != nil {
						t.Fatal("bad args")
					}
					if _, present := args["content"]; present {
						t.Fatal("bytes leaked into generic args")
					}
					downloaderSDKEqual(t, "typed args frozen", p["args"], `{"is_paused":null,"tag":[]}`)
					if json.Unmarshal(p["total"], &total) != nil || total != sample.size || downloaderSDKString(t, p["sha256"]) != hex.EncodeToString(digest[:]) {
						t.Fatal("digest/total changed")
					}
					// Mutation after begin must not change the frozen logical input.
					if len(original) > 0 {
						original[0] ^= 255
					}
					return json.RawMessage(`{"handle":"` + handle + `"}`), nil
				case "upload_append":
					var offset int
					var data []byte
					if len(p) != 4 || len(raw) > downloaderCommandAppendMaxBytes || downloaderSDKString(t, p["handle"]) != handle || json.Unmarshal(p["offset"], &offset) != nil || offset != len(received) || json.Unmarshal(p["data"], &data) != nil || len(data) != min(TorrentMaterialChunkBytes, total-len(received)) {
						t.Fatal("bad page")
					}
					received = append(received, data...)
					ack, _ := json.Marshal(map[string]any{"handle": handle, "offset": len(received)})
					return ack, nil
				case "upload_submit":
					if submitted || len(p) != 2 || downloaderSDKString(t, p["handle"]) != handle || !bytes.Equal(received, expected) {
						t.Fatal("submit repeated or partial/corrupt bytes")
					}
					submitted = true
					if sample.cap == QBittorrentAddCapability {
						return json.RawMessage(`{"success":false,"ids":[]}`), nil
					}
					return json.RawMessage(`{"value":{"hashString":"Owned"}}`), nil
				default:
					t.Fatal("unexpected lifecycle frame", op)
					return nil, nil
				}
			}
			args := DownloaderCommandArgs{"content": original, "is_paused": nil, "tag": []string{}}
			if sample.cap == QBittorrentAddCapability {
				r, e := (SharedQBittorrentCommands{host, "Owned"}).AddTorrent(context.Background(), args)
				if e != nil || r.Success || !r.SourceTruthy() {
					t.Fatal(r, e)
				}
			} else {
				r, e := (SharedTransmissionCommands{host, "Owned"}).AddTorrent(context.Background(), args)
				if e != nil || string(r.Value) != `{"hashString":"Owned"}` {
					t.Fatal(r, e)
				}
			}
			if !submitted || len(ops) != (sample.size+TorrentMaterialChunkBytes-1)/TorrentMaterialChunkBytes+2 {
				t.Fatal("page lifecycle count", ops)
			}
		})
	}
}

type downloaderSDKCustomString string

func (downloaderSDKCustomString) MarshalJSON() ([]byte, error) {
	panic("custom marshaler must not execute")
}

type downloaderSDKPointerString string

func (*downloaderSDKPointerString) MarshalJSON() ([]byte, error) {
	panic("addressable custom marshaler must not execute")
}

func TestSharedDownloaderCommandsStrictFailuresAndNoResubmit(t *testing.T) {
	invokeCalls := 0
	invokeErr := errors.New("owned invoke outcome unknown")
	_, invokeReturned := (SharedQBittorrentCommands{func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		invokeCalls++
		return nil, invokeErr
	}, "Owned"}).AddTorrent(context.Background(), DownloaderCommandArgs{"content": "magnet:?xt=urn:btih:Owned"})
	if !errors.Is(invokeReturned, invokeErr) || invokeCalls != 1 {
		t.Fatal("invoke error retried or swallowed", invokeReturned, invokeCalls)
	}

	for _, sample := range []struct {
		name, reply string
		tr, control bool
	}{
		{"duplicate", `{"success":true,"success":false,"ids":[]}`, false, false},
		{"escaped-duplicate", `{"success":true,"\u0073uccess":false,"ids":[]}`, false, false},
		{"unknown", `{"success":true,"ids":[],"effect":"ok"}`, false, false},
		{"null-success", `{"success":null,"ids":[]}`, false, false},
		{"null-ids", `{"success":true,"ids":null}`, false, false},
		{"null-id", `{"success":true,"ids":[null]}`, false, false},
		{"lossy-id", `{"success":true,"ids":["\ud800"]}`, false, false},
		{"trailing", `{"success":true,"ids":[]} {}`, false, false},
		{"case-alias", `{"Success":true,"ids":[]}`, false, false},
		{"missing-raw", `{}`, true, false},
		{"nested-duplicate", `{"value":{"x":1,"x":2}}`, true, false},
		{"nonbool-control", `{"value":1}`, false, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			calls := 0
			host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				calls++
				return json.RawMessage(sample.reply), nil
			}
			var err error
			if sample.control {
				_, err = (SharedQBittorrentCommands{host, "Owned"}).StartTorrents(context.Background(), nil)
			} else if sample.tr {
				_, err = (SharedTransmissionCommands{host, "Owned"}).AddTorrent(context.Background(), DownloaderCommandArgs{"content": nil})
			} else {
				_, err = (SharedQBittorrentCommands{host, "Owned"}).AddTorrent(context.Background(), DownloaderCommandArgs{"content": nil})
			}
			if err == nil || calls != 1 {
				t.Fatal("malformed reply accepted or retried", err, calls)
			}
		})
	}
	// Different escaped code-point keys remain distinct in opaque TR JSON;
	// the duplicate check must not merge both into replacement U+FFFD.
	raw := json.RawMessage(`{"value":{"\ud800":1,"\ud801":2}}`)
	r, e := (SharedTransmissionCommands{func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return raw, nil }, "Owned"}).AddTorrent(context.Background(), DownloaderCommandArgs{"content": nil})
	if e != nil || string(r.Value) != `{"\ud800":1,"\ud801":2}` {
		t.Fatal("raw distinct code points lost", string(r.Value), e)
	}

	for _, mode := range []string{"begin-error", "bad-begin", "late-cancel-begin", "append-error", "bad-ack", "wrong-offset", "submit-error", "bad-submit", "late-cancel-submit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			handle := "u_" + strings.Repeat("b", 64)
			ops := []string{}
			ownedErr := errors.New("owned uncertain/transport error")
			host := func(_ context.Context, _ string, raw json.RawMessage) (json.RawMessage, error) {
				p := downloaderSDKPacket(t, raw)
				op := downloaderSDKString(t, p["op"])
				ops = append(ops, op)
				switch op {
				case "upload_begin":
					if mode == "begin-error" {
						return nil, ownedErr
					}
					if mode == "bad-begin" {
						return json.RawMessage(`{"handle":"` + handle + `","unknown":1}`), nil
					}
					if mode == "late-cancel-begin" {
						cancel()
					}
					return json.RawMessage(`{"handle":"` + handle + `"}`), nil
				case "upload_append":
					if mode == "append-error" {
						return nil, ownedErr
					}
					if mode == "bad-ack" {
						return json.RawMessage(`{"handle":"` + handle + `","offset":null}`), nil
					}
					if mode == "wrong-offset" {
						return json.RawMessage(`{"handle":"` + handle + `","offset":0}`), nil
					}
					return json.RawMessage(`{"handle":"` + handle + `","offset":3}`), nil
				case "upload_submit":
					if mode == "submit-error" {
						return nil, ownedErr
					}
					if mode == "bad-submit" {
						return json.RawMessage(`{"success":true}`), nil
					}
					if mode == "late-cancel-submit" {
						cancel()
					}
					return json.RawMessage(`{"success":true,"ids":[]}`), nil
				case "upload_abort":
					return json.RawMessage(`{"aborted":true}`), nil
				}
				t.Fatal("unexpected op", op)
				return nil, nil
			}
			result, err := (SharedQBittorrentCommands{host, "Owned"}).AddTorrent(ctx, DownloaderCommandArgs{"content": []byte{1, 2, 3}})
			if err == nil || result.Success || result.IDs != nil {
				t.Fatal("failed effect returned source success/partial", result, err)
			}
			submit, abort := 0, 0
			for _, op := range ops {
				if op == "upload_submit" {
					submit++
				}
				if op == "upload_abort" {
					abort++
				}
			}
			switch mode {
			case "begin-error", "bad-begin":
				if len(ops) != 1 || submit != 0 || abort != 0 {
					t.Fatal("guessed token or repeated begin", ops)
				}
			case "late-cancel-begin", "append-error", "bad-ack", "wrong-offset":
				if submit != 0 || abort != 1 {
					t.Fatal("pre-submit failure not reclaimed exactly once", ops)
				}
			default:
				if submit != 1 || abort != 0 {
					t.Fatal("submit repeated or false rollback attempted", ops)
				}
			}
		})
	}
	calls := 0
	never := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("must not reach host")
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	badArgs := []DownloaderCommandArgs{
		{}, {"content": []byte{}, "tag": string([]byte{0xff})}, {"content": nil, "tag": downloaderSDKCustomString("owned")},
		{"content": nil, "tag": []downloaderSDKPointerString{"owned"}},
		{"content": nil, "tag": cyclic}, {"content": nil, "tag": []byte{1}},
		{"content": nil, "tag": json.RawMessage(`{"x":1,"x":2}`)},
		{"content": []byte{}, "category": strings.Repeat("x", downloaderCommandRequestMaxBytes)},
		{"content": make([]byte, TorrentMaterialMaxBytes+1)},
	}
	for _, args := range badArgs {
		if _, err := (SharedQBittorrentCommands{never, "Owned"}).AddTorrent(context.Background(), args); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (SharedQBittorrentCommands{never, "Owned"}).StartTorrents(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("invalid input/cancellation reached host", calls)
	}
}
