package larkmessager

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type directoryAlgorithmHost struct {
	ids     Object
	failure string
	calls   []any
}

func (h *directoryAlgorithmHost) API(_ context.Context, r Request) (Response, error) {
	if r.Op != "user_batch" {
		return Response{}, ErrDenied
	}
	h.calls = append(h.calls, clone(r.Payload))
	if h.failure == "all" || h.failure == "first" && len(h.calls) == 1 {
		return Response{}, errors.New("synthetic directory failure")
	}
	items := []any{}
	for _, key := range []string{"emails", "mobiles", "employee_ids"} {
		for _, id := range toStrings(r.Payload[key]) {
			if value, present := h.ids[id]; present {
				field := map[string]string{"emails": "email", "mobiles": "mobile", "employee_ids": "employee_id"}[key]
				items = append(items, Object{field: id, "user_id": value})
			}
		}
	}
	return Response{Status: 200, Body: uuidJSON(Object{"code": 0, "data": Object{"user_list": items}})}, nil
}
func (*directoryAlgorithmHost) ReadFile(context.Context, string) (Blob, error) {
	return Blob{}, ErrDenied
}
func (*directoryAlgorithmHost) FetchImage(context.Context, string) (Blob, error) {
	return Blob{}, ErrDenied
}

func adminReferenceResult(input Object) (any, error) {
	h := &directoryAlgorithmHost{ids: obj(input["identities"]), failure: text(input["failure"]), calls: []any{}}
	saved, sleeps := []any{}, []any{}
	configs := []any{Object{"name": "Other", "config": Object{"n": json.Number("9007199254740993")}}, Object{"name": "Lark", "config": Object{"keep": true}}}
	p := New(PluginHost{API: h, Sleep: func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, int(d.Seconds())); return nil }, UpdateNotifications: func(_ context.Context, _ uint64, fn func(any) (any, bool, error)) error {
		v, changed, e := fn(configs)
		if changed && e == nil {
			saved = append(saved, clone(v))
		}
		return e
	}})
	epoch, e := p.Init(obj(input["config"]))
	if e != nil {
		return nil, e
	}
	var result any
	switch input["method"] {
	case "resolve":
		ids, user := p.ResolveAdmins(context.Background())
		result = []any{ids, user}
	case "worker":
		e = p.SyncAdmins(context.Background(), epoch)
	case "admin_cache":
		values := []any{}
		for _, id := range []string{" ou_a ", "ou_b", "ou_unknown", "ou_unknown"} {
			ok, err := p.IsAdmin(context.Background(), id)
			if err != nil {
				return nil, err
			}
			values = append(values, ok)
		}
		result = values
	}
	return Object{"result": result, "calls": h.calls, "saved": saved, "sleeps": sleeps, "user_id": p.ConfigTrusted().UserID, "resolved": clone(p.resolved)}, e
}

type directoryPermissionHost struct{ failure string }

func (h *directoryPermissionHost) API(context.Context, Request) (Response, error) {
	return Response{Status: 200, Body: uuidJSON(Object{"code": 999, "msg": h.failure})}, nil
}
func (*directoryPermissionHost) ReadFile(context.Context, string) (Blob, error) {
	return Blob{}, ErrDenied
}
func (*directoryPermissionHost) FetchImage(context.Context, string) (Blob, error) {
	return Blob{}, ErrDenied
}
