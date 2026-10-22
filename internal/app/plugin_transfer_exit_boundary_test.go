package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
	transfer "light-media/internal/plugins/torrenttransfer"
)

// This owned helper runs the real transfer SDK, then deliberately remains alive
// after Serve has sent stop's reply. The delay is a test injection, not evidence
// of the historical intermittent form failure.
func TestTransferExitBoundarySDKFixture(t *testing.T) {
	at := -1
	for i, arg := range os.Args {
		if arg == "transfer-exit-boundary" {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	if len(os.Args) != at+5 {
		os.Exit(61)
	}
	pluginID, pidPath, releasePath, servedPath := os.Args[at+1], os.Args[at+2], os.Args[at+3], os.Args[at+4]
	manifest := transfer.Manifest(pluginID)
	if manifest.Validate() != nil || os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600) != nil {
		os.Exit(62)
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, transfer.Handle) != nil {
		os.Exit(63)
	}
	if os.WriteFile(servedPath, []byte("serve-returned"), 0600) != nil {
		os.Exit(64)
	}
	// The parent either releases this gate or its unchanged 10s Runner context
	// kills and reaps this process. The independent fallback bounds a faulty test.
	until := time.NewTimer(30 * time.Second)
	defer until.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if raw, err := os.ReadFile(releasePath); err == nil && string(raw) == "release" {
			os.Exit(0)
		}
		select {
		case <-tick.C:
		case <-until.C:
			os.Exit(65)
		}
	}
}

func TestTransferActualSDKExitBoundary(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("owned executable unavailable")
	}
	for _, tc := range []struct{ name, pluginID, mode string }{
		{"released_base", "TorrentTransfer", "release"},
		{"parent_cancel_music", "TorrentTransfermusic", "cancel"},
		{"deadline_base", "TorrentTransfer", "deadline"},
		{"cancel_at_wait_end_base", "TorrentTransfer", "cancel_at_wait_end"},
		{"fresh_music_after_cancellation", "TorrentTransfermusic", "release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pidPath, releasePath, servedPath := filepath.Join(dir, "pid"), filepath.Join(dir, "release"), filepath.Join(dir, "served")
			started := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			manifest := transfer.Manifest(tc.pluginID)
			wantIdentity := plugins.Identity{PluginID: tc.pluginID, InstanceID: tc.pluginID, Version: manifest.Version}
			formBytes, marshalErr := json.Marshal(map[string]any{"conf": []any{}, "model": map[string]string{"owned_instance": tc.pluginID, "owned_round": tc.name}})
			if marshalErr != nil {
				t.Fatal("owned form encoding failed")
			}
			form := json.RawMessage(formBytes)
			var observations []plugins.RunObservation
			var actionError error
			hostCalls := 0
			waitReached := false
			runner := plugins.Runner{Timeout: 10 * time.Second, ObservePhase: func(v plugins.RunObservation) {
				observations = append(observations, v)
				if v.Phase == plugins.PhaseWaitEnd && tc.mode == "cancel_at_wait_end" {
					cancel()
				}
				if v.Phase != plugins.PhaseWaitBegin {
					return
				}
				waitReached = true
				switch tc.mode {
				case "release", "cancel_at_wait_end":
					actionError = os.WriteFile(releasePath, []byte("release"), 0600)
				case "cancel":
					cancel()
				}
			}}
			got, runErr := runner.Run(ctx, plugins.Command{Path: executable, Args: []string{"-test.run=^TestTransferExitBoundarySDKFixture$", "--", "transfer-exit-boundary", tc.pluginID, pidPath, releasePath, servedPath}}, manifest, tc.pluginID, "config_form", nil, func(hostCtx context.Context, identity plugins.Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
				hostCalls++
				if hostCtx.Err() != nil || identity != wantIdentity || method != transfer.FormCapability || string(payload) != "{}" {
					t.Error("host scope or transfer form call changed")
					return nil, errors.New("owned_exit_boundary_scope")
				}
				return form, nil
			})
			elapsed := time.Since(started)
			if actionError != nil {
				t.Fatal("owned release marker failed")
			}
			if !waitReached || hostCalls != 1 || got.HostCalls != 1 {
				t.Fatalf("did not reach exactly one real form and stop/Wait boundary: wait=%v external=%d runner=%d", waitReached, hostCalls, got.HostCalls)
			}
			var phases []plugins.RunPhase
			for _, v := range observations {
				phases = append(phases, v.Phase)
			}
			wantPhases := []plugins.RunPhase{plugins.PhaseRunEntered, plugins.PhaseStartBegin, plugins.PhaseStartEnd, plugins.PhaseHandshakeBegin, plugins.PhaseHandshakeEnd, plugins.PhaseNegotiateBegin, plugins.PhaseNegotiateEnd, plugins.PhaseInvokeBegin, plugins.PhaseInvokeEnd, plugins.PhaseStopBegin, plugins.PhaseStopEnd, plugins.PhaseWaitBegin, plugins.PhaseWaitEnd, plugins.PhaseRunReturned}
			if !reflect.DeepEqual(phases, wantPhases) {
				t.Fatal("SDK invoke/stop/Wait/reap order changed", phases)
			}
			terminal := plugins.RunContextActive
			if tc.mode == "release" {
				if runErr != nil || string(got.Result) != string(form) {
					t.Fatal("released actual SDK did not return the form")
				}
				if raw, e := os.ReadFile(servedPath); e != nil || string(raw) != "serve-returned" {
					t.Fatal("normal child never completed real Serve")
				}
			} else {
				var worker *plugins.Error
				if !errors.As(runErr, &worker) || worker.Code != "execution_canceled" || len(got.Result) != 0 {
					t.Errorf("canceled exit accepted a form or changed fixed error: result_bytes=%d", len(got.Result))
				}
				terminal = plugins.RunContextCanceled
				if tc.mode == "deadline" {
					terminal = plugins.RunContextDeadline
					if !errors.Is(ctx.Err(), context.DeadlineExceeded) || elapsed < 10*time.Second {
						t.Fatal("original 10s deadline was not exercised")
					}
					if raw, e := os.ReadFile(servedPath); e != nil || string(raw) != "serve-returned" {
						t.Fatal("deadline did not occur after actual Serve returned")
					}
				} else if !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatal("parent cancellation not preserved")
				}
			}
			for i, v := range observations {
				expected := plugins.RunContextActive
				if i >= len(observations)-2 && tc.mode != "cancel_at_wait_end" || i == len(observations)-1 {
					expected = terminal
				}
				if v.ContextState != expected {
					t.Fatal("phase context changed", v.Phase.String(), v.ContextState.String())
				}
			}
			// Probe only the PID written by this owned child. No signal is delivered.
			pidBytes, e := os.ReadFile(pidPath)
			if e != nil {
				t.Fatal("owned child PID evidence missing")
			}
			pid, e := strconv.Atoi(string(pidBytes))
			if e != nil || pid <= 0 {
				t.Fatal("owned child PID evidence invalid")
			}
			if e = syscall.Kill(pid, 0); !errors.Is(e, syscall.ESRCH) {
				t.Fatal("SDK child was not reaped")
			}
			if got.ReadWireBytes == 0 || got.SentWireBytes == 0 {
				t.Fatal("real wire protocol was bypassed")
			}
			t.Logf("owned_exit_boundary mode=%s elapsed_ms=%d host_calls=%d terminal=%s", tc.mode, elapsed.Milliseconds(), got.HostCalls, terminal.String())
		})
	}
}
