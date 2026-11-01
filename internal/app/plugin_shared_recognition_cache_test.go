package app

import (
	"context"
	"errors"
	"light-media/internal/catalog"
	"net/http"
	"testing"
	"time"
)

// 本根是实际 Go 缓存组件，不声称 recording source Cache 证明了真实TTL实现。
func TestOriginalRecognitionSnapshotCacheFreshAndIsolation(t *testing.T) {
	var cache originalRecognitionRequestCache
	clock := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	calls := 0
	revoked := false
	fail := false
	denied := errors.New("owned scope closed")
	failure := errors.New("owned read failed")
	at := func() time.Time { return clock }
	guard := func() error {
		if revoked {
			return denied
		}
		return nil
	}
	fetch := func() (*catalog.SourceRecognitionSnapshot, error) {
		calls++
		if fail {
			return nil, failure
		}
		return &catalog.SourceRecognitionSnapshot{Status: 404, Body: []byte("{\"success\":false,\"status_message\":\"owned\"}"), Headers: http.Header{"X-Owned": []string{"one"}}}, nil
	}
	load := func(fresh bool) (*catalog.SourceRecognitionSnapshot, error) {
		return cache.load(context.Background(), "owned-day-key", fresh, 2, time.Hour, at, guard, fetch)
	}
	first, err := load(false)
	if err != nil || first == nil || calls != 1 {
		t.Fatal(first, err, calls)
	}
	first.Body[0] = 'X'
	first.Headers.Set("X-Owned", "mutated")
	second, err := load(false)
	if err != nil || calls != 1 || second.Body[0] != '{' || second.Headers.Get("X-Owned") != "one" {
		t.Fatal("negative JSON cache hit or ownership changed")
	}
	if _, err = load(true); err != nil || calls != 2 {
		t.Fatal("fresh did not bypass read")
	}
	if _, err = load(false); err != nil || calls != 2 {
		t.Fatal("fresh success did not update cache")
	}
	fail = true
	if _, err = load(true); err != failure || calls != 3 {
		t.Fatal("fresh failure was swallowed")
	}
	if _, err = load(false); err != nil || calls != 3 {
		t.Fatal("failed fresh destroyed old snapshot")
	}
	revoked = true
	if _, err = load(false); err != denied || calls != 3 {
		t.Fatal("cached data bypassed current authority")
	}
	revoked = false
	fail = false
	clock = clock.Add(2 * time.Hour)
	if _, err = load(false); err != nil || calls != 4 {
		t.Fatal("expired snapshot remained valid")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = cache.load(ctx, "owned-day-key", false, 2, time.Hour, at, guard, fetch); err != context.Canceled || calls != 4 {
		t.Fatal("canceled context reached cache/fetch")
	}
}
