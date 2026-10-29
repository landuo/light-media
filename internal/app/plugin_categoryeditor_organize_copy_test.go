package app

// This is a real local COPY acceptance, not another preview test: the actual
// standard CategoryEditor executable saves policy through the authenticated
// host, and the production background jobs worker publishes an owned synthetic
// MP4. Metadata is a strictly bounded in-memory fixture; no public TMDB request,
// real API key, existing media, NAS or current MoviePilot configuration is used.
// The classification oracle is the 2026-10-20 archived helper's controlled
// replay (including its WeakSingleton type adapter), not a current NAS run or
// proof of original singleton lifecycle behavior.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
)

// One black 16x16 MPEG-4 frame, one second. Independently generated and ffprobe
// checked, not downloaded media. Embedded so the test needs no ffmpeg runtime.
// Generation: ffmpeg -f lavfi -i color=c=black:s=16x16:r=1:d=1 -frames:v 1
// -c:v mpeg4 -q:v 31 -fflags +bitexact -flags:v +bitexact -map_metadata -1
// -metadata title='SYNTHETIC CategoryEditor COPY acceptance' synthetic.mp4
const categoryEditorSyntheticCopyMP4 = "AAAAHGZ0eXBpc29tAAACAGlzb21pc28ybXA0MQAAAAhmcmVlAAAAGW1kYXQAAAGzABAHAAABthY/GLG37wAAA0ttb292AAAAbG12aGQAAAAAAAAAAAAAAAAAAAPoAAAD6AABAAABAAAAAAAAAAAAAAAAAQAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACAAACWnRyYWsAAABcdGtoZAAAAAMAAAAAAAAAAAAAAAEAAAAAAAAD6AAAAAAAAAAAAAAAAAAAAAAAAQAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAEAAAAAAEAAAABAAAAAAACRlZHRzAAAAHGVsc3QAAAAAAAAAAQAAA+gAAAAAAAEAAAAAAdJtZGlhAAAAIG1kaGQAAAAAAAAAAAAAAAAAAEAAAABAAFXEAAAAAAAtaGRscgAAAAAAAAAAdmlkZQAAAAAAAAAAAAAAAFZpZGVvSGFuZGxlcgAAAAF9bWluZgAAABR2bWhkAAAAAQAAAAAAAAAAAAAAJGRpbmYAAAAcZHJlZgAAAAAAAAABAAAADHVybCAAAAABAAABPXN0YmwAAADZc3RzZAAAAAAAAAABAAAAyW1wNHYAAAAAAAAAAQAAAAAAAAAAAAAAAAAAAAAAEAAQAEgAAABIAAAAAAAAAAEKTGF2YyBtcGVnNAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAY//8AAABPZXNkcwAAAAADgICAPgABAASAgIAwIBEAAAAAAw1AAAAAiAWAgIAeAAABsAEAAAG1iRMAAAEAAAABIADEjYgADQCEAhRjBoCAgAECAAAAEHBhc3AAAAABAAAAAQAAABRidHJ0AAAAAAADDUAAAACIAAAAGHN0dHMAAAAAAAAAAQAAAAEAAEAAAAAAHHN0c2MAAAAAAAAAAQAAAAEAAAABAAAAAQAAABRzdHN6AAAAAAAAABEAAAABAAAAFHN0Y28AAAAAAAAAAQAAACwAAAB9dWR0YQAAAHVtZXRhAAAAAAAAACFoZGxyAAAAAAAAAABtZGlyYXBwbAAAAAAAAAAAAAAAAEhpbHN0AAAAQKluYW0AAAA4ZGF0YQAAAAEAAAAAU1lOVEhFVElDIENhdGVnb3J5RWRpdG9yIENPUFkgYWNjZXB0YW5jZQ=="

