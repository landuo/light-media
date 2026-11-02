package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type rating24ReaderFunc func([]byte) (int, error)

func (f rating24ReaderFunc) Read(p []byte) (int, error) { return f(p) }

// This root tests the changed private reader and existing admission seam only.
// The 45 original schema observations and complete v1/v2 matrices are inherited,
// not regenerated here or represented by these narrow reader cases.
func TestOriginalPluginRatingCompleteBodyReader(t *testing.T) {
	large := []byte(strings.Repeat("x", (2<<20)+17))
	got, err := readOriginalPluginCenterBody(context.Background(), bytes.NewReader(large))
	if err != nil || !bytes.Equal(got, large) {
		t.Fatal("complete response was truncated", err, len(got))
	}
	final := []byte(`{"rating":4.5}`)
	reads := 0
	got, err = readOriginalPluginCenterBody(context.Background(), rating24ReaderFunc(func(p []byte) (int, error) { reads++; return copy(p, final), io.EOF }))
	if err != nil || reads != 1 || !bytes.Equal(got, final) {
		t.Fatal("final data plus EOF lost", err, reads)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reads = 0
	got, err = readOriginalPluginCenterBody(ctx, rating24ReaderFunc(func(p []byte) (int, error) { reads++; return 0, io.EOF }))
	if !errors.Is(err, context.Canceled) || got != nil || reads != 0 {
		t.Fatal("pre-cancel still read or published", err, reads)
	}
	ctx, cancel = context.WithCancel(context.Background())
	reads = 0
	got, err = readOriginalPluginCenterBody(ctx, rating24ReaderFunc(func(p []byte) (int, error) { reads++; cancel(); return copy(p, final), io.EOF }))
	if !errors.Is(err, context.Canceled) || got != nil || reads != 1 {
		t.Fatal("read-time cancellation published partial", err, reads)
	}
	fault := errors.New("owned partial read error")
	reads = 0
	got, err = readOriginalPluginCenterBody(context.Background(), rating24ReaderFunc(func(p []byte) (int, error) { reads++; return copy(p, final), fault }))
	if err != fault || got != nil || reads != 1 {
		t.Fatal("partial valid prefix became successful body", err, reads)
	}

	body := `{"rating":4.5,"padding":"` + string(large) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/plugin/rating/Owned", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	rating, err := originalPluginRatingRequest(httptest.NewRecorder(), request)
	if err != nil || rating != 4.5 {
		t.Fatal("source-ignored large field rejected", err, rating)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/plugin/rating/Owned", nil)
	request.Body = io.NopCloser(rating24ReaderFunc(func(p []byte) (int, error) { return copy(p, final), fault }))
	request.Header.Set("Content-Type", "application/json")
	_, err = originalPluginRatingRequest(httptest.NewRecorder(), request)
	var api *APIError
	if !errors.As(err, &api) || api.Status != http.StatusRequestEntityTooLarge || api.Code != "PLUGIN_RATING_BODY_LIMIT" || strings.Contains(err.Error(), fault.Error()) {
		t.Fatal("read failure changed native safe compatibility result", err)
	}
	t.Log("complete >2MiB and EOF data retained; canceled/read-error body never accepted; existing source rating admission unchanged")
}
