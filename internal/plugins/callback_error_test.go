package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	old "light-media/internal/plugins/testdata/legacywire"
)

func callbackErrorCommand(t *testing.T, mode string) Command {
	t.Helper()
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Path: p, Args: []string{"-test.run=^TestCallbackErrorWorker$", "--", mode}}
}

func TestCallbackErrorWorker(t *testing.T) {
	args := os.Args
	if len(args) < 3 || args[len(args)-2] != "--" {
		return
	}
	mode := args[len(args)-1]
	handle := func(ctx context.Context, method string, raw json.RawMessage, host HostCaller) (json.RawMessage, error) {
		if method == "stop" {
			return json.RawMessage(`null`), nil
		}
		switch string(raw) {
		case `"empty"`:
			return nil, errors.New("")
		case `"large"`:
			return nil, errors.New(strings.Repeat("文", 4000))
		case `"host"`:
			_, err := host(ctx, "data.read", json.RawMessage(`{}`))
			return nil, fmt.Errorf("outer private context: %w", err)
		case `"canceled"`:
			return nil, fmt.Errorf("outer private context: %w", context.Canceled)
		default:
			return nil, errors.New("owned callback detail")
		}
	}
	if mode == "old" {
		_ = old.Serve(context.Background(), os.Stdin, os.Stdout, oldManifest(), func(ctx context.Context, method string, raw json.RawMessage, host old.HostCaller) (json.RawMessage, error) {
			return handle(ctx, method, raw, HostCaller(host))
		})
	} else if mode == "concurrent" {
		_ = ServeConcurrent(context.Background(), os.Stdin, os.Stdout, fixtureManifest(), handle)
	} else {
		_ = Serve(context.Background(), os.Stdin, os.Stdout, fixtureManifest(), handle)
	}
	os.Exit(0)
}

func TestCallbackErrorNegotiatedActualProcesses(t *testing.T) {
	for _, mode := range []string{"single", "serial", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			runner := Runner{Timeout: 5 * time.Second, ConcurrentCalls: mode == "concurrent"}
			host := func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				return nil, errors.New("owned host secret")
			}
			var client *Client
			if mode != "single" {
				var err error
				client, err = runner.Start(context.Background(), callbackErrorCommand(t, mode), fixtureManifest(), sessionBinding())
				if err != nil {
					t.Fatal(err)
				}
				defer client.Stop(context.Background())
			}
			for _, value := range []string{"ordinary", "empty", "large", "host", "canceled"} {
				payload, _ := json.Marshal(value)
				var err error
				if client == nil {
					_, err = runner.Run(context.Background(), callbackErrorCommand(t, mode), fixtureManifest(), "Fixture", "invoke", payload, host)
				} else {
					_, err = client.Call(context.Background(), "invoke", payload, host)
				}
				if codeOf(err) != "plugin_operation_failed" || strings.Contains(err.Error(), "owned") || strings.Contains(err.Error(), "private") {
					t.Fatalf("safe Error(): %v", err)
				}
				detail, present := CallbackErrorDetail(err)
				if value == "host" || value == "canceled" || value == "large" {
					if present {
						t.Fatal("host/cancel/oversized detail escaped")
					}
					continue
				}
				if !present {
					t.Fatal("negotiated detail lost", value)
				}
				if value == "ordinary" && detail != "owned callback detail" || value == "empty" && detail != "" {
					t.Fatal("callback detail changed", value, len(detail))
				}
			}
		})
	}
}

func TestCallbackErrorFrozenOldBothDirections(t *testing.T) {
	_, err := (Runner{}).Run(context.Background(), callbackErrorCommand(t, "old"), fixtureManifest(), "Fixture", "invoke", json.RawMessage(`"ordinary"`), nil)
	if codeOf(err) != "plugin_operation_failed" {
		t.Fatal(err)
	}
	if _, ok := CallbackErrorDetail(err); ok {
		t.Fatal("old worker invented detail")
	}
	c := callbackErrorCommand(t, "serial")
	_, err = (old.Runner{}).Run(context.Background(), old.Command{Path: c.Path, Args: c.Args}, oldManifest(), "Fixture", "invoke", json.RawMessage(`"ordinary"`), nil)
	if err == nil || strings.Contains(err.Error(), "detail") {
		t.Fatal("old host incompatible or leaked", err)
	}
}

func TestCallbackErrorDetailValidation(t *testing.T) {
	detail := "owned"
	for _, test := range []struct{ kind, code string }{{"host_result", "HOST_DENIED"}, {"result", "TRANSPORT_MESSAGE_LIMIT"}, {"result", "EXECUTION_CANCELED"}} {
		f := Frame{Protocol: 1, ID: "owned", Type: test.kind, Identity: Identity{PluginID: "Fixture", InstanceID: "Fixture", Version: "1.2.3"}, Error: &ReplyError{Code: test.code, Detail: &detail}}
		if _, err := json.Marshal(f); err == nil {
			t.Fatal("noncallback detail accepted", test)
		}
	}
	if codeOf(workerReplyCallbackFailure(&ReplyError{Code: "OPERATION_FAILED", Detail: &detail}, false)) != "invalid_frame" {
		t.Fatal("unnegotiated detail accepted")
	}
	if _, ok := CallbackErrorDetail(fmt.Errorf("wrapper: %w", failure("host_operation_failed"))); ok {
		t.Fatal("host error invented detail")
	}
}
