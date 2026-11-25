package iyuuautoseed

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"testing"
)

type torrentReplayCall struct {
	Operation string
	URL       string
	Key       string
	Content   []byte
	Form      []TorrentFormField
}

type torrentReplayResponse struct {
	Status   int
	Content  []byte
	Location *string
}

type torrentReplayHost struct {
	t         *testing.T
	calls     []torrentReplayCall
	responses []*torrentReplayResponse
	cached    []byte
	failWrite bool
}

func (h *torrentReplayHost) Get(_ context.Context, address string) (*TorrentResponse, error) {
	h.calls = append(h.calls, torrentReplayCall{Operation: "get", URL: address})
	return h.nextResponse(), nil
}

func (h *torrentReplayHost) Post(_ context.Context, address string, fields []TorrentFormField) (*TorrentResponse, error) {
	h.calls = append(h.calls, torrentReplayCall{Operation: "post", URL: address, Form: fields})
	return h.nextResponse(), nil
}

func (h *torrentReplayHost) nextResponse() *TorrentResponse {
	h.t.Helper()
	if len(h.responses) == 0 {
		h.t.Fatal("unexpected tracker request")
	}
	row := h.responses[0]
	h.responses = h.responses[1:]
	if row == nil {
		return nil
	}
	headers := make(http.Header)
	if row.Location != nil {
		headers.Set("Location", *row.Location)
	}
	return &TorrentResponse{Status: row.Status, Content: row.Content, Text: pythonUTF8Replace(row.Content), Headers: headers}
}

func (h *torrentReplayHost) GetTorrent(_ context.Context, key string) ([]byte, error) {
	h.calls = append(h.calls, torrentReplayCall{Operation: "cache_get", Key: key})
	return h.cached, nil
}

func (h *torrentReplayHost) PutTorrent(_ context.Context, key string, content []byte) error {
	h.calls = append(h.calls, torrentReplayCall{Operation: "cache_put", Key: key, Content: content})
	if h.failWrite {
		return errors.New("synthetic cache write failure")
	}
	return nil
}

func (h *torrentReplayHost) AddInvalid(_ context.Context, address string) error {
	h.calls = append(h.calls, torrentReplayCall{Operation: "invalid", URL: address})
	return nil
}

func TestTorrentMaterial222(t *testing.T) {
	var fixture struct {
		Source string `json:"source_sha256"`
		Cases  []struct {
			Name         string
			URL          string
			InitialCache []byte `json:"initial_cache"`
			FailWrite    bool   `json:"fail_write"`
			Responses    []*torrentReplayResponse
			Calls        []torrentReplayCall
			Result       struct {
				Fault    string
				CacheKey string `json:"cache_key"`
				Content  string
				Magnet   string
				Folder   string
				Files    []string
				Message  string
			}
		}
	}
	raw, err := os.ReadFile("reference/torrent-material-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Source != "1c8a420d419ee53579079283ea0828536de9d978b6b15e722f1ba53cb51d5dd5" || len(fixture.Cases) == 0 {
		t.Fatal("pinned torrent source fixture required")
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			host := &torrentReplayHost{t: t, responses: row.Responses, cached: row.InitialCache, failWrite: row.FailWrite}
			actual, err := (TorrentHelper{HTTP: host, Cache: host}).Download(t.Context(), row.URL, true)
			expected := row.Result
			if expected.Fault != "" {
				var fault *SourceFault
				if !errors.As(err, &fault) || fault.Class != expected.Fault {
					t.Fatalf("got error %v, want source %s", err, expected.Fault)
				}
			} else if err != nil || actual.CachePath != expected.CacheKey ||
				base64.StdEncoding.EncodeToString(actual.Content) != expected.Content || actual.Magnet != expected.Magnet ||
				actual.Folder != expected.Folder || !reflect.DeepEqual(actual.Files, expected.Files) || actual.ErrorMessage != expected.Message {
				t.Fatalf("result = %+v, %v, want %+v", actual, err, expected)
			}
			if len(host.responses) != 0 || !reflect.DeepEqual(host.calls, row.Calls) {
				t.Fatalf("calls = %+v with %d responses remaining, want %+v", host.calls, len(host.responses), row.Calls)
			}
		})
	}
}
