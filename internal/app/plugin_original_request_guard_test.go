package app

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestWorkGuardSiteRedirectRetainsSelectedPOSTFacts(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			a, _, p := ownedSiteHTTP(t)
			var hits, checks atomic.Int64
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			}))
			defer peer.Close()
			ownedSiteHTTPConfig(t, a, peer.URL, "", "")
			denied := bad("OWNED_REVOKED", "owned transient scope changed")
			scope := originalBaseCallScope{object: p, installation: p.installation, validate: func(context.Context) error {
				if checks.Add(1) == 3 {
					return denied
				}
				return nil
			}}
			raw, _ := json.Marshal(plugins.SiteHTTPRequestV1{ID: "owned-site", Path: "/first", Method: method, Body: []byte("owned")})
			out, e := a.originalHostSiteHTTPResource(context.Background(), scope, raw)
			if hits.Load() != 1 {
				t.Fatal("guard failure retried selected request", hits.Load())
			}
			if method == "GET" {
				if !errors.Is(e, denied) {
					t.Fatal("GET authority refusal lost", e, string(out))
				}
				return
			}
			var r plugins.SiteHTTPResultV1
			if e != nil || json.Unmarshal(out, &r) != nil || !r.ScopeChanged || r.RequestState != "unknown" {
				t.Fatal("POST unknown fact lost", e, string(out))
			}
			if checks.Load() != 4 {
				t.Fatal("guard should recover at last check", checks.Load())
			}
		})
	}
}
func TestWorkGuardOrdinarySiteAndOCRTransportErrorRemainNone(t *testing.T) {
	a, _, p := ownedSiteHTTP(t)
	var hits atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer peer.Close()
	ownedSiteHTTPConfig(t, a, peer.URL, "", "")
	scope := originalBaseCallScope{object: p, installation: p.installation, validate: func(context.Context) error { return nil }}
	raw, _ := json.Marshal(plugins.SiteHTTPRequestV1{ID: "owned-site", Path: "/drop", Method: "POST"})
	out, e := a.originalHostSiteHTTPResource(context.Background(), scope, raw)
	var r plugins.SiteHTTPResultV1
	if e != nil || json.Unmarshal(out, &r) != nil || r.Response != nil || r.ScopeChanged || r.RequestState != "unknown" || hits.Load() != 1 {
		t.Fatal("ordinary vendor error changed", e, string(out), hits.Load())
	}
	u, _ := url.Parse(peer.URL)
	ocr, e := originalOCRRequest(context.Background(), http.MethodPost, u, http.Header{}, []byte("owned"), func(context.Context) error { return nil }, 1024)
	if e != nil || ocr != nil || hits.Load() != 2 {
		t.Fatal("ordinary OCR vendor failure changed", ocr, e, hits.Load())
	}
}
func TestWorkGuardOCRRedirectAndUnwrap(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "")
	}
	var hits, checks atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
	}))
	defer peer.Close()
	u, _ := url.Parse(peer.URL)
	denied := bad("OWNED_REVOKED", "owned scope changed")
	response, e := originalOCRRequest(context.Background(), http.MethodPost, u, http.Header{}, []byte("owned"), func(context.Context) error {
		if checks.Add(1) == 3 {
			return denied
		}
		return nil
	}, 1024)
	if !errors.Is(e, denied) || response != nil || hits.Load() != 1 {
		t.Fatal("OCR redirect guard lost/retried", e, response, hits.Load())
	}
	marker := originalSiteRequestGuardFailure(denied)
	var original *APIError
	if !errors.Is(marker, denied) || !errors.As(marker, &original) || original.Code != "OWNED_REVOKED" {
		t.Fatal("private marker removed original classification")
	}
}
