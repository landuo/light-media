package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadFileDomainEventRejectsUnbackedOrAdditionalAuthority(t *testing.T) {
	a := newTestApp(t)
	id := "pfe_" + strings.Repeat("a", 64)
	base := DomainEvent{Type: "downloadfile.deleted", FileEventID: id, EventID: domainEventID("downloadfile.deleted", id)}
	for _, mutate := range []func(*DomainEvent){
		func(e *DomainEvent) { e.IntentID = "invented-intent" },
		func(e *DomainEvent) { e.NativeDownloadID = "invented-download" },
		func(e *DomainEvent) { e.FileEventID = "/private/file" },
		func(e *DomainEvent) { e.Origin = "plugin" },
		func(e *DomainEvent) { e.Purpose = "cross_seed" },
		func(e *DomainEvent) { e.MediaID = "movie:123" },
		func(e *DomainEvent) { e.EventID = domainEventID(e.Type, "other") },
		func(e *DomainEvent) { e.SubjectKind = "download" },
		func(e *DomainEvent) { e.JobID = "unrelated-run" },
	} {
		v := base
		mutate(&v)
		if _, err := validateDomainEvent(v); err == nil {
			t.Fatal("additional authority accepted")
		}
	}
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := recordDomainEventTx(context.Background(), tx, base); pluginErrorCode(err) != "FILE_EVENT_UNCOMMITTED" {
		t.Fatal("unbacked private event accepted", err)
	}
	core := domainFixture("download.accepted", "core")
	core.FileEventID = id
	if _, err := validateDomainEvent(core); err == nil {
		t.Fatal("file fact attached to unrelated core download")
	}
}

func TestPluginRemoveLinkOutboxInstalledSourceReferenceIntegrity(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "plugins", "remove-link-outbox")
	for file, want := range map[string]string{
		"installed-source.json":          "f72c9142c01970d2ddabe03ddfda1012a14c65c8c4003c15f287ee2ad4277f0f",
		"installed-function-oracle.json": "ff03a5032d05e6e7baa206a61f9c9eef46a1634f282a3aa1c5bdc8218efb0f00",
		"generate_oracle.py":             "cc7c91b289dc5e28ae4405d7ab0df181581fd066bce5f83cb70d76978bb67cd6",
	} {
		raw, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("installed reference changed without review", file)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "installed-function-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Downloads []struct {
			Name  string            `json:"name"`
			Trace []json.RawMessage `json:"trace"`
		} `json:"download_cases"`
		Notifications []json.RawMessage `json:"notification_cases"`
		Defaults      struct {
			MType                   string `json:"mtype"`
			Link                    string `json:"link"`
			Source, UserID, Channel any
			SaveHistory             bool `json:"save_history"`
		} `json:"plugin_notification_defaults"`
		Broadcast struct {
			Queued   int `json:"queued"`
			Handlers int `json:"handlers_called_inline"`
		} `json:"broadcast_enqueue"`
	}
	// The 17 AST scenarios establish source boundaries; the hashful downloader
	// and routing scenarios are not counted as implemented Go consumer branches.
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle.Downloads) != 8 || len(oracle.Notifications) != 7 {
		t.Fatal("source oracle inventory incomplete")
	}
	for _, v := range oracle.Downloads {
		if strings.HasPrefix(v.Name, "src-only-") && len(v.Trace) != 0 {
			t.Fatal("installed core unexpectedly acts on src-only event")
		}
	}
	if oracle.Defaults.MType != "站点" || !oracle.Defaults.SaveHistory || oracle.Defaults.Source != nil || oracle.Defaults.UserID != nil || oracle.Defaults.Channel != nil || !strings.HasSuffix(oracle.Defaults.Link, "#/plugins?tab=installed&id=RemoveLink") {
		t.Fatal("source notification defaults changed")
	}
	if oracle.Broadcast.Queued != 1 || oracle.Broadcast.Handlers != 0 {
		t.Fatal("source enqueue unexpectedly acknowledges inline delivery")
	}
}
