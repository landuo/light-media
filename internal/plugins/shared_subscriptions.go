package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	SharedSubscriptionsGet          = "subscriptions.get"
	SharedSubscriptionsList         = "subscriptions.list"
	SharedSubscriptionsListByTMDBID = "subscriptions.list_by_tmdbid"
	SharedSubscriptionsUpdate       = "subscriptions.update"
)

// ID is the host's stable opaque subscription identity, not an ORM primary key.
// Row contains the complete source-shaped column projection. Revision is an
// optional host concurrency extension; omitting it preserves last-write wins.
type SharedSubscriptionV1 struct {
	ID       string          `json:"id"`
	Revision int             `json:"revision"`
	Row      json.RawMessage `json:"row"`
}
type SharedSubscriptionGetV1 struct {
	ID string `json:"id"`
}
type SharedSubscriptionListV1 struct {
	State json.RawMessage `json:"state,omitempty"`
}
type SharedSubscriptionTMDBV1 struct {
	TMDBID json.RawMessage `json:"tmdbid"`
	Season json.RawMessage `json:"season,omitempty"`
}
type SharedSubscriptionUpdateV1 struct {
	ID               string          `json:"id"`
	Patch            json.RawMessage `json:"patch"`
	ExpectedRevision *int            `json:"expected_revision,omitempty"`
}
type SharedSubscriptions struct{ Host HostCaller }

func (s SharedSubscriptions) call(ctx context.Context, method string, in, out any) error {
	if s.Host == nil {
		return errors.New("subscriptions require an active host call")
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return e
	}
	raw, e = s.Host(ctx, method, raw)
	if e != nil {
		return e
	}
	return json.Unmarshal(raw, out)
}
func (s SharedSubscriptions) Get(ctx context.Context, in SharedSubscriptionGetV1) (*SharedSubscriptionV1, error) {
	var out *SharedSubscriptionV1
	e := s.call(ctx, SharedSubscriptionsGet, in, &out)
	return out, e
}
func (s SharedSubscriptions) List(ctx context.Context, in SharedSubscriptionListV1) ([]SharedSubscriptionV1, error) {
	var out []SharedSubscriptionV1
	e := s.call(ctx, SharedSubscriptionsList, in, &out)
	return out, e
}
func (s SharedSubscriptions) ListByTMDBID(ctx context.Context, in SharedSubscriptionTMDBV1) ([]SharedSubscriptionV1, error) {
	var out []SharedSubscriptionV1
	e := s.call(ctx, SharedSubscriptionsListByTMDBID, in, &out)
	return out, e
}

// Update returns a fresh committed projection, not the expired Python ORM
// instance returned by SubscribeOper.update. Unknown transient ORM attributes are
// ignored in this persisted-row projection; id mutation is unsupported because
// identity is host-owned. No arbitrary SQL/config channel exists.
func (s SharedSubscriptions) Update(ctx context.Context, in SharedSubscriptionUpdateV1) (*SharedSubscriptionV1, error) {
	var out *SharedSubscriptionV1
	e := s.call(ctx, SharedSubscriptionsUpdate, in, &out)
	return out, e
}
