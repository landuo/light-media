package torrentfilter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestInstalledAdditionalFilterReference(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/torrent-filter-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Source  string `json:"source_sha256"`
		Context string `json:"context_sha256"`
		Cases   []struct {
			Name    string     `json:"name"`
			Params  Parameters `json:"params"`
			Torrent Torrent    `json:"torrent"`
			Result  struct {
				Matched bool   `json:"matched"`
				Error   string `json:"error"`
			} `json:"result"`
			Volume string `json:"volume_factor"`
		} `json:"cases"`
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 313 || fixture.Source != "1c8a420d419ee53579079283ea0828536de9d978b6b15e722f1ba53cb51d5dd5" || fixture.Context != "70a088bd54c748ab44ec467e6a1f1fc5bb5368f818bb16288eca0d15cf98877e" {
		t.Fatal("invalid installed source fixture")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			matched, err := Match(context.Background(), c.Params, c.Torrent)
			kind := ""
			if err != nil {
				kind = err.Error()
			}
			if matched != c.Result.Matched || kind != c.Result.Error {
				t.Fatalf("Go (%v,%q) original (%v,%q)", matched, kind, c.Result.Matched, c.Result.Error)
			}
			if got := VolumeFactor(c.Torrent.UploadFactor, c.Torrent.DownloadFactor); got != c.Volume {
				t.Fatal("promotion text differs", got, c.Volume)
			}
		})
	}
}
func filterText(s string) *string { return &s }
func TestAdditionalFiltersAreBoundedCancellableAndConcurrent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Match(ctx, Parameters{}, Torrent{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel not propagated", err)
	}
	bad := Parameters{Include: filterText("private-synthetic-pattern(")}
	if _, err := Match(context.Background(), bad, Torrent{Title: "Synthetic"}); err == nil || strings.Contains(err.Error(), "private-synthetic") {
		t.Fatal("invalid expression accepted or leaked")
	}
	if err := Validate(Parameters{Quality: filterText(strings.Repeat("x", 64<<10+1))}); err == nil {
		t.Fatal("expression size unbounded")
	}
	if _, err := Match(context.Background(), Parameters{Quality: filterText(strings.Repeat(`\b`, 5000))}, Torrent{Title: "Synthetic"}); err == nil || err.Error() != "RuleLimit" {
		t.Fatal("expanded pattern size unbounded", err)
	}
	if ok, err := Match(context.Background(), Parameters{Include: filterText("missing"), Quality: filterText("(")}, Torrent{Title: "safe"}); ok || err != nil {
		t.Fatal("lazy matching lost", ok, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 30; n++ {
				ok, err := Match(context.Background(), Parameters{Quality: filterText(`(?P<q>WEB)-DL`), Resolution: filterText(`(?<= )2160p`)}, Torrent{Title: "Example WEB-DL 2160p"})
				if !ok || err != nil {
					t.Error("concurrent matching changed result", err)
				}
			}
		}()
	}
	wg.Wait()
}
