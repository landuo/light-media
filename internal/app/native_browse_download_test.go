package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercises the same POST shape used by SiteResourceDialog/AddDownloadDialog.
// Both tracker and downloader are local synthetic protocol servers.
func TestNativeBrowseDownloadRechecksOriginalPageAndPreservesDisabledSite(t *testing.T) {
	a, source, _ := importTestApp(t)
	torrent, hash := workflowTorrent("Synthetic.Recent.Movie.2026.mkv")
	var siteRequests []string
	listingTitle := "Synthetic.Recent.Movie.2026.1080p.WEB-DL"
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siteRequests = append(siteRequests, r.URL.Path)
		if r.Header.Get("Cookie") != "fixture-cookie" {
			t.Error("missing configured tracker authentication")
		}
		switch r.URL.Path {
		case "/torrents.php":
			if !r.URL.Query().Has("search") || r.URL.Query().Get("search") != "" || r.URL.Query().Get("page") != "1" {
				t.Error("download changed the explicit recent query/page or reused a stale cache hint")
			}
			io.WriteString(w, workflowHTML(listingTitle, "", false))
		case "/download.php":
			if r.URL.Query().Get("id") != "101" {
				t.Error("downloaded another resource")
			}
			_, _ = w.Write(torrent)
		default:
			t.Error("unexpected tracker endpoint")
			http.NotFound(w, r)
		}
	}))
	defer tracker.Close()
	added := false
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if added {
				task := workflowTask(hash, 0)
				task["save_path"] = source
				workflowJSON(w, []any{task})
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Error(err)
			}
			if r.FormValue("savepath") != source {
				t.Error("selected save directory changed")
			}
			added = true
			io.WriteString(w, "Ok.")
		default:
			t.Error("unexpected downloader endpoint")
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	workflowConfigure(t, a, tracker.URL, qb.URL)
	a.config.Sites[0].Enabled = false
	cookie := setupTestAdmin(t, a)
	// A previously cached media search for the same ID must not replace the
	// explicit browse page when the user selects the recent listing.
	if err := a.Store.Put(context.Background(), "resource", "fixture-site:101", map[string]any{"query": "unrelated cached query", "page": 9}); err != nil {
		t.Fatal(err)
	}
	listed := callTest(t, a.Handler(), "POST", "/api/sites/manage/fixture-site/resources", `{"query":"","page":2}`, cookie, "")
	var listing struct {
		Data SearchOutput `json:"data"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &listing) != nil || len(listing.Data.Candidates) != 1 || listing.Data.Candidates[0].Resource.ID != "101" {
		t.Fatal("recent browse did not return the selectable resource")
	}
	body, _ := json.Marshal(NativeDownloadRequest{SiteID: "fixture-site", ResourceID: "101", Browse: true, Page: 2, DownloaderID: "fixture-qb", DownloadPath: source})
	created := callTest(t, a.Handler(), "POST", "/api/downloads/add", string(body), cookie, "")
	var result struct {
		Data NativeDownload `json:"data"`
	}
	if created.Code != http.StatusOK || json.Unmarshal(created.Body.Bytes(), &result) != nil || result.Data.State != "planned" || result.Data.Hash != hash || result.Data.ResourceTitle != listingTitle || result.Data.DownloadPath != source {
		t.Fatal("recent resource was not queued with the verified ID/material/directory", created.Code, created.Body.String())
	}
	job, err := a.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.safeRun(context.Background(), job); err != nil || !added {
		t.Fatal("queued recent download was not accepted by the synthetic downloader", err)
	}
	if strings.Join(siteRequests, ",") != "/torrents.php,/torrents.php,/download.php" || a.Settings().Sites[0].Enabled {
		t.Fatal("browse skipped verification, changed site, or enabled background requests", siteRequests)
	}
}

func TestNativeBrowseDownloadRejectsChangedAndUnauthenticatedListing(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"missing-ID", strings.ReplaceAll(workflowHTML("Another.Movie.2026", "", false), "id=101", "id=102")},
		{"login", `<form><input type="password"></form>`},
		{"unsupported", `<a href="logout.php">Logout</a><p>Changed template</p>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			calls := 0
			tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/torrents.php" || r.URL.Query().Get("search") != "" || r.URL.Query().Get("page") != "2" {
					t.Error("unverified resource material was fetched or listing changed")
				}
				io.WriteString(w, tc.body)
			}))
			defer tracker.Close()
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("unverified resource reached downloader")
			}))
			defer qb.Close()
			workflowConfigure(t, a, tracker.URL, qb.URL)
			_, err := a.addNativeDownload(context.Background(), NativeDownloadRequest{SiteID: "fixture-site", ResourceID: "101", Browse: true, Page: 3, DownloaderID: "fixture-qb", DownloadPath: source})
			var queued int
			if err == nil || calls != 1 {
				t.Fatal("unverified recent resource was accepted", err, calls)
			}
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='native_download'`).Scan(&queued); err != nil || queued != 0 {
				t.Fatal("failed verification queued a download")
			}
		})
	}
}

func TestNativeDownloadLegacyQueryHintStillAppliesOutsideBrowse(t *testing.T) {
	a, source, _ := importTestApp(t)
	torrent, _ := workflowTorrent("Synthetic.Legacy.Movie.mkv")
	queries := 0
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/torrents.php" {
			queries++
			if r.URL.Query().Get("search") != "cached media title" || r.URL.Query().Get("page") != "3" {
				t.Error("legacy media query hint was replaced by recent browsing")
			}
			io.WriteString(w, workflowHTML("Synthetic.Legacy.Movie", "", false))
		} else if r.URL.Path == "/download.php" {
			w.Write(torrent)
		} else {
			t.Error("unexpected endpoint")
		}
	}))
	defer tracker.Close()
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no downloader request expected before queued execution")
	}))
	defer qb.Close()
	workflowConfigure(t, a, tracker.URL, qb.URL)
	request := NativeDownloadRequest{SiteID: "fixture-site", ResourceID: "101", Page: 1, DownloaderID: "fixture-qb", DownloadPath: source}
	if _, err := a.addNativeDownload(context.Background(), request); err == nil || queries != 0 {
		t.Fatal("missing non-browse query became an unbounded listing request")
	}
	if err := a.Store.Put(context.Background(), "resource", "fixture-site:101", map[string]any{"query": "cached media title", "page": 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.addNativeDownload(context.Background(), request); err != nil || queries != 1 {
		t.Fatal("legacy media search hint no longer works", err)
	}
	request.Browse, request.Query = true, "invalid\nquery"
	if _, err := a.addNativeDownload(context.Background(), request); err == nil || queries != 1 {
		t.Fatal("invalid browse query was sent to the site")
	}
	a.config.Sites[0].Enabled = false
	request.Browse, request.Query = false, "cached media title"
	if _, err := a.addNativeDownload(context.Background(), request); err == nil || queries != 1 {
		t.Fatal("ordinary search path bypassed the disabled-site guard")
	}
}