func categoryEditorCopySourceCase(t *testing.T) (string, catalog.Media, string) {
	t.Helper()
	categoryRuntimeArchivedSource(t)
	readBound := func(name, want string, out any) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("../../migration/evidence/categoryeditor/source", name))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != want {
			t.Fatalf("independent source evidence binding changed: %s: %v", name, err)
		}
		if err = json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
	var inputs struct {
		Media catalog.Media `json:"media"`
		Cases []struct {
			ID            string `json:"id"`
			ContentBase64 string `json:"content_base64"`
		} `json:"cases"`
	}
	var oracle struct {
		SourceSHA256 string `json:"source_sha256"`
		InputsSHA256 string `json:"inputs_sha256"`
		Cases        []struct {
			ID   string `json:"id"`
			Warm struct {
				Lookups map[string]categorySemanticsSourceCall `json:"lookups"`
			} `json:"warm"`
		} `json:"cases"`
	}
	const inputSHA = "a9224f02261b09a20d780099463201b74d198a2370e68bec58290f39a874c780"
	readBound("categoryeditor-source-helper-inputs.json", inputSHA, &inputs)
	readBound("categoryeditor-source-helper-oracle.json", "9fa95d762b18da0ee90973f4b494bf508a4ce68546834d11d214795df66d78a6", &oracle)
	if oracle.SourceSHA256 != "1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2" || oracle.InputsSHA256 != inputSHA {
		t.Fatal("oracle is not bound to the fixed archived helper and metadata input")
	}
	var content, category string
	for _, c := range inputs.Cases {
		if c.ID == "ordered_and_invert" {
			raw, err := base64.StdEncoding.DecodeString(c.ContentBase64)
			if err != nil {
				t.Fatal(err)
			}
			content = string(raw)
		}
	}
	for _, c := range oracle.Cases {
		if c.ID == "ordered_and_invert" {
			call := c.Warm.Lookups["movie_match"]
			if call.Exception != nil || call.Return == nil || call.Return.Type != "str" || json.Unmarshal(call.Return.Value, &category) != nil {
				t.Fatal("source oracle does not record a successful movie category")
			}
		}
	}
	if content == "" || category == "" {
		t.Fatal("missing independent ordered-and-inverted source case")
	}
	return content, inputs.Media, category
}

