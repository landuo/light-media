package notice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInstalledSourceAndEnumsPinned(t *testing.T) {
	o := oracle(t)
	source := obj(obj(o["provenance"])["source_sha256"])
	names := map[string]string{"helper/message.py": "helper_message.py", "chain/__init__.py": "chain.py", "schemas/message.py": "schemas_message.py", "schemas/types.py": "schemas_types.py", "utils/identity.py": "identity.py"}
	for original, local := range names {
		b, err := os.ReadFile(originalFixture("reference/source/" + local))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(b)
		if hex.EncodeToString(hash[:]) != source[original] {
			t.Fatal("source bytes differ", original)
		}
	}
	for field, name := range map[string]string{"channel": "MessageChannel", "mtype": "NotificationType", "ctype": "ContentType"} {
		equal(t, enumValues[field], obj(o["enums"])[name])
	}
}

func TestQueueOwnedFIFOConcurrentSendAndTick(t *testing.T) {
	ctx := context.Background()
	var allowed atomic.Bool
	var calls sync.Mutex
	seen := map[int]bool{}
	q, err := NewQueue(periodList([]any{[]any{jsonInt(9), jsonInt(0), jsonInt(18), jsonInt(0)}}), func() time.Time {
		if allowed.Load() {
			return clock(12, 0)
		}
		return clock(23, 0)
	}, nil, func(_ context.Context, c Call) error {
		id := num(c.Kwargs["id"])
		calls.Lock()
		defer calls.Unlock()
		if seen[id] {
			t.Errorf("duplicate attempt %d", id)
		}
		seen[id] = true
		if id%5 == 0 {
			return errors.New("source consumes caught failure")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		call := Call{[]any{"post_message"}, Object{"id": i, "nested": Object{"title": "owned"}}}
		if _, err := q.Send(ctx, call); err != nil {
			t.Fatal(err)
		}
		call.Kwargs["id"] = -1
		obj(call.Kwargs["nested"])["title"] = "mutated"
	}
	pending := q.Pending()
	equal(t, obj(pending[0].Kwargs["nested"])["title"], "owned")
	pending[0].Kwargs["id"] = -2
	allowed.Store(true)
	var wg sync.WaitGroup
	for i := 64; i < 128; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := q.Send(ctx, Call{nil, Object{"id": i}}); err != nil {
				t.Error(err)
			}
			q.Tick(ctx)
		}(i)
	}
	wg.Wait()
	q.Tick(ctx)
	if len(q.Pending()) != 0 {
		t.Fatal("pending not drained")
	}
	calls.Lock()
	defer calls.Unlock()
	if len(seen) != 128 {
		t.Fatalf("attempts %d", len(seen))
	}
}

func TestQueueStopAndInvalidJSONHaveNoInventedDelivery(t *testing.T) {
	ctx := context.Background()
	var count int
	q, _ := NewQueue(nil, func() time.Time { return clock(12, 0) }, nil, func(context.Context, Call) error { count++; return nil })
	q.Stop()
	attempt, err := q.Send(ctx, Call{nil, nil})
	if err != nil || !attempt.Attempted || count != 1 {
		t.Fatal("explicit send incorrectly disabled by stop", err)
	}
	if rows := q.Tick(ctx); len(rows) != 0 {
		t.Fatal("stopped monitor ran")
	}
	if _, err := q.Send(ctx, Call{nil, Object{"bad": make(chan int)}}); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if count != 1 || len(q.Pending()) != 0 {
		t.Fatal("invalid request caused effects")
	}
	withoutSender, _ := NewQueue(nil, nil, nil, nil)
	a, err := withoutSender.Send(ctx, Call{})
	if err != nil || a.Attempted || a.Queued {
		t.Fatal("absent callback claimed attempt", a, err)
	}
}

func TestRouterTemplateAndDependencyBoundaries(t *testing.T) {
	ctx := context.Background()
	needs, _ := NewEnvelope(Object{"ctype": "organizeSuccess"})
	r := Router{}
	if !errors.Is(r.Post(ctx, needs, Business{}, nil), ErrTemplateDependency) {
		t.Fatal("template dependency falsely ready")
	}
	if err := r.Post(ctx, nil, Business{}, nil); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(r.Post(ctx, &Envelope{}, Business{}, nil), ErrInvalidEnvelope) {
		t.Fatal("zero envelope accepted")
	}
	e, _ := NewEnvelope(Object{"title": "owned", "save_history": false})
	if !errors.Is(r.Post(ctx, e, Business{}, nil), ErrDependency) {
		t.Fatal("missing sender treated successful")
	}
	var observed Object
	r.Render = func(_ context.Context, e *Envelope, _ Business, kw Object) (*Envelope, error) {
		observed = kw
		kw["mutable"] = "isolated"
		return nil, nil
	}
	if err := r.Post(ctx, e, Business{}, Object{"current_time": nil}); err != nil {
		t.Fatal(err)
	}
	if _, ok := observed["current_time"]; !ok || observed["current_time"] != nil {
		t.Fatal("explicit null time overwritten")
	}
}

func jsonInt(n int) json.Number { return json.Number(strconv.Itoa(n)) }
