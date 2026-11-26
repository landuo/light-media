package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Block the workers until the client has received the actual queued state.
// The same POST then reports real terminal receipts, including the hard link.
func TestImmediateOrganizeStreamsBeforeFinalHardlink(t *testing.T) {
	a, src, library := importTestApp(t)
	admin := setupTestAdmin(t, a)
	importFixture(t, a, "movie")
	source := filepath.Join(src, "Test.Film.2024.mkv")
	importWrite(t, source, []byte("unique owned fixture"))
	payload, _ := json.Marshal(manualOrganizeRequest{Interactive: true, Paths: []string{source}, TargetPath: library, Mode: "hardlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/files/organize", bytes.NewReader(payload))
	request.Header.Set("Origin", server.URL)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.AddCookie(admin)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("response %s %v", response.Status, response.Header)
	}
	lines := bufio.NewScanner(response.Body)
	event := ""
	queued := false
	for lines.Scan() {
		line := lines.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		switch event {
		case "progress":
			var frame organizeFrame
			if err := json.Unmarshal([]byte(data), &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Stage == "queued" && !queued {
				if frame.Total != 1 || frame.Completed != 0 {
					t.Fatal(frame)
				}
				queued = true
				delivered, err := a.deliverOrganizeInbox(ctx)
				if err != nil || !delivered {
					t.Fatal(delivered, err)
				}
				for _, kind := range []string{"organize_file", "import"} {
					job, err := a.claimJob(ctx, allJobs)
					if err != nil || job.Kind != kind {
						t.Fatalf("claim %s: %s, %v", kind, job.Kind, err)
					}
					a.runClaimedJob(ctx, job)
				}
			}
		case "complete":
			var out manualOrganizeOutput
			if err := json.Unmarshal([]byte(data), &out); err != nil || !queued || len(out.Items) != 1 || out.Failed != 0 || out.Items[0].Status != "success" {
				t.Fatal(queued, data, err)
			}
			input, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			output, err := os.Stat(out.Items[0].Target)
			if err != nil || !os.SameFile(input, output) {
				t.Fatal(out.Items[0], err)
			}
			return
		case "error":
			t.Fatal(data)
		}
	}
	t.Fatal("missing terminal result", lines.Err())
}
