package seedhost

import (
	"context"
	"encoding/json"
	"light-media/internal/download"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeMutationNotStartedProofBindsExactLiveInvocationAndEffect(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode([]any{}) }))
	defer s.Close()
	c, err := download.New(download.Config{ID: "owned", Kind: "qb", BaseURL: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Control(context.Background(), badHash, "remove_keep_data")
	if !download.MutationNotStarted(err) {
		t.Fatal("actual Client read-only rejection missing", err)
	}
	inv := Invocation{Binding: Binding{InstanceID: "owned", Generation: 1, Revision: 1}, RunID: "owned-run", Action: "run", InputSHA: "owned-input"}
	e := Effect{Binding: inv.Binding, Authority: inv.Binding, RunID: inv.RunID, ID: "owned-effect", Kind: "native_write", Operation: "qb.torrents_delete", DownloaderID: "owned", DownloaderSHA: "owned-sha", DownloaderRevision: 1, Args: json.RawMessage(`{"torrent_hashes":"` + badHash + `","delete_files":false}`), Phase: "confirmed", ErrorCode: "DOWNLOADER_MUTATION_NOT_STARTED", Result: json.RawMessage(`{"protocol_not_started":true}`)}
	ctx := nativeNotStartedContext(context.Background(), inv, e, err)
	if !NativeMutationNotStarted(ctx, inv, e) {
		t.Fatal("actual live typed proof rejected")
	}
	if NativeMutationNotStarted(context.Background(), inv, e) {
		t.Fatal("JSON fields alone certified rejection")
	}
	if NativeMutationNotStarted(nil, inv, e) {
		t.Fatal("nil context certified rejection")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if NativeMutationNotStarted(canceled, inv, e) {
		t.Fatal("canceled receipt proof remained live")
	}
	for _, field := range []string{"run", "generation", "revision", "input", "method", "arguments", "target", "result"} {
		t.Run(field, func(t *testing.T) {
			otherInv, otherEffect := inv, e
			switch field {
			case "run":
				otherInv.RunID = "other-run"
			case "generation":
				otherInv.Binding.Generation++
			case "revision":
				otherInv.Binding.Revision++
			case "input":
				otherInv.InputSHA = "another-input"
			case "method":
				otherEffect.Operation = "qb.torrents_add_tags"
			case "arguments":
				otherEffect.Args = json.RawMessage(`{}`)
			case "target":
				otherEffect.DownloaderSHA = "another-client"
			case "result":
				otherEffect.Result = json.RawMessage(`{"protocol_acknowledged":true,"observed":false}`)
			}
			if NativeMutationNotStarted(ctx, otherInv, otherEffect) {
				t.Fatal("proof accepted for foreign identity")
			}
		})
	}
	remote := &download.Error{Operation: "control", Code: "DOWNLOADER_MUTATION_NOT_STARTED"}
	if NativeMutationNotStarted(nativeNotStartedContext(context.Background(), inv, e, remote), inv, e) {
		t.Fatal("error code forged live proof")
	}
}