func TestCategoryEditorStandardActualOrganizeCopyConsumesSourcePolicy(t *testing.T) {
	content, identity, category := categoryEditorCopySourceCase(t)
	f := newCategoryEditorStandardFixture(t, nil)
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, library := filepath.Join(base, "synthetic-source"), filepath.Join(base, "synthetic-library")
	for _, dir := range []string{source, library} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	settings := f.app.Settings()
	if settings.TMDB.APIKey != "" || settings.TMDB.AccessToken != "" || len(settings.Sites) != 0 || len(settings.Downloaders) != 0 || settings.FNOS.Enabled {
		t.Fatal("COPY acceptance requires an owned App without external credentials or integrations")
	}
	settings.AllowedRoots = []string{source}
	settings.Roots = []Root{{ID: "synthetic-category-copy", Path: library, Kind: "mixed"}}
	if err = f.app.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	// Keep all classifier inputs from the source oracle. Add only the synthetic
	// identity/naming fields needed by the organizer; never ask the Go classifier
	// to manufacture an expected category.
	identity.ID, identity.MediaType = 123, "movie"
	identity.Title, identity.OriginalTitle = "SYNTHETIC CategoryEditor Copy", "SYNTHETIC CategoryEditor Copy"
	identity.Year, identity.CategoryMetadataKnown = 2020, true
	for _, id := range identity.GenreIDs {
		identity.Genres = append(identity.Genres, catalog.Genre{ID: id, Name: "Synthetic oracle genre"})
	}
	importCache(t, f.app, "media:movie:123", identity)
	provider, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	var metadataCalls atomic.Int32
	metadataClient := installMetadataFixture(t, f.app, "synthetic-category-copy-not-a-real-key", []string{"/3/movie/123"}, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("append_to_response") != "images,credits,alternative_titles,translations,release_dates,external_ids" {
			return nil, errors.New("unexpected metadata projection in synthetic CategoryEditor COPY")
		}
		metadataCalls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(provider)), Request: r}, nil
	})
	inputBytes, err := base64.StdEncoding.DecodeString(categoryEditorSyntheticCopyMP4)
	const sourceSHA = "d1fdbcf944d537ff68bb8b6724b2fb92c8855c368a1a39dea5d9e05ef6978217"
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(inputBytes)) != sourceSHA {
		t.Fatal("embedded synthetic MP4 fixture changed", err)
	}
	input := filepath.Join(source, "SYNTHETIC CategoryEditor Copy (2020).mp4")
	if err = os.WriteFile(input, inputBytes, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	f.save(t, categoryEditorStandardID, map[string]any{"enabled": true, "content": content})
	f.bytes(t, content)
	f.disabled(t, categoryEditorStandardID, content)
	f.notification(t)
	request := manualOrganizeRequest{Paths: []string{input}, TargetStorage: "local", TargetPath: library, Mode: "copy", MediaType: "movie", TMDBID: identity.ID, Scraping: importFlag(false), LibraryTypeFolder: importFlag(true)}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, f.app.Handler(), "POST", "/api/files/organize", string(raw), f.cookie, "")
	var envelope struct {
		Data manualOrganizeOutput `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Data.Total != 1 || envelope.Data.Submitted != 1 || envelope.Data.Failed != 0 || len(envelope.Data.Items) != 1 {
		t.Fatalf("formal organize HTTP entry did not submit one real COPY: %d %s", response.Code, response.Body.String())
	}
	item := envelope.Data.Items[0]
	wantTarget := filepath.Join(library, category, "SYNTHETIC CategoryEditor Copy (2020)", "SYNTHETIC CategoryEditor Copy (2020).mp4")
	if item.PlanID == "" || item.JobID == "" || item.Status != "queued" || item.Mode != "copy" || item.Target != wantTarget {
		t.Fatalf("submitted COPY differs from archived source category %q: %+v", category, item)
	}
	plan, job := historyUpsertAttempt(t, f.app, item.JobID)
	if plan.ID != item.PlanID || plan.State != "queued" || plan.Mode != "copy" || plan.Files[0].Method != "copy" || plan.Files[0].Target != wantTarget || job.State != "queued" || job.Attempt != 0 {
		t.Fatalf("formal submission was not persisted as a queued COPY: plan=%+v job=%+v", plan, job)
	}
	if _, err = os.Stat(wantTarget); !os.IsNotExist(err) {
		t.Fatal("submission unexpectedly bypassed the background worker", err)
	}
	// Run the exact production queue loop (including real claim, safeRun and
	// finishJob), not a mock executor or a test-forced running/succeeded state.
	workerCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() { defer close(stopped); f.app.worker(workerCtx) }()
	t.Cleanup(func() { cancel(); <-stopped })
	deadline := time.Now().Add(20 * time.Second)
	for {
		job, err = scanJob(f.app.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, item.JobID))
		if err != nil {
			t.Fatal(err)
		}
		if job.State == "succeeded" {
			break
		}
		if job.State != "queued" && job.State != "running" || time.Now().After(deadline) {
			t.Fatalf("real COPY worker did not succeed: %+v", job)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-stopped
	plan, job = historyUpsertAttempt(t, f.app, item.JobID)
	if plan.State != "complete" || plan.Files[0].Status != "imported" || plan.Files[0].Target != wantTarget || plan.Files[0].Method != "copy" || job.Attempt != 1 || job.Generation != 1 || job.ErrorCode != "" {
		t.Fatalf("durable execution did not finish the classified COPY: plan=%+v job=%+v", plan, job)
	}
	var receipt importReceipt
	if err = f.app.Store.Get(ctx, "import_receipt", plan.ID+"/"+plan.Files[0].ID, &receipt); err != nil || receipt.State != "published" || receipt.Source != input || receipt.Target != wantTarget || receipt.SHA256 != sourceSHA {
		t.Fatalf("COPY publication receipt missing or inconsistent: %+v err=%v", receipt, err)
	}
	targetBytes, err := os.ReadFile(wantTarget)
	if err != nil || !bytes.Equal(targetBytes, inputBytes) || fmt.Sprintf("%x", sha256.Sum256(targetBytes)) != sourceSHA {
		t.Fatal("published target bytes/SHA256 differ from the synthetic MP4", err)
	}
	after, err := os.Stat(input)
	readBack, readErr := os.ReadFile(input)
	targetInfo, targetErr := os.Stat(wantTarget)
	if err != nil || readErr != nil || targetErr != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || !bytes.Equal(readBack, inputBytes) || os.SameFile(after, targetInfo) {
		t.Fatal("COPY must retain the original inode/mtime/bytes and create a distinct target inode", err, readErr, targetErr)
	}
	history := historyUpsertOnly(t, f.app, "")
	if history.PlanID != plan.ID || history.JobID != job.ID || history.JobState != "succeeded" || history.Status != "success" || history.Source != input || history.Target != wantTarget || history.Mode != "copy" {
		t.Fatalf("durable organize history is not the successful COPY: %+v", history)
	}
	var jobs int
	if err = f.app.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("synthetic COPY unexpectedly created other jobs", jobs, err)
	}
	if metadataCalls.Load() != 1 || f.app.catalog() != metadataClient {
		t.Fatal("COPY did not retain the strictly bounded metadata fixture", metadataCalls.Load())
	}
	t.Logf("SYNTHETIC real COPY: archived oracle=ordered_and_invert category=%q; plan=%s queued->complete file=imported; job=%s queued->succeeded attempt=%d; receipt=%s sha256=%s bytes=%d; history=success; source inode/mtime/bytes preserved; target distinct inode; no public provider/NAS/download/scrape", category, plan.ID, job.ID, job.Attempt, receipt.State, receipt.SHA256, len(targetBytes))
}
