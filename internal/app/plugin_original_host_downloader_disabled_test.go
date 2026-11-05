package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func TestHostDownloaderDisabledMutationsSourceAndMidcallRevocation(t *testing.T) {
	var source struct {
		SourceCommit string `json:"source_commit"`
		Disabled     bool   `json:"explicit_disabled_service"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-downloader-20261031/disabled-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &source) != nil || source.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || source.Disabled {
		t.Fatal("fixed service-helper oracle unavailable", err)
	}
	a, _, object := hostDownloaderFixture(t)
	disabled := false
	var dispatch atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatch.Add(1)
		if r.URL.Path == "/api/v2/torrents/info" {
			workflowJSON(w, []any{workflowTask(ownedDownloaderHash, 1)})
			return
		}
		http.Error(w, "unexpected", 400)
	}))
	defer server.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, Enabled: &disabled}}
	requests := []struct {
		method string
		value  any
	}{
		{"add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", Magnet: "magnet:?xt=urn:btih:" + ownedDownloaderHash}},
		{"control", plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: "start"}},
		{"find_by_tag", plugins.DownloaderFindByTagRequestV1{DownloaderID: "owned", Tags: []string{"owned"}}},
		{"delete_tags", plugins.DownloaderDeleteTagsRequestV1{DownloaderID: "owned", Tags: []string{"owned"}}},
		{"delete_files", plugins.DownloaderDeleteFilesRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}}},
	}
	for _, request := range requests {
		if err := hostDownloaderCall(t, object, request.method, request.value, nil); err == nil {
			t.Fatal("disabled write allowed", request.method)
		}
	}
	requests = append(requests,
		struct {
			method string
			value  any
		}{"completed", plugins.DownloaderCompletedRequestV1{DownloaderID: "owned"}},
		struct {
			method string
			value  any
		}{"downloading", plugins.DownloaderCompletedRequestV1{DownloaderID: "owned"}},
		struct {
			method string
			value  any
		}{"list", plugins.DownloaderListRequestV1{DownloaderID: "owned"}},
		struct {
			method string
			value  any
		}{"tags", plugins.DownloaderTagsRequestV1{DownloaderID: "owned"}},
		struct {
			method string
			value  any
		}{"trackers", plugins.DownloaderTrackersRequestV1{DownloaderID: "owned", Hash: ownedDownloaderHash, ErrorMessages: []string{}}},
	)
	for _, request := range requests[5:] {
		if err := hostDownloaderCall(t, object, request.method, request.value, nil); err == nil {
			t.Fatal("disabled public read allowed", request.method)
		}
	}
	if dispatch.Load() != 0 {
		t.Fatal("disabled mutation contacted daemon", dispatch.Load())
	}
	// A live test scope proves enablement is checked again at the transport
	// boundary, rather than only when the client was constructed.
	enabled := true
	a.config.Downloaders[0].Enabled = &enabled
	scope := originalBaseCallScope{installation: object.installation, object: object, validate: func(context.Context) error { return nil }}
	client, _, guard, err := a.originalHostDownloaderMutationClient(context.Background(), scope, "owned")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	enabled = false
	before := dispatch.Load()
	if guard(context.Background()) == nil {
		t.Fatal("midcall disable ignored")
	}
	receipt, err := client.Start(context.Background(), []string{ownedDownloaderHash})
	if err == nil || receipt.Accepted || receipt.Outcome == "unknown" || dispatch.Load() != before {
		t.Fatal("revoked selected mutation dispatched", receipt, err, dispatch.Load(), before)
	}
}
