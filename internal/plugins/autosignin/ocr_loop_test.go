package autosignin

import (
	"context"
	"errors"
	"testing"
)

type cancelCaptchaLoop struct {
	cancel        context.CancelFunc
	calls, sleeps int
}

func (f *cancelCaptchaLoop) Do(_ context.Context, p PageRequest) (*PageResponse, error) {
	f.calls++
	if f.calls == 1 {
		return &PageResponse{Status: 200, Text: "home", Content: []byte("home")}, nil
	}
	if f.calls == 5 {
		f.cancel()
	}
	return nil, nil
}
func TestHDSkySourceNon200LoopRemainsCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &cancelCaptchaLoop{cancel: cancel}
	_, err := OCRSignin(AdapterHDSky, ctx, Object{"url": "https://hdsky.me/"}, SiteVisitHooks{HTTP: transport})
	if !errors.Is(err, context.Canceled) || transport.calls != 5 {
		t.Fatal("source non-incrementing branch lost or cancellation swallowed", err, transport.calls)
	}
}
