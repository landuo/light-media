package iyuuautoseed

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

func TestIYUUHostPassKeepsMaterialGrantAcrossBudgetBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	plugin := NewPlugin()
	var calls sync.WaitGroup
	var caller func(uint64) sdk.HostCaller
	caller = func(phase uint64) sdk.HostCaller {
		return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			if method == sdk.BackgroundSubmitCapability {
				var submit struct {
					Method  string           `json:"method"`
					Payload passPhaseRequest `json:"payload"`
				}
				if err := json.Unmarshal(raw, &submit); err != nil {
					return nil, err
				}
				calls.Add(1)
				go func() {
					defer calls.Done()
					// The test admission driver starts a fresh invocation, just
					// as the host owns child callbacks beyond parent return.
					if err := plugin.continueHostPass(context.WithoutCancel(ctx), caller(submit.Payload.Phase), submit.Payload); err != nil {
						t.Error(err)
					}
				}()
				return json.RawMessage(`{"accepted":true}`), nil
			}
			return json.Marshal(phase)
		}
	}

	err := plugin.withHostPass(ctx, caller(0), func(ctx context.Context, host sdk.HostCaller) error {
		check := func(method, raw string, want uint64) error {
			value, err := host(ctx, method, json.RawMessage(raw))
			if err != nil {
				return err
			}
			var phase uint64
			if err := json.Unmarshal(value, &phase); err != nil || phase != want {
				return fmt.Errorf("request %s should use phase %d, got %s", method, want, value)
			}
			return nil
		}
		for i := 0; i < maxPhaseCalls-1; i++ {
			if err := check(sdk.LogWriteCapability, `{}`, 1); err != nil {
				return err
			}
		}
		// A token request starts on a fresh scope even before the old scope
		// becomes full. The exact returned material chain keeps that scope.
		for _, profile := range []string{"iyuu-mteam", "iyuu-mteam-material-api", "iyuu-mteam-material-cdn"} {
			if err := check(sdk.SitesSourceRequestCapability, fmt.Sprintf(`{"origin_profile":%q}`, profile), 2); err != nil {
				return err
			}
		}
		for i := 3; i < maxPhaseCalls; i++ {
			if err := check(sdk.LogWriteCapability, `{}`, 2); err != nil {
				return err
			}
		}
		return check(sdk.DownloaderSourceAddCapability, `{}`, 3)
	})
	calls.Wait()
	if err != nil {
		t.Fatal(err)
	}
}

func TestIYUUHostPassSkipsCanceledQueuedMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	owner, stopOwner := context.WithCancelCause(ctx)
	defer stopOwner(nil)
	plugin := NewPlugin()
	pass := &hostPass{ctx: owner, cancel: stopOwner, calls: make(chan passCall, 1), pending: 1}
	plugin.passes[1] = pass

	requestCtx, cancelRequest := context.WithCancel(ctx)
	cancelRequest()
	reply := make(chan passReply, 1)
	pass.calls <- passCall{ctx: requestCtx, method: sdk.DownloaderSourceAddCapability, body: json.RawMessage(`{}`), reply: reply}
	finished := make(chan error, 1)
	go func() {
		finished <- plugin.continueHostPass(ctx, func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			t.Error("canceled queued mutation reached the SDK caller")
			return nil, nil
		}, passPhaseRequest{Pass: 1, Phase: 1})
	}()
	select {
	case result := <-reply:
		if result.err != context.Canceled {
			t.Errorf("expected canceled queued mutation, got %v", result.err)
		}
	case <-ctx.Done():
		t.Error("canceled queued mutation was not answered")
	}
	stopOwner(nil)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}
