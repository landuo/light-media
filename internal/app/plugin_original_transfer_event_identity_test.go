package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"light-media/internal/plugins"
)

func assertOwnedTransferDownloader(t *testing.T, rows []json.RawMessage, want any, hash string) {
	t.Helper()
	count := 0
	for _, row := range rows {
		var event plugins.MutableEvent
		if json.Unmarshal(row, &event) != nil {
			t.Fatal("actual SDK event envelope", string(row))
		}
		if event.Type != "transfer.complete" && event.Type != "transfer.failed" {
			continue
		}
		var fields map[string]any
		if json.Unmarshal(event.Data, &fields) != nil || !reflect.DeepEqual(fields["downloader"], want) || fields["download_hash"] != hash {
			t.Fatal("source history downloader identity", string(row), want, hash)
		}
		if fields["transfer_history_id"] == nil || fields["transfer_history_id"] == "" {
			t.Fatal("missing committed history", string(row))
		}
		count++
	}
	if count != 1 {
		t.Fatal("one physical result callback", count)
	}
}
