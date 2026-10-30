package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestSourcePackageReaderFiniteBandsAndTrustedFallback(t *testing.T) {
	req, _, index, _ := fixture(t, []byte("owned-inert"), "executable")
	var mode, requests, loaderCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/Owned/Repo/main/go-plugins.v1.json":
			switch mode.Load() {
			case 0:
				_ = json.NewEncoder(w).Encode(index)
			case 1:
				http.NotFound(w, r)
			case 2:
				http.Error(w, "owned failure", 500)
			case 3:
				_, _ = w.Write([]byte(`{broken`))
			}
		case "/Owned/Repo/main/package.v2.json":
			_, _ = w.Write([]byte(`{"OwnedHigher":{"name":"Owned","version":"1.0"}}`))
		case "/Owned/Repo/main/package.json":
			_, _ = w.Write([]byte(`{"OwnedBase":{"name":"Owned","version":"1.0","v2":true}}`))
		default:
			t.Error("unexpected source package URL", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	local, _ := url.Parse(peer.URL)
	client := &http.Client{Transport: repositoryReplayTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "raw.githubusercontent.com" || r.Method != http.MethodGet {
			return nil, errors.New("nonowned destination")
		}
		copy := r.Clone(r.Context())
		address := *r.URL
		address.Scheme, address.Host = local.Scheme, local.Host
		copy.URL, copy.Host = &address, address.Host
		return peer.Client().Transport.RoundTrip(copy)
	})}
	const repository = "https://github.com/Owned/Repo"
	for _, version := range []string{"", "v2"} {
		values, err := DiscoverGitHubSourcePackage(context.Background(), repository, version, client)
		if err != nil || len(values) != 1 || values[0].SourcePackageVersion != version || values[0].UnavailableReason == "" || values[0].Current.Manifest.Runtime != "python" {
			t.Fatal("source band metadata/provenance changed", values, err)
		}
	}
	before := requests.Load()
	for _, version := range []string{"v3", "../v2", "v2.json", "https://example.invalid"} {
		if _, err := DiscoverGitHubSourcePackage(context.Background(), repository, version, client); err == nil {
			t.Fatal("nonfinite source package version", version)
		}
	}
	if requests.Load() != before {
		t.Fatal("invalid source package selector reached HTTP")
	}
	loaderError := errors.New("owned partial result")
	loader := func(ctx context.Context, source string, actual *http.Client) ([]Discovery, error) {
		loaderCalls.Add(1)
		if source != repository || actual != client {
			t.Fatal("trusted loader lost configured source/client")
		}
		return []Discovery{{PluginID: "OwnedPartial"}}, loaderError
	}
	for _, value := range []int32{0, 1, 2, 3} {
		mode.Store(value)
		before := loaderCalls.Load()
		rows, err := DiscoverRepositoryWithSourcePackages(context.Background(), repository, req.Platform, req.Validate, client, loader)
		if value == 1 {
			if !errors.Is(err, loaderError) || len(rows) != 1 || rows[0].PluginID != "OwnedPartial" || loaderCalls.Load() != before+1 {
				t.Fatal("valid native404 fallback lost partial/error", rows, err)
			}
		} else if loaderCalls.Load() != before || value == 0 && err != nil || value != 0 && err == nil {
			t.Fatal("source loader bypassed native priority/validation", value, rows, err)
		}
	}
}
