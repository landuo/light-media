package cleaninvalidseed

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestActualInstalledDownloaderWrapperReference(t *testing.T) {
	var corpus []struct {
		Input    map[string]any
		Expected any
	}
	decodeFile(t, "reference/wrapper-oracle.json", &corpus)
	for _, c := range corpus {
		t.Run(c.Input["id"].(string), func(t *testing.T) {
			trace := []any{}
			kind := c.Input["kind"].(string)
			d := NativeDownloader{Service: Service{Name: "synthetic", Kind: kind}, Call: func(_ context.Context, op string, args map[string]any) (json.RawMessage, error) {
				method := strings.Split(op, ".")[1]
				if labels, ok := args["labels"].([]any); ok {
					sort.SliceStable(labels, func(i, j int) bool {
						a, _ := json.Marshal(labels[i])
						b, _ := json.Marshal(labels[j])
						return string(a) < string(b)
					})
					args["labels"] = labels
				}
				trace = append(trace, map[string]any{"method": method, "args": args})
				if Truth(c.Input["throw"]) {
					return nil, errors.New("synthetic controlled transport failure")
				}
				return json.RawMessage(`null`), nil
			}}
			var result any
			var e error
			if c.Input["op"] == "delete" {
				result, e = d.Delete(context.Background(), c.Input["hash"], false)
			} else {
				tags := c.Input["tags"].([]any)
				result, e = d.SetTags(context.Background(), c.Input["hash"], tags)
			}
			got := canonical(map[string]any{"result": result, "error": ErrorType(e), "trace": trace})
			if !reflect.DeepEqual(got, c.Expected) {
				t.Fatal("native source wrapper differs", got, c.Expected)
			}
		})
	}
}
func TestLazyQBTrackerRPCReadCountAndScope(t *testing.T) {
	calls := []string{}
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	d := NativeDownloader{Service: Service{Name: "synthetic", Kind: "qbittorrent"}, Call: func(_ context.Context, op string, args map[string]any) (json.RawMessage, error) {
		calls = append(calls, op)
		switch op {
		case "qb.torrents_info":
			if len(args) != 0 {
				t.Fatal("unexpected list scope", args)
			}
			return json.RawMessage(`[{"name":"synthetic","hash":"` + hash + `","state":"uploading"}]`), nil
		case "qb.torrents_trackers":
			if !reflect.DeepEqual(args, map[string]any{"torrent_hash": hash}) {
				t.Fatal("wrong lazy tracker identity", args)
			}
			return json.RawMessage(`[{"url":"https://tracker.synthetic/a","status":2,"msg":"Success","tier":0}]`), nil
		}
		return nil, errors.New("ungranted operation")
	}}
	objects, flag, e := d.List(context.Background())
	if e != nil || flag || len(objects) != 1 {
		t.Fatal(e, flag)
	}
	m := New(nil)
	if got := m.TrackerInfo(objects[0], "qbittorrent"); len(got) != 1 {
		t.Fatal(got)
	}
	if len(calls) != 4 {
		t.Fatal("source hasattr + truth probe + assignment must make three lazy reads", calls)
	}
	if got := m.TrackerInfo(objects[0], "qbittorrent"); len(got) != 1 || len(calls) != 7 {
		t.Fatal("second phase must repeat native property reads", calls)
	}
}
func TestTransmissionSnapshotKeepsActualInstalledPropertyPresence(t *testing.T) {
	o, e := nativeTR(map[string]any{"id": json.Number("1"), "name": "synthetic", "hashString": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "downloadDir": "/synthetic", "totalSize": json.Number("18446744073709551615"), "error": json.Number("0"), "errorString": "", "trackerStats": []any{}, "trackers": []any{}, "labels": []any{}, "status": json.Number("6")})
	if e != nil {
		t.Fatal(e)
	}
	for _, k := range []string{"downloadDir", "errorString", "trackerStats", "trackerList", "size"} {
		if SafeHas(o, k) {
			t.Fatal("invented camelCase/source alias", k)
		}
	}
	for _, k := range []string{"download_dir", "error_string", "tracker_stats", "hashString", "total_size", "status"} {
		if !SafeHas(o, k) {
			t.Fatal("missing actual property", k)
		}
	}
	size, e := required(o, "total_size")
	if e != nil || size != json.Number("18446744073709551615") {
		t.Fatal("integer projected through float", size, e)
	}
	if v, _ := required(o, "status"); v != "seeding" {
		t.Fatal(v)
	}
}
