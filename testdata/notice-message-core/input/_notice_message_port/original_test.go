package notice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"testing"
	"time"
)

func oracle(t *testing.T) Object {
	t.Helper()
	f, err := os.Open("reference/installed-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.UseNumber()
	var out Object
	if err := d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
func equal(t *testing.T, got, want any) {
	t.Helper()
	a, e := json.Marshal(owned(got))
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(owned(want))
	if e != nil {
		t.Fatal(e)
	}
	if string(a) != string(b) {
		t.Fatalf("got %s\nwant %s", a, b)
	}
}
func obj(v any) Object { return v.(map[string]any) }
func arr(v any) []any  { return v.([]any) }
func num(v any) int    { n, _ := v.(json.Number).Int64(); return int(n) }
func periodList(v any) []Period {
	a := arr(v)
	out := make([]Period, len(a))
	for i, v := range a {
		for j, n := range arr(v) {
			out[i][j], _ = new(big.Int).SetString(string(n.(json.Number)), 10)
		}
	}
	return out
}
func periodJSON(v []Period) []any {
	out := []any{}
	for _, p := range v {
		row := []any{}
		for _, n := range p {
			row = append(row, json.Number(n.String()))
		}
		out = append(out, row)
	}
	return out
}
func clock(h, m int) time.Time { return time.Date(2026, 1, 2, h, m, 0, 0, time.UTC) }
func boolValue(v any) bool     { b, _ := v.(bool); return b }
func callJSON(c Call) Object   { return Object{"args": c.Args, "kwargs": c.Kwargs} }
func callsJSON(cs []Call) []any {
	out := []any{}
	for _, c := range cs {
		out = append(out, callJSON(c))
	}
	return out
}

func TestInstalledNotificationPydanticJSONOracle(t *testing.T) {
	for _, v := range arr(oracle(t)["envelopes"]) {
		c := obj(v)
		name := c["name"].(string)
		if len(name) > 100 {
			name = name[:100]
		}
		t.Run(name, func(t *testing.T) {
			e, err := NewEnvelope(obj(c["input"]))
			if c["errors"] != nil {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected validation error, got %v", err)
				}
				equal(t, ve.Issues, c["errors"])
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			equal(t, e.Dump(), c["value"])
			dump := e.Dump()
			dump["title"] = "changed"
			if e.Get("title") == "changed" {
				t.Fatal("dump aliases envelope")
			}
		})
	}
}
func TestInstalledScheduleAndWindowOracle(t *testing.T) {
	o := oracle(t)
	for i, v := range arr(o["periods"]) {
		t.Run(fmt.Sprint("parse-", i), func(t *testing.T) {
			c := obj(v)
			p, err := ParseSchedule(c["input"])
			if c["error"] != nil {
				var se SourceError
				if !errors.As(err, &se) || se.Class != c["error"] {
					t.Fatalf("got %v want %v", err, c["error"])
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			equal(t, periodJSON(p), c["value"])
		})
	}
	for i, v := range arr(o["windows"]) {
		t.Run(fmt.Sprint("window-", i), func(t *testing.T) {
			c := obj(v)
			equal(t, Allowed(periodList(c["periods"]), clock(num(c["hour"]), num(c["minute"]))), c["allowed"])
		})
	}
}
func TestInstalledQueueSendOracle(t *testing.T) {
	o := oracle(t)
	for i, v := range append(arr(o["queue"]), arr(o["async_queue"])...) {
		c := obj(v)
		t.Run(c["name"].(string), func(t *testing.T) {
			calls := []Call{}
			now := arr(c["now"])
			q, err := NewQueue(periodList(c["periods"]), func() time.Time { return clock(num(now[0]), num(now[1])) }, nil, func(_ context.Context, call Call) error {
				calls = append(calls, call)
				if boolValue(c["callback_error"]) {
					return errors.New("synthetic")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			send := q.Send
			if i >= len(arr(o["queue"])) {
				send = q.AsyncSend
			}
			result, err := send(context.Background(), Call{[]any{"post_message"}, Object{"message": "owned-message", "immediately": c["immediately"], "extra": "retained"}})
			if err != nil {
				t.Fatal(err)
			}
			if (result.CallbackError != nil) != boolValue(c["callback_error"]) {
				t.Fatal("callback error not observed as attempt")
			}
			equal(t, callsJSON(calls), c["calls"])
			equal(t, callsJSON(q.Pending()), c["pending"])
			if result.Queued && result.Attempted {
				t.Fatal("queued message reported attempted")
			}
		})
	}
}
func TestInstalledMonitorOracle(t *testing.T) {
	for _, v := range arr(oracle(t)["monitor"]) {
		c := obj(v)
		t.Run(c["name"].(string), func(t *testing.T) {
			calls := []Call{}
			at := clock(23, 0)
			rows := arr(c["clock"])
			index := -1
			now := func() time.Time {
				if index < 0 {
					return at
				}
				row := arr(rows[min(index, len(rows)-1)])
				index++
				return clock(num(row[0]), num(row[1]))
			}
			var callback SendCallback
			if !boolValue(c["no_callback"]) {
				callback = func(_ context.Context, call Call) error {
					calls = append(calls, call)
					if boolValue(c["callback_error"]) {
						return errors.New("synthetic")
					}
					return nil
				}
			}
			q, err := NewQueue([]Period{{big.NewInt(9), big.NewInt(0), big.NewInt(18), big.NewInt(0)}}, now, func() bool { return boolValue(c["stopped"]) }, callback)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				_, err := q.Send(context.Background(), Call{[]any{"post_message"}, Object{"message": fmt.Sprint("owned-", i)}})
				if err != nil {
					t.Fatal(err)
				}
			}
			index = 0
			q.Tick(context.Background())
			equal(t, callsJSON(calls), c["calls"])
			equal(t, callsJSON(q.Pending()), c["pending"])
		})
	}
}

func TestInstalledChainRoutingOracle(t *testing.T) {
	o := oracle(t)
	for _, v := range append(arr(o["routes"]), arr(o["route_failures"])...) {
		c := obj(v)
		t.Run(c["name"].(string), func(t *testing.T) {
			trace := []any{}
			counts := map[string]int{}
			add := func(o Object) error {
				trace = append(trace, o)
				op := o["op"].(string)
				counts[op]++
				if c["failure"] == op || c["failure"] == fmt.Sprintf("%s:%d", op, counts[op]) {
					return SourceError{"RuntimeError"}
				}
				return nil
			}
			r := Router{Dependencies{Now: func() time.Time { return time.Date(2026, 1, 2, 12, 34, 56, 0, time.UTC) }, Superuser: "root", SaveHistory: func(_ context.Context, m Object) error { return add(Object{"op": "history", "message": m}) }, NotificationSwitch: func(_ context.Context, kind any) (any, error) {
				if err := add(Object{"op": "switch", "kind": kind}); err != nil {
					return nil, err
				}
				return c["switch"], nil
			}, UserSettings: func(_ context.Context, key any) (any, error) {
				if err := add(Object{"op": "user_settings", "key": key}); err != nil {
					return nil, err
				}
				s, _ := key.(string)
				return obj(c["users"])[s], nil
			}, NoticeMessage: func(_ context.Context, data Object) error {
				return add(Object{"op": "notice_event", "event": "notice.message", "data": data})
			}, QueueCall: func(_ context.Context, call Call) error {
				return add(Object{"op": "queue_call", "args": call.Args, "kwargs": call.Kwargs})
			}}}
			e, err := NewEnvelope(obj(c["input"]))
			if err != nil {
				t.Fatal(err)
			}
			kw, _ := c["kwargs"].(map[string]any)
			err = r.Post(context.Background(), e, Business{}, kw)
			if c["error"] != nil {
				var se SourceError
				if !errors.As(err, &se) || se.Class != c["error"] {
					t.Fatalf("got %v want %v", err, c["error"])
				}
			} else if err != nil {
				t.Fatal(err)
			}
			equal(t, trace, c["trace"])
			equal(t, e.Get("userid"), obj(c["input"])["userid"])
			if !reflect.DeepEqual(e.Get("targets"), obj(c["input"])["targets"]) {
				t.Fatal("routing changed caller envelope")
			}
		})
	}
}

func TestInstalledTimeToMinutesOracle(t *testing.T) {
	for _, v := range arr(oracle(t)["time_minutes"]) {
		c := obj(v)
		t.Run(c["input"].(string), func(t *testing.T) {
			n, err := TimeToMinutes(c["input"].(string))
			if c["error"] != nil {
				var se SourceError
				if !errors.As(err, &se) || se.Class != c["error"] {
					t.Fatalf("got %v want %v", err, c["error"])
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			equal(t, json.Number(n.String()), c["value"])
		})
	}
}

func TestInstalledPydanticIntegerLexicalOracle(t *testing.T) {
	for i, v := range arr(oracle(t)["coercions"]) {
		c := obj(v)
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			e, err := NewEnvelope(Object{c["field"].(string): c["input"]})
			if c["errors"] != nil {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected validation error, got %v", err)
				}
				equal(t, ve.Issues, c["errors"])
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			equal(t, e.Get(c["field"].(string)), c["value"])
		})
	}
}

func TestInstalledChainAndQueueCompositionOracle(t *testing.T) {
	for _, v := range arr(oracle(t)["integrated"]) {
		c := obj(v)
		t.Run(c["name"].(string), func(t *testing.T) {
			trace := []any{}
			at := clock(23, 0)
			q, err := NewQueue(periodList([]any{[]any{jsonInt(9), jsonInt(0), jsonInt(18), jsonInt(0)}}), func() time.Time { return at }, nil, func(_ context.Context, call Call) error {
				trace = append(trace, Object{"op": "sender_attempt", "args": call.Args, "kwargs": call.Kwargs})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			r := Router{Dependencies{Now: func() time.Time { return at }, Superuser: "root", SaveHistory: func(_ context.Context, m Object) error {
				trace = append(trace, Object{"op": "history", "message": m})
				return nil
			}, NotificationSwitch: func(_ context.Context, kind any) (any, error) {
				trace = append(trace, Object{"op": "switch", "kind": kind})
				return "admin", nil
			}, UserSettings: func(_ context.Context, key any) (any, error) {
				trace = append(trace, Object{"op": "user_settings", "key": key})
				return Object{}, nil
			}, NoticeMessage: func(_ context.Context, data Object) error {
				trace = append(trace, Object{"op": "notice_event", "event": "notice.message", "data": data})
				return nil
			}, QueueCall: func(ctx context.Context, call Call) error { _, err := q.Send(ctx, call); return err }}}
			e, err := NewEnvelope(obj(c["input"]))
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Post(context.Background(), e, Business{}, nil); err != nil {
				t.Fatal(err)
			}
			equal(t, trace, c["before"])
			equal(t, callsJSON(q.Pending()), c["pending"])
			at = clock(12, 0)
			q.Tick(context.Background())
			equal(t, trace, c["after"])
			equal(t, callsJSON(q.Pending()), c["remaining"])
		})
	}
}
