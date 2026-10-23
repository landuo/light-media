package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

func sourceFailureClient(t *testing.T, kind, body string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	return &Client{config: Config{ID: "synthetic", Kind: kind, Cookie: "synthetic-cookie", APIKey: "synthetic-key"}, base: base, http: server.Client()}
}

func TestSourceUserModuleFailureOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/site-account-outcomes-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Kind, Body string
			ModuleErrors     int `json:"module_errors"`
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, item := range fixture.Cases {
		if item.Kind == "throws" || item.Kind == "unsupported" {
			continue
		} // No corresponding live Go dispatch.
		t.Run(item.Name, func(t *testing.T) {
			kind := item.Kind
			if kind == "nexus" {
				kind = "jiayuan"
			}
			c := sourceFailureClient(t, kind, item.Body)
			count := 0
			for call := 0; call < 2; call++ { // Oracle totals one batch call plus one API call.
				_, dto, err := c.SourceUserStats(context.Background(), UserStatsOptions{})
				got, classified := SourceUserModuleFailureKind(err)
				if classified {
					count++
					if dto != nil || got != item.Name || err.Error() != "site_user_source_module_failed" {
						t.Fatalf("invalid projection: %q, DTO present=%v, err=%v", got, dto != nil, err)
					}
				}
			}
			if count != item.ModuleErrors {
				t.Fatalf("got %d module failures across two calls, want %d", count, item.ModuleErrors)
			}
		})
	}
}

