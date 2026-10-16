package cleaninvalidseed

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInstalledSafeAttributeReference(t *testing.T) {
	var corpus []struct {
		Input    map[string]any
		Expected any
	}
	decodeFile(t, "reference/safe-oracle.json", &corpus)
	for i, c := range corpus {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			seq := c.Input["sequence"].([]any)
			var reads atomic.Int64
			o := &Object{Accessor: func(k string) (any, bool, error) {
				i := int(reads.Add(1)) - 1
				if i >= len(seq) {
					i = len(seq) - 1
				}
				v := seq[i].(map[string]any)
				if kind, ok := v["error"].(string); ok {
					return nil, false, source(kind, "controlled getter")
				}
				return v["value"], true, nil
			}}
			var result any
			if c.Input["op"] == "has" {
				result = SafeHas(o, "test")
			} else {
				result = SafeGet(o, "test", "DEFAULT")
			}
			got := canonical(map[string]any{"result": result, "reads": reads.Load()})
			if !reflect.DeepEqual(got, c.Expected) {
				t.Fatal("safe attribute actual AST differs", got, c.Expected)
			}
		})
	}
}
func TestBlockingGetterTimeoutDoesNotHang(t *testing.T) {
	release := make(chan struct{})
	exited := make(chan struct{})
	var reads atomic.Int64
	o := &Object{Accessor: func(string) (any, bool, error) {
		if reads.Add(1) == 2 {
			defer close(exited)
		}
		<-release
		return 5, true, nil
	}}
	start := time.Now()
	if v := SafeGetTimeout(o, "test", "DEFAULT", 10*time.Millisecond); v != "DEFAULT" || time.Since(start) > time.Second {
		close(release)
		t.Fatal("blocking source getter ignored timeout", v)
	}
	close(release)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("owned timeout fixture goroutine did not finish")
	}
}
func TestUnboundedSizeAndOverflowIntegerFailure(t *testing.T) {
	h := newSyntheticHost(map[string]any{"files": []any{map[string]any{"path": "/owned", "kind": "dir"}, map[string]any{"path": "/owned/a", "kind": "file", "size": json.Number("9223372036854775807")}, map[string]any{"path": "/owned/b", "kind": "file", "size": json.Number("9223372036854775807")}}})
	m := New(h)
	size, e := m.Size("/owned")
	want, _ := new(big.Int).SetString("18446744073709551614", 10)
	if e != nil || size.Cmp(want) != 0 {
		t.Fatal("directory total overflowed", size, e)
	}
	if e = m.Init(context.Background(), map[string]any{"min_seeding_days": math.Inf(1)}); ErrorType(e) != "OverflowError" {
		t.Fatal("source only catches ValueError/TypeError, not float infinity", e)
	}
}

type loggingFixture struct {
	*syntheticHost
	lines []string
}

func (h *loggingFixture) Log(_ context.Context, level, text string) {
	h.lines = append(h.lines, level+":"+text)
}
func TestCoreResultsVisibleWithNotificationsDisabled(t *testing.T) {
	var corpus struct {
		Cases []struct {
			ID    string
			Input map[string]any
		}
	}
	decodeFile(t, "reference/oracle.json", &corpus)
	for _, c := range corpus.Cases {
		if c.ID != "detect_invalid" {
			continue
		}
		h := &loggingFixture{syntheticHost: newSyntheticHost(c.Input)}
		m := New(h)
		config := c.Input["config"].(map[string]any)
		config["notify"] = false
		if e := m.Init(context.Background(), config); e != nil {
			t.Fatal(e)
		}
		if e := m.Clean(context.Background()); e != nil {
			t.Fatal(e)
		}
		found := false
		for _, s := range h.lines {
			if strings.Contains(s, "检测到 1 个失效做种") {
				found = true
			}
		}
		if !found {
			t.Fatal("disabled notifications hid cleanup result", h.lines)
		}
		for _, v := range h.trace {
			if v["op"] == "notice" {
				t.Fatal("notify=false sent a message")
			}
		}
		return
	}
	t.Fatal("missing controlled source case")
}
