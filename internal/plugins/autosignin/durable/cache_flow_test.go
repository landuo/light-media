package durable

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"

	source "light-media/internal/plugins/autosignin"
)

type knownCacheIOStore struct{ Journal }

func (s knownCacheIOStore) Apply(ctx context.Context, b Binding, e Effect) (Entry, error) {
	if e.Kind == "cache.write" {
		e.ErrorClass = "OSError"
		e.KnownError = "synthetic known cache IO failure"
	}
	return s.Journal.Apply(ctx, b, e)
}

type sequenceHTTP struct {
	rows  []map[string]any
	calls int
}

func (s *sequenceHTTP) Do(context.Context, source.PageRequest) (*source.PageResponse, error) {
	s.calls++
	if len(s.rows) == 0 {
		return nil, nil
	}
	row := s.rows[0]
	s.rows = s.rows[1:]
	text, _ := row["text"].(string)
	content := []byte(text)
	if value, ok := row["raw_hex"].(string); ok {
		content, _ = hex.DecodeString(value)
	}
	return &source.PageResponse{Status: 200, Text: text, Content: content}, nil
}

type sequencePublic struct{ target *sequenceHTTP }

func (s sequencePublic) Suggest(ctx context.Context, _ string) (*source.PageResponse, error) {
	return s.target.Do(ctx, source.PageRequest{})
}
func (s sequencePublic) Image(ctx context.Context, _ string) (*source.PageResponse, error) {
	return s.target.Do(ctx, source.PageRequest{})
}
func TestTjuptKnownCacheIOFailureKeepsOriginalSuccessButNeverClaimsCacheApplied(t *testing.T) {
	raw, err := readAutoSignInFixture("../tjupt-inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Cases []struct {
			Name  string
			Input struct {
				Responses []map[string]any
				Cache     string
			}
		}
	}
	if json.Unmarshal(raw, &ref) != nil {
		t.Fatal("tjupt synthetic input")
	}
	var rows []map[string]any
	initial := ""
	for _, c := range ref.Cases {
		if c.Name == "Tjupt-do-nonempty-cache-match-write" {
			rows = c.Input.Responses
			initial = c.Input.Cache
		}
	}
	if len(rows) != 5 {
		t.Fatal("source Tjupt fixture changed")
	}
	store, binding, _ := fixture(t)
	if err = store.update(context.Background(), func(s *State) error { s.Cache["tjupt"] = []byte(initial); return nil }); err != nil {
		t.Fatal(err)
	}
	target := &sequenceHTTP{rows: rows}
	c := controller(knownCacheIOStore{store}, binding, target)
	c.Dependencies.Factory = func(context.Context, source.Object) (BoundSite, error) {
		return BoundSite{Site: source.Object{"id": 1, "name": "Synthetic", "url": "https://tjupt.org/", "cookie": "synthetic-cookie=not-a-credential"}, Hooks: source.SiteVisitHooks{HTTP: target, Tjupt: &source.TjuptDependencies{Public: sequencePublic{target}}}, Revision: "owned-synthetic-tjupt-v1"}, nil
	}
	boot(t, c)
	if _, err = c.Execute(context.Background(), "tjupt", Command{Action: "service"}); err != nil {
		t.Fatal(err)
	}
	state, _ := store.State(context.Background(), binding)
	if state.Stats["tjupt.org"].Success != 1 || target.calls != 5 || string(state.Cache["tjupt"]) != initial {
		t.Fatal("source cache IO handling changed", target.calls, state.Stats)
	}
	found := false
	for _, entry := range state.Runs["tjupt"].Lanes["签到:0"] {
		if entry.Kind == "cache.write" {
			found = true
			if entry.Phase != "known_error" || entry.ErrorClass != "OSError" {
				t.Fatal("IO failure was reported applied", entry)
			}
		}
	}
	if !found {
		t.Fatal("cache write was never exercised")
	}
}
