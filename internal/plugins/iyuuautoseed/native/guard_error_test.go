package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFindByTagVendorFailuresRetainNineSourceAttempts(t *testing.T) {
	reads, sleeps := 0, []time.Duration{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reads++; http.Error(w, "owned vendor failure", 500) }))
	defer peer.Close()
	c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{Sleep: func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hash, err := c.PluginHostFindByTag(context.Background(), []string{"owned"}, nil)
	if err != nil || hash != "" || reads != 9 || len(sleeps) != 9 {
		t.Fatal("ordinary source query-error retry changed", hash, err, reads, sleeps)
	}
	for _, d := range sleeps {
		if d != 3*time.Second {
			t.Fatal("source sleep value changed", sleeps)
		}
	}
}
func TestFindByTagTransientGuardCannotResumeAsVendorFailure(t *testing.T) {
	for _, stage := range []string{"query", "version", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			var calls, network, sleeps atomic.Int32
			sentinel := errors.New("OWNED_PRIVATE_GUARD_CAUSE")
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				network.Add(1)
				if r.URL.Path == "/api/v2/torrents/info" {
					io.WriteString(w, `[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tags":"owned"}]`)
					return
				}
				t.Error("denied cleanup or version reached wire", r.URL.Path)
			}))
			defer peer.Close()
			denyAt := int32(2)
			if stage == "cleanup" {
				denyAt = 3
			}
			c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{Sleep: func(context.Context, time.Duration) error { sleeps.Add(1); return nil }, Guard: func(context.Context) error {
				if calls.Add(1) == denyAt {
					return sentinel
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			var status *string
			if stage == "version" {
				v := "paused"
				status = &v
			}
			hash, err := c.PluginHostFindByTag(context.Background(), []string{"owned"}, status)
			wantNetwork := int32(0)
			if stage == "cleanup" {
				wantNetwork = 1
			}
			if hash != "" || !errors.Is(err, sentinel) || sleeps.Load() != 1 || network.Load() != wantNetwork {
				t.Fatal("transient guard became retry/observed result", hash, err, sleeps.Load(), network.Load())
			}
			if strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("retained guard cause leaked")
			}
		})
	}
}

func TestCookieSyncTransientGuardStopsSourceFallbackDispatch(t *testing.T) {
	for _, denyAt := range []int32{1, 2, 3} {
		t.Run(fmt.Sprint(denyAt), func(t *testing.T) {
			var checks, wire, writes atomic.Int32
			sentinel := errors.New("OWNED_PRIVATE_COOKIE_GUARD")
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire.Add(1)
				switch r.URL.Path {
				case "/api/v2/app/webapiVersion":
					io.WriteString(w, "2.11.4")
				case "/api/v2/app/cookies":
					io.WriteString(w, `[]`)
				default:
					writes.Add(1)
					t.Error("guard failure fell through to a selected write", r.URL.Path)
				}
			}))
			defer peer.Close()
			c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{Guard: func(context.Context) error {
				if checks.Add(1) == denyAt {
					return sentinel
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			address, cookie := "https://owned.invalid/material", "owned=synthetic"
			out, err := c.PluginHostAddMaterial(context.Background(), IYUUAddSpec{}, true, nil, nil, &address, &cookie)
			if !errors.Is(err, sentinel) || out.Accepted || out.Outcome != "rejected" || !MutationNotDispatched(err) || MutationFailureClass(err) != "owner_denied" || wire.Load() != denyAt-1 || writes.Load() != 0 || checks.Load() != denyAt {
				t.Fatal("cookie helper swallowed a transient trusted guard", out, err, checks.Load(), wire.Load(), writes.Load())
			}
		})
	}
}