func TestSourceUserModuleFailureNegativeBoundaries(t *testing.T) {
	for _, item := range []struct{ name, body string }{
		{"int64-overflow-string", `{"code":"0","data":{"memberCount":{"uploaded":"9223372036854775808"}}}`},
		{"int64-overflow-number", `{"code":"0","data":{"memberCount":{"uploaded":9223372036854775808}}}`},
		{"exponent-overflow", `{"code":"0","data":{"memberCount":{"uploaded":1e999}}}`},
		{"conversion-limit", `{"code":"0","data":{"memberCount":{"uploaded":"` + strings.Repeat("1", 4301) + `"}}}`},
		{"json-depth-limit", strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)},
		{"other-schema", `{"code":"0","data":[]}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			_, dto, err := sourceFailureClient(t, "mteam", item.body).SourceUserStats(context.Background(), UserStatsOptions{})
			if dto != nil || err == nil {
				t.Fatal("expected existing unprojected parser/native failure")
			}
			if kind, ok := SourceUserModuleFailureKind(err); ok {
				t.Fatalf("unexpected projection %q", kind)
			}
		})
	}
	var cold SourceUserData
	coldErr := json.Unmarshal([]byte(`{"upload":"not-an-integer"}`), &cold)
	_, _, marker := sourceMTeamRoot([]byte("{"))
	for _, err := range []error{nil, errors.New("site_user_source_module_failed"), coldErr, marker} {
		if _, ok := SourceUserModuleFailureKind(err); ok {
			t.Fatalf("untrusted/cold error projected: %T", err)
		}
	}
	if coldErr == nil {
		t.Fatal("cold snapshot should fail decoding")
	}
}

func TestSourceUserModuleFailureRevocationProvenance(t *testing.T) {
	c := sourceFailureClient(t, "mteam", "{")
	_, _, previous := c.SourceUserStats(context.Background(), UserStatsOptions{})
	if _, ok := SourceUserModuleFailureKind(previous); !ok {
		t.Fatal("missing original failure")
	}
	if _, ok := SourceUserModuleFailureKind(fmt.Errorf("old: %w", previous)); ok {
		t.Fatal("arbitrary wrapping is not fresh provenance")
	}
	for _, revoked := range []error{previous, fmt.Errorf("old: %w", previous), errors.New("site_user_source_module_failed")} {
		c.SetRequestGuard(func(context.Context) error { return revoked })
		_, dto, err := c.SourceUserStats(context.Background(), UserStatsOptions{})
		if dto != nil || !errors.Is(err, revoked) {
			t.Fatal("guard denial was not preserved", err)
		}
		if _, ok := SourceUserModuleFailureKind(err); ok {
			t.Fatal("guard replay became a new module failure")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.SetRequestGuard(func(context.Context) error { cancel(); return previous })
	_, dto, err := c.SourceUserStats(ctx, UserStatsOptions{})
	if dto != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation did not take priority", err)
	}
	if _, ok := SourceUserModuleFailureKind(err); ok {
		t.Fatal("cancellation classified")
	}
	// A caller-supplied trace must not survive a new invocation.
	stale := &sourceUserParserFailure{kind: "mt-invalid-json"}
	c.SetRequestGuard(func(context.Context) error { return stale })
	ctx = context.WithValue(context.Background(), sourceUserStatsKey{}, &sourceUserStatsTrace{parserFailure: stale})
	_, _, err = c.SourceUserStats(ctx, UserStatsOptions{})
	if _, ok := SourceUserModuleFailureKind(err); ok {
		t.Fatal("foreign trace/parser marker classified")
	}
}

func TestSourceUserModuleFailureSwallowedAndSafetyErrors(t *testing.T) {
	for _, limit := range []bool{false, true} {
		t.Run(fmt.Sprintf("body-limit-%v", limit), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if limit {
					fmt.Fprint(w, strings.Repeat("x", int(htmlLimit)+1))
					return
				}
				if r.URL.Path == "/api/member/profile" {
					fmt.Fprint(w, `{"code":"0","data":{}}`)
					return
				}
				fmt.Fprint(w, "{") // JSON-body seeding reader swallows malformed JSON.
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			c := &Client{config: Config{ID: "synthetic", Kind: "mteam", APIKey: "synthetic-key"}, base: base, http: server.Client()}
			_, dto, err := c.SourceUserStats(context.Background(), UserStatsOptions{})
			if _, ok := SourceUserModuleFailureKind(err); ok {
				t.Fatal("swallowed/safety error projected")
			}
			if limit {
				if dto != nil || err == nil || err.Error() != "site_body_limit" {
					t.Fatal("lost body limit", err)
				}
			} else if dto == nil {
				t.Fatal("swallowed JSON error should keep DTO", err)
			}
		})
	}
}

func TestSourceUserModuleFailurePreservesHHSafetyError(t *testing.T) {
	for _, panel := range []string{"", `<div id="user-info-panel"><div></div><div><div><div><div>分享率: 1</div></div></div><div><div></div><div></div><div></div><div>上传: 1 GB</div><div>下载: 1 GB</div></div></div></div>`} {
		c := sourceFailureClient(t, "hhanclub", `<a href="logout.php">out</a><div> 上传量: 999999999999999999999 PB</div>`+panel)
		_, dto, err := c.SourceUserStats(context.Background(), UserStatsOptions{})
		if dto != nil || err == nil || err.Error() != "site_user_integer_limit" {
			t.Fatal("lost HH safety failure", err)
		}
		if _, ok := SourceUserModuleFailureKind(err); ok {
			t.Fatal("HH safety error became missing panel module error")
		}
	}
}

func TestSourceUserModuleFailureLongMalformedInteger(t *testing.T) {
	for _, value := range []string{strings.Repeat(" ", 4300) + "x", strings.Repeat("错", 1434)} {
		raw, _ := json.Marshal(value)
		c := sourceFailureClient(t, "mteam", `{"code":"0","data":{"memberCount":{"uploaded":`+string(raw)+`}}}`)
		_, dto, err := c.SourceUserStats(context.Background(), UserStatsOptions{})
		if kind, ok := SourceUserModuleFailureKind(err); !ok || kind != "mt-invalid-integer" || dto != nil {
			t.Fatal("format failure confused with digit bound", err)
		}
	}
}

func TestSourceUserModuleFailureConcurrentInvocationIsolation(t *testing.T) {
	type modeKey struct{}
	c := sourceFailureClient(t, "mteam", "{")
	_, _, previous := c.SourceUserStats(context.Background(), UserStatsOptions{})
	c.SetRequestGuard(func(ctx context.Context) error {
		if ctx.Value(modeKey{}) == "guard" {
			return previous
		}
		return nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		mode := []string{"parser", "guard", "cancel"}[i%3]
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.WithValue(context.Background(), modeKey{}, mode)
			if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, dto, err := c.SourceUserStats(ctx, UserStatsOptions{})
			kind, ok := SourceUserModuleFailureKind(err)
			if dto != nil {
				t.Errorf("%s unexpectedly returned DTO", mode)
			}
			switch mode {
			case "parser":
				if !ok || kind != "mt-invalid-json" || err == previous {
					t.Errorf("parser lost own failure: %v", err)
				}
			case "guard":
				if ok || !errors.Is(err, previous) {
					t.Errorf("guard inherited parser projection: %v", err)
				}
			case "cancel":
				if ok || !errors.Is(err, context.Canceled) {
					t.Errorf("cancel inherited parser projection: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}
