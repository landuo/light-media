package brushflow

import (
	"context"
	"errors"
	"testing"
)

type originalRetainedFailureHost struct {
	*memoryHost
	failSave bool
}

func (h *originalRetainedFailureHost) SaveConfig(ctx context.Context, r Row) error {
	if h.failSave {
		return errors.New("owned fixture save failure")
	}
	return h.memoryHost.SaveConfig(ctx, r)
}
func TestOriginalRetainedInitKeepsPostNormalizePartialObject(t *testing.T) {
	h := &originalRetainedFailureHost{memoryHost: testHost()}
	p := NewUninitialized(h)
	if err := p.Init(context.Background(), Row{"enabled": false, "tasks": []any{}}); err != nil {
		t.Fatal(err)
	}
	h.failSave = true
	if err := p.Init(context.Background(), Row{"enabled": true, "tasks": []any{}}); err == nil {
		t.Fatal("expected owned save failure")
	}
	if !p.Enabled() {
		t.Fatal("member mutation before save failure was lost")
	}
	if len(h.queued) != 0 {
		t.Fatal("failed save continued into queue effects")
	}
}
func TestOriginalRetainedInitAndLegacyNewShareImplementation(t *testing.T) {
	h := testHost()
	p := NewUninitialized(h)
	raw := Row{"enabled": true, "tasks": []any{Row{"id": "owned", "enabled": true, "site_id": 1, "downloader": "qb", "cron": "bad", "del_no_free": true}}}
	if err := p.Init(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	s := p.Services(context.Background())
	if len(s) != 3 || s[0].Cron != "" || s[1].Minutes != 15 || s[2].Minutes != 1 {
		t.Fatalf("services=%+v", s)
	}
	q, err := New(context.Background(), testHost(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Services(context.Background())) != len(s) {
		t.Fatal("legacy wrapper changed services")
	}
}
